package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/oldsj/rom-request/internal/qbittorrent"
	"github.com/oldsj/rom-request/internal/torrent"
)

// fakeProwlarr serves a search and the .torrent files its download links point at. Download
// links carry the API key, as Prowlarr's do, so a test can check it never reaches the browser.
type fakeProwlarr struct {
	*httptest.Server
	releases []map[string]any
	files    map[string]string // path -> .torrent body
}

const prowlarrKey = "prowlarr-secret-key"

func newFakeProwlarr(t *testing.T) *fakeProwlarr {
	t.Helper()
	f := &fakeProwlarr{files: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/search", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != prowlarrKey {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(f.releases)
	})
	mux.HandleFunc("GET /dl/{name}", func(w http.ResponseWriter, r *http.Request) {
		body, ok := f.files[r.PathValue("name")]
		if !ok || r.URL.Query().Get("apikey") != prowlarrKey {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, body)
	})
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

// addTorrentRelease adds a release whose download link serves a single-file torrent.
func (f *fakeProwlarr) addTorrentRelease(title, file string, size int) {
	name := fmt.Sprintf("t%d", len(f.files))
	info := fmt.Sprintf("d6:lengthi%de4:name%d:%s12:piece lengthi16384e6:pieces20:%se", size, len(file), file, strings.Repeat("x", 20))
	f.files[name] = "d4:info" + info + "e"
	f.releases = append(f.releases, map[string]any{
		"guid": name, "title": title, "indexer": "Homebrew Hub", "size": size, "seeders": 5,
		"protocol": "torrent", "infoUrl": "https://example.com/" + name,
		"downloadUrl": f.URL + "/dl/" + name + "?apikey=" + prowlarrKey,
		"categories":  []map[string]any{{"id": 1000, "name": "Console"}},
	})
}

func (f *fakeProwlarr) addMagnetRelease(title, hash string) {
	f.releases = append(f.releases, map[string]any{
		"guid": hash, "title": title, "indexer": "Homebrew Hub", "size": 4, "seeders": 50,
		"protocol": "torrent", "magnetUrl": "magnet:?xt=urn:btih:" + hash,
		"categories": []map[string]any{},
	})
	// A Usenet result is dropped.
	f.releases = append(f.releases, map[string]any{"title": "usenet " + title, "protocol": "usenet", "downloadUrl": "http://x"})
}

// fakeQBittorrent keeps torrents in memory and requires a signed-in session.
type fakeQBittorrent struct {
	*httptest.Server
	mu       sync.Mutex
	torrents map[string]*qbittorrent.Torrent
	files    map[string][]qbittorrent.File
	deleted  []string
}

func newFakeQBittorrent(t *testing.T) *fakeQBittorrent {
	t.Helper()
	f := &fakeQBittorrent{torrents: map[string]*qbittorrent.Torrent{}, files: map[string][]qbittorrent.File{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v2/auth/login", func(w http.ResponseWriter, r *http.Request) {
		if r.FormValue("username") != "admin" || r.FormValue("password") != "qb-pass" {
			_, _ = io.WriteString(w, "Fails.")
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "SID", Value: "s1", Path: "/"})
		_, _ = io.WriteString(w, "Ok.")
	})
	authed := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if c, err := r.Cookie("SID"); err != nil || c.Value != "s1" {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			h(w, r)
		}
	}
	mux.HandleFunc("POST /api/v2/torrents/add", authed(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var hash string
		if fh := r.MultipartForm.File["torrents"]; len(fh) == 1 {
			file, _ := fh[0].Open()
			b, _ := io.ReadAll(file)
			hash, _ = torrent.ID(b)
		} else {
			hash, _ = torrent.MagnetID(r.FormValue("urls"))
		}
		if hash == "" || f.torrents[hash] != nil || r.FormValue("autoTMM") != "false" || r.FormValue("tags") != "rom-request" {
			_, _ = io.WriteString(w, "Fails.")
			return
		}
		f.torrents[hash] = &qbittorrent.Torrent{Hash: hash, State: "downloading", SavePath: r.FormValue("savepath") + "/"}
		_, _ = io.WriteString(w, "Ok.")
	}))
	mux.HandleFunc("GET /api/v2/torrents/info", authed(func(w http.ResponseWriter, r *http.Request) {
		out := []qbittorrent.Torrent{}
		for h := range strings.SplitSeq(r.URL.Query().Get("hashes"), "|") {
			if t := f.torrents[h]; t != nil {
				out = append(out, *t)
			}
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	mux.HandleFunc("GET /api/v2/torrents/files", authed(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(f.files[r.URL.Query().Get("hash")])
	}))
	mux.HandleFunc("POST /api/v2/torrents/delete", authed(func(w http.ResponseWriter, r *http.Request) {
		h := r.FormValue("hashes")
		if r.FormValue("deleteFiles") != "true" {
			http.Error(w, "expected deleteFiles", http.StatusBadRequest)
			return
		}
		delete(f.torrents, h)
		f.deleted = append(f.deleted, h)
	}))
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

func (f *fakeQBittorrent) get(hash string) *qbittorrent.Torrent {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t := f.torrents[hash]; t != nil {
		c := *t
		return &c
	}
	return nil
}

func (f *fakeQBittorrent) set(hash string, fn func(*qbittorrent.Torrent), files []qbittorrent.File) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f.torrents[hash])
	if files != nil {
		f.files[hash] = files
	}
}
