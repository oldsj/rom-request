// Package prowlarr searches a Prowlarr instance's indexers.
package prowlarr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Release is one search result. DownloadURL and MagnetURL may carry Prowlarr's API key: keep
// them on the server.
type Release struct {
	GUID        string     `json:"guid"`
	Title       string     `json:"title"`
	Indexer     string     `json:"indexer"`
	Size        int64      `json:"size"`
	Seeders     *int       `json:"seeders"`
	Leechers    *int       `json:"leechers"`
	PublishDate *time.Time `json:"publishDate"`
	InfoURL     string     `json:"infoUrl"`
	DownloadURL string     `json:"downloadUrl"`
	MagnetURL   string     `json:"magnetUrl"`
	InfoHash    string     `json:"infoHash"`
	Protocol    string     `json:"protocol"`
	Categories  []Category `json:"categories"`
}

// Category is a Newznab category.
type Category struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// Client calls Prowlarr's v1 API.
type Client struct {
	base   *url.URL
	apiKey string
	hc     *http.Client
}

// New makes a Client. baseURL has been validated by config.
func New(baseURL, apiKey string, hc *http.Client) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil {
		return nil, errors.New("prowlarr: invalid URL")
	}
	return &Client{base: u, apiKey: apiKey, hc: hc}, nil
}

// MaxResults caps what one search returns.
const MaxResults = 100

// Search runs an interactive search across the configured indexers and returns its torrent
// results (Usenet results are dropped: there is no Usenet downloader).
func (c *Client) Search(ctx context.Context, query string, categories []int) ([]Release, error) {
	q := url.Values{"query": {query}, "type": {"search"}, "limit": {strconv.Itoa(MaxResults)}}
	for _, cat := range categories {
		q.Add("categories", strconv.Itoa(cat))
	}
	u := c.base.JoinPath("api/v1/search")
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("prowlarr: %w", err)
	}
	req.Header.Set("X-Api-Key", c.apiKey)
	req.Header.Set("Accept", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("prowlarr: search: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		return nil, fmt.Errorf("prowlarr: search: status %d", resp.StatusCode)
	}
	var all []Release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(&all); err != nil {
		return nil, fmt.Errorf("prowlarr: search: decode: %w", err)
	}
	out := make([]Release, 0, len(all))
	for _, r := range all {
		if r.Protocol == "torrent" && (r.DownloadURL != "" || r.MagnetURL != "") {
			out = append(out, r)
		}
	}
	if len(out) > MaxResults {
		out = out[:MaxResults]
	}
	return out, nil
}
