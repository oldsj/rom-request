// Package torrent identifies torrents: the id qBittorrent knows a torrent by, computed from a
// .torrent file or read from a magnet link, so a request follows exactly the torrent it added.
package torrent

import (
	"context"
	"crypto/sha1" //nolint:gosec // BitTorrent v1 info-hashes are SHA-1 by definition
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// MaxTorrentBytes caps a downloaded .torrent file.
const MaxTorrentBytes = 10 << 20

// ErrInvalid means the input is not a usable torrent or magnet link.
var ErrInvalid = errors.New("torrent: invalid")

// Source is what to hand qBittorrent: a .torrent file or a magnet link, and its id.
type Source struct {
	File   []byte
	Magnet string
	// Hash is qBittorrent's id for the torrent: the v1 info-hash, or for a v2-only torrent the
	// v2 info-hash truncated to 20 bytes. Lower-case hex.
	Hash string
}

// ID computes the id of a .torrent file.
func ID(file []byte) (string, error) {
	info, err := infoDict(file)
	if err != nil {
		return "", err
	}
	if v2Only(info) {
		sum := sha256.Sum256(info)
		return hex.EncodeToString(sum[:20]), nil
	}
	sum := sha1.Sum(info) //nolint:gosec // see import
	return hex.EncodeToString(sum[:]), nil
}

// MagnetID reads the id from a magnet link's xt parameter (btih in hex or base32, or btmh).
func MagnetID(magnet string) (string, error) {
	u, err := url.Parse(magnet)
	if err != nil || u.Scheme != "magnet" {
		return "", fmt.Errorf("%w: not a magnet link", ErrInvalid)
	}
	var v2 string
	for _, xt := range u.Query()["xt"] {
		switch {
		case strings.HasPrefix(xt, "urn:btih:"):
			h := strings.TrimPrefix(xt, "urn:btih:")
			switch len(h) {
			case 40:
				if _, err := hex.DecodeString(h); err == nil {
					return strings.ToLower(h), nil
				}
			case 32:
				if b, err := base32.StdEncoding.DecodeString(strings.ToUpper(h)); err == nil {
					return hex.EncodeToString(b), nil
				}
			}
		case strings.HasPrefix(xt, "urn:btmh:1220"):
			// A multihash: 0x12 (sha2-256), 0x20 (32 bytes), then the v2 info-hash.
			h := strings.TrimPrefix(xt, "urn:btmh:1220")
			if b, err := hex.DecodeString(h); err == nil && len(b) == 32 {
				v2 = hex.EncodeToString(b[:20])
			}
		}
	}
	if v2 != "" {
		return v2, nil
	}
	return "", fmt.Errorf("%w: magnet link has no info-hash", ErrInvalid)
}

// Resolve turns a release into a Source: the magnet link if there is one, otherwise the
// .torrent file at downloadURL, which may itself redirect to a magnet link (Prowlarr does
// that for some indexers). downloadURL comes from server configuration, never from a user.
func Resolve(ctx context.Context, hc *http.Client, magnet, downloadURL string) (Source, error) {
	if magnet != "" {
		id, err := MagnetID(magnet)
		if err != nil {
			return Source{}, err
		}
		return Source{Magnet: magnet, Hash: id}, nil
	}
	if downloadURL == "" {
		return Source{}, fmt.Errorf("%w: release has no download link", ErrInvalid)
	}
	noFollow := *hc
	noFollow.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	next := downloadURL
	for range 5 {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, next, nil)
		if err != nil {
			return Source{}, fmt.Errorf("%w: download link", ErrInvalid)
		}
		resp, err := noFollow.Do(req)
		if err != nil {
			return Source{}, fmt.Errorf("torrent: fetch: %w", redact(err))
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, MaxTorrentBytes+1))
		_ = resp.Body.Close()
		switch {
		case resp.StatusCode >= 300 && resp.StatusCode < 400:
			loc, err := resp.Location()
			if err != nil {
				return Source{}, fmt.Errorf("%w: redirect without a location", ErrInvalid)
			}
			if loc.Scheme == "magnet" {
				id, err := MagnetID(loc.String())
				if err != nil {
					return Source{}, err
				}
				return Source{Magnet: loc.String(), Hash: id}, nil
			}
			if loc.Scheme != "http" && loc.Scheme != "https" {
				return Source{}, fmt.Errorf("%w: redirect to %s", ErrInvalid, loc.Scheme)
			}
			next = loc.String()
			continue
		case resp.StatusCode != http.StatusOK:
			return Source{}, fmt.Errorf("torrent: fetch: status %d", resp.StatusCode)
		case readErr != nil:
			return Source{}, fmt.Errorf("torrent: fetch: %w", redact(readErr))
		case len(body) > MaxTorrentBytes:
			return Source{}, fmt.Errorf("%w: .torrent file over %d bytes", ErrInvalid, MaxTorrentBytes)
		}
		id, err := ID(body)
		if err != nil {
			return Source{}, err
		}
		return Source{File: body, Hash: id}, nil
	}
	return Source{}, fmt.Errorf("%w: too many redirects", ErrInvalid)
}

