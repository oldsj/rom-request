// Package qbittorrent drives qBittorrent through its Web API (v2, qBittorrent 4.1 and later,
// including 5.x).
package qbittorrent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
)

// Torrent is the part of /torrents/info this app reads.
type Torrent struct {
	Hash     string  `json:"hash"`
	Name     string  `json:"name"`
	State    string  `json:"state"`
	Progress float64 `json:"progress"`
	SavePath string  `json:"save_path"`
	Size     int64   `json:"size"`
	// The torrent's v1 and v2 info-hashes (qBittorrent 4.4+). Hash is one of them: on
	// libtorrent 2.x builds the truncated v2 hash whenever there is one.
	InfohashV1 string `json:"infohash_v1"`
	InfohashV2 string `json:"infohash_v2"`
}

// Matches reports whether the torrent is known by any of ids (lower-case hex, 40 characters).
func (t Torrent) Matches(ids []string) bool {
	v2 := t.InfohashV2
	if len(v2) > 40 {
		v2 = v2[:40]
	}
	for _, id := range ids {
		for _, h := range []string{t.Hash, t.InfohashV1, v2} {
			if h != "" && strings.EqualFold(h, id) {
				return true
			}
		}
	}
	return false
}

// File is one file of a torrent, from /torrents/files. Name is relative to the save path and
// uses "/" separators.
type File struct {
	Name     string  `json:"name"`
	Size     int64   `json:"size"`
	Progress float64 `json:"progress"`
	Priority int     `json:"priority"`
}

// Complete reports whether every wanted byte is on disk and qBittorrent has finished with it
// (seeding, or stopped after completing).
func (t Torrent) Complete() bool {
	if t.Progress < 1 {
		return false
	}
	switch t.State {
	case "uploading", "stalledUP", "queuedUP", "forcedUP", "pausedUP", "stoppedUP":
		return true
	}
	return false
}

// Errored reports a state that won't recover on its own.
func (t Torrent) Errored() bool { return t.State == "error" || t.State == "missingFiles" }

// Add describes a torrent to add.
type Add struct {
	File     []byte
	Magnet   string
	SavePath string
	Category string
	Tags     []string
}

// ErrRejected means qBittorrent refused to add the torrent (often: it already has it).
var ErrRejected = errors.New("qbittorrent: torrent not added")

// Client is a signed-in Web API client. It signs in on first use and again when the session
// expires.
type Client struct {
	base               *url.URL
	username, password string
	hc                 *http.Client

	mu       sync.Mutex
	signedIn bool
}

// New makes a Client. Leave username and password empty when qBittorrent bypasses
// authentication for this client.
func New(baseURL, username, password string, hc *http.Client) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil {
		return nil, errors.New("qbittorrent: invalid URL")
	}
	jar, _ := cookiejar.New(nil)
	c := *hc
	c.Jar = jar
	return &Client{base: u, username: username, password: password, hc: &c, signedIn: username == ""}, nil
}

