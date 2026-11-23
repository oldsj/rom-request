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

// Source is what to hand qBittorrent: a .torrent file or a magnet link, and its ids.
type Source struct {
	File   []byte
	Magnet string
	// Hash identifies the torrent across requests: the v1 info-hash, or for a v2-only torrent
	// the v2 info-hash truncated to 20 bytes. Lower-case hex.
	Hash string
	// IDs are every id qBittorrent may know the torrent by. A hybrid (v1 + v2) torrent is its
	// v1 hash on libtorrent 1.2 builds and its truncated v2 hash on libtorrent 2.x builds
	// (which prefer v2), so both are listed. Hash is always among them.
	IDs []string
}

func newSource(v1, v2 string) Source {
	src := Source{Hash: v1}
	if v1 == "" {
		src.Hash = v2
	}
	for _, id := range []string{v1, v2} {
		if id != "" {
			src.IDs = append(src.IDs, id)
		}
	}
	return src
}

// Identify computes the ids of a .torrent file.
func Identify(file []byte) (Source, error) {
	info, err := infoDict(file)
	if err != nil {
		return Source{}, err
	}
	hasV1, hasV2 := versions(info)
	var v1, v2 string
	if hasV1 {
		sum := sha1.Sum(info) //nolint:gosec // see import
		v1 = hex.EncodeToString(sum[:])
	}
	if hasV2 {
		sum := sha256.Sum256(info)
		v2 = hex.EncodeToString(sum[:20])
	}
	src := newSource(v1, v2)
	src.File = file
	return src, nil
}

// ID is the identifying Hash of a .torrent file.
func ID(file []byte) (string, error) {
	src, err := Identify(file)
	return src.Hash, err
}

// IsMagnet reports whether link is a magnet link (and not, say, an http link that leads to one).
func IsMagnet(link string) bool {
	u, err := url.Parse(link)
	return err == nil && u.Scheme == "magnet"
}

// ParseMagnet reads a magnet link's ids from its xt parameters (btih in hex or base32, btmh).
func ParseMagnet(magnet string) (Source, error) {
	u, err := url.Parse(magnet)
	if err != nil || u.Scheme != "magnet" {
		return Source{}, fmt.Errorf("%w: not a magnet link", ErrInvalid)
	}
	var v1, v2 string
	for _, xt := range u.Query()["xt"] {
		switch {
		case strings.HasPrefix(xt, "urn:btih:") && v1 == "":
			h := strings.TrimPrefix(xt, "urn:btih:")
			switch len(h) {
			case 40:
				if _, err := hex.DecodeString(h); err == nil {
					v1 = strings.ToLower(h)
				}
			case 32:
				if b, err := base32.StdEncoding.DecodeString(strings.ToUpper(h)); err == nil {
					v1 = hex.EncodeToString(b)
				}
			}
		case strings.HasPrefix(xt, "urn:btmh:1220") && v2 == "":
			// A multihash: 0x12 (sha2-256), 0x20 (32 bytes), then the v2 info-hash.
			h := strings.TrimPrefix(xt, "urn:btmh:1220")
			if b, err := hex.DecodeString(h); err == nil && len(b) == 32 {
				v2 = hex.EncodeToString(b[:20])
			}
		}
	}
	if v1 == "" && v2 == "" {
		return Source{}, fmt.Errorf("%w: magnet link has no info-hash", ErrInvalid)
	}
	src := newSource(v1, v2)
	src.Magnet = magnet
	return src, nil
}

// MagnetID is the identifying Hash of a magnet link.
func MagnetID(magnet string) (string, error) {
	src, err := ParseMagnet(magnet)
	return src.Hash, err
}

// Resolve turns a release into a Source. A magnet link is used as it is. Otherwise the release
// is fetched: its download link, or its magnet link when that is really an http link
// (Prowlarr's search API rewrites both into links to its own download proxy, which answers
// with the .torrent file or a redirect to the magnet link). The links come from Prowlarr's
// results, which the server keeps; never from a user.
func Resolve(ctx context.Context, hc *http.Client, magnet, downloadURL string) (Source, error) {
	if IsMagnet(magnet) {
		return ParseMagnet(magnet)
	}
	link := downloadURL
	if link == "" {
		link = magnet
	}
	if link == "" {
		return Source{}, fmt.Errorf("%w: release has no download link", ErrInvalid)
	}
	noFollow := *hc
	noFollow.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	next := link
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
				return ParseMagnet(loc.String())
			}
			if loc.Scheme != "http" && loc.Scheme != "https" {
				return Source{}, fmt.Errorf("%w: redirect to %s", ErrInvalid, loc.Scheme)
			}
			next = loc.String()
			continue
		case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
			// The indexer no longer has the release; asking again won't help.
			return Source{}, fmt.Errorf("%w: release gone (status %d)", ErrInvalid, resp.StatusCode)
		case resp.StatusCode != http.StatusOK:
			return Source{}, fmt.Errorf("torrent: fetch: status %d", resp.StatusCode)
		case readErr != nil:
			return Source{}, fmt.Errorf("torrent: fetch: %w", redact(readErr))
		case len(body) > MaxTorrentBytes:
			return Source{}, fmt.Errorf("%w: .torrent file over %d bytes", ErrInvalid, MaxTorrentBytes)
		}
		return Identify(body)
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

// versions reports which BitTorrent versions an info dictionary carries: v1 has "pieces", v2
// has "meta version" 2. A hybrid has both.
func versions(info []byte) (v1, v2 bool) {
	i := 1
	for i < len(info) && info[i] != 'e' {
		key, end, err := bstring(info, i)
		if err != nil {
			return true, false
		}
		valEnd, err := skip(info, end, 0)
		if err != nil {
			return true, false
		}
		switch key {
		case "meta version":
			v2 = string(info[end:valEnd]) == "i2e"
		case "pieces":
			v1 = true
		}
		i = valEnd
	}
	if !v1 && !v2 {
		v1 = true // not a valid torrent either way; hash it as v1, as clients would
	}
	return v1, v2
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