// redact drops the URL from a url.Error: Prowlarr's download links carry its API key.
func redact(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%s: %w", ue.Op, ue.Err)
	}
	return err
}

// infoDict returns the raw bencoded bytes of the top-level "info" dictionary, which is what the
// info-hash is taken over.
func infoDict(b []byte) ([]byte, error) {
	if len(b) == 0 || b[0] != 'd' {
		return nil, fmt.Errorf("%w: not a bencoded dictionary", ErrInvalid)
	}
	i := 1
	for i < len(b) && b[i] != 'e' {
		key, end, err := bstring(b, i)
		if err != nil {
			return nil, err
		}
		valEnd, err := skip(b, end, 0)
		if err != nil {
			return nil, err
		}
		if key == "info" {
			if b[end] != 'd' {
				return nil, fmt.Errorf("%w: info is not a dictionary", ErrInvalid)
			}
			return b[end:valEnd], nil
		}
		i = valEnd
	}
	return nil, fmt.Errorf("%w: no info dictionary", ErrInvalid)
}

// v2Only reports whether an info dictionary is BitTorrent v2 without v1 compatibility: it has
// "meta version" 2 and no "pieces".
func v2Only(info []byte) bool {
	var version2, pieces bool
	i := 1
	for i < len(info) && info[i] != 'e' {
		key, end, err := bstring(info, i)
		if err != nil {
			return false
		}
		valEnd, err := skip(info, end, 0)
		if err != nil {
			return false
		}
		switch key {
		case "meta version":
			version2 = string(info[end:valEnd]) == "i2e"
		case "pieces":
			pieces = true
		}
		i = valEnd
	}
	return version2 && !pieces
}

// bstring reads a byte string at b[i:], returning it and the index after it.
func bstring(b []byte, i int) (string, int, error) {
	n := 0
	j := i
	for ; j < len(b) && b[j] >= '0' && b[j] <= '9'; j++ {
		n = n*10 + int(b[j]-'0')
		if n > len(b) {
			return "", 0, fmt.Errorf("%w: string length", ErrInvalid)
		}
	}
	if j == i || j >= len(b) || b[j] != ':' || j+1+n > len(b) {
		return "", 0, fmt.Errorf("%w: string", ErrInvalid)
	}
	return string(b[j+1 : j+1+n]), j + 1 + n, nil
}

// skip returns the index just after the bencoded value starting at b[i].
func skip(b []byte, i, depth int) (int, error) {
	if depth > 64 {
		return 0, fmt.Errorf("%w: nested too deeply", ErrInvalid)
	}
	if i >= len(b) {
		return 0, fmt.Errorf("%w: truncated", ErrInvalid)
	}
	switch c := b[i]; {
	case c == 'i':
		j := i + 1
		for j < len(b) && b[j] != 'e' {
			j++
		}
		if j >= len(b) || j == i+1 {
			return 0, fmt.Errorf("%w: integer", ErrInvalid)
		}
		return j + 1, nil
	case c == 'l' || c == 'd':
		j := i + 1
		for j < len(b) && b[j] != 'e' {
			if c == 'd' {
				_, end, err := bstring(b, j)
				if err != nil {
					return 0, err
				}
				j = end
			}
			end, err := skip(b, j, depth+1)
			if err != nil {
				return 0, err
			}
			j = end
		}
		if j >= len(b) {
			return 0, fmt.Errorf("%w: truncated", ErrInvalid)
		}
		return j + 1, nil
	case c >= '0' && c <= '9':
		_, end, err := bstring(b, i)
		return end, err
	default:
		return 0, fmt.Errorf("%w: unexpected byte %q", ErrInvalid, c)
	}
}