func (c *Client) login(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.signedIn {
		return nil
	}
	form := url.Values{"username": {c.username}, "password": {c.password}}
	resp, err := c.send(ctx, http.MethodPost, "auth/login", nil, "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
	_ = resp.Body.Close()
	// Up to 5.2 a sign-in is 200 "Ok." (a refusal 200 "Fails."); 5.3 answers 204 with no body
	// (a refusal 401). Its session cookie, QBT_SID_<port>, comes only with a new session.
	ok := resp.StatusCode == http.StatusOK && strings.TrimSpace(string(body)) == "Ok." ||
		resp.StatusCode == http.StatusNoContent
	if !ok {
		return fmt.Errorf("qbittorrent: sign-in refused (status %d)", resp.StatusCode)
	}
	c.signedIn = true
	return nil
}

func (c *Client) send(ctx context.Context, method, path string, query url.Values, contentType string, body io.Reader) (*http.Response, error) {
	u := c.base.JoinPath("api/v2", path)
	u.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, fmt.Errorf("qbittorrent: %w", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	// qBittorrent's CSRF check compares these with its own address.
	req.Header.Set("Referer", c.base.String())
	req.Header.Set("Origin", c.base.Scheme+"://"+c.base.Host)
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("qbittorrent: %s: %w", path, err)
	}
	return resp, nil
}

// call signs in if needed, sends the request, and signs in again once on 403 (an expired
// session). body is a func so it can be rebuilt for the retry.
func (c *Client) call(ctx context.Context, method, path string, query url.Values, body func() (string, io.Reader)) ([]byte, int, error) {
	for attempt := range 2 {
		if err := c.login(ctx); err != nil {
			return nil, 0, err
		}
		ct, r := "", io.Reader(nil)
		if body != nil {
			ct, r = body()
		}
		resp, err := c.send(ctx, method, path, query, ct, r)
		if err != nil {
			return nil, 0, err
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		_ = resp.Body.Close()
		if err != nil {
			return nil, 0, fmt.Errorf("qbittorrent: %s: %w", path, err)
		}
		if resp.StatusCode == http.StatusForbidden && attempt == 0 && c.username != "" {
			c.mu.Lock()
			c.signedIn = false
			c.mu.Unlock()
			continue
		}
		return data, resp.StatusCode, nil
	}
	return nil, 0, fmt.Errorf("qbittorrent: %s: forbidden after signing in", path)
}

// Add adds a torrent, not using automatic torrent management, so SavePath applies.
func (c *Client) Add(ctx context.Context, a Add) error {
	body := func() (string, io.Reader) {
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		if a.File != nil {
			fw, _ := w.CreateFormFile("torrents", "release.torrent")
			_, _ = fw.Write(a.File)
		} else {
			_ = w.WriteField("urls", a.Magnet)
		}
		_ = w.WriteField("savepath", a.SavePath)
		_ = w.WriteField("autoTMM", "false")
		if a.Category != "" {
			_ = w.WriteField("category", a.Category)
		}
		if len(a.Tags) > 0 {
			_ = w.WriteField("tags", strings.Join(a.Tags, ","))
		}
		_ = w.Close()
		return w.FormDataContentType(), &buf
	}
	data, status, err := c.call(ctx, http.MethodPost, "torrents/add", nil, body)
	if err != nil {
		return err
	}
	text := strings.TrimSpace(string(data))
	switch {
	// Up to 5.1 a refusal is "Fails."; from 5.2 it is 409 Conflict (415 for an unreadable file).
	case status == http.StatusConflict || status == http.StatusUnsupportedMediaType || text == "Fails.":
		return ErrRejected
	case status != http.StatusOK && status != http.StatusAccepted:
		return fmt.Errorf("qbittorrent: add: status %d", status)
	}
	// 5.x may answer with JSON counts instead of "Ok.".
	var counts struct {
		Failure *int `json:"failure_count"`
	}
	if json.Unmarshal(data, &counts) == nil && counts.Failure != nil && *counts.Failure > 0 {
		return ErrRejected
	}
	return nil
}

// Info returns the torrents with these ids that qBittorrent has.
func (c *Client) Info(ctx context.Context, hashes []string) ([]Torrent, error) {
	if len(hashes) == 0 {
		return nil, nil
	}
	data, status, err := c.call(ctx, http.MethodGet, "torrents/info", url.Values{"hashes": {strings.Join(hashes, "|")}}, nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("qbittorrent: info: status %d", status)
	}
	var out []Torrent
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("qbittorrent: info: decode: %w", err)
	}
	return out, nil
}

// Files lists a torrent's files.
func (c *Client) Files(ctx context.Context, hash string) ([]File, error) {
	data, status, err := c.call(ctx, http.MethodGet, "torrents/files", url.Values{"hash": {hash}}, nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("qbittorrent: files: status %d", status)
	}
	var out []File
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("qbittorrent: files: decode: %w", err)
	}
	return out, nil
}

// Delete removes a torrent, and its downloaded data if deleteFiles.
func (c *Client) Delete(ctx context.Context, hash string, deleteFiles bool) error {
	body := func() (string, io.Reader) {
		f := url.Values{"hashes": {hash}, "deleteFiles": {fmt.Sprint(deleteFiles)}}
		return "application/x-www-form-urlencoded", strings.NewReader(f.Encode())
	}
	_, status, err := c.call(ctx, http.MethodPost, "torrents/delete", nil, body)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("qbittorrent: delete: status %d", status)
	}
	return nil
}
