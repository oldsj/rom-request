package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
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
	magnets  map[string]string // path -> magnet link
	fetches  atomic.Int32      // download proxy hits
	expired  sync.Map          // path -> true: the indexer refuses the link (Prowlarr answers 500)
}

const prowlarrKey = "prowlarr-secret-key"

func newFakeProwlarr(t *testing.T) *fakeProwlarr {
	t.Helper()
	f := &fakeProwlarr{files: map[string]string{}, magnets: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/search", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != prowlarrKey {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(f.releases)
	})
	// Prowlarr's download proxy: the .torrent file, or a redirect to the magnet link. Its search
	// API gives both download and magnet links as links to it.
	mux.HandleFunc("GET /dl/{name}", func(w http.ResponseWriter, r *http.Request) {
		f.fetches.Add(1)
		name := r.PathValue("name")
		if _, gone := f.expired.Load(name); gone {
			http.Error(w, "Download selectors didn't match", http.StatusInternalServerError)
			return
		}
		if r.URL.Query().Get("apikey") != prowlarrKey {
			http.NotFound(w, r)
			return
		}
		if m, ok := f.magnets[name]; ok {
			w.Header().Set("Location", m)
			w.WriteHeader(http.StatusMovedPermanently)
			return
		}
		body, ok := f.files[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, body)
	})
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

// addTorrentRelease adds a release whose download link serves a single-file v1 torrent.
func (f *fakeProwlarr) addTorrentRelease(title, file string, size int) string {
	info := fmt.Sprintf("d6:lengthi%de4:name%d:%s12:piece lengthi16384e6:pieces20:%se", size, len(file), file, strings.Repeat("x", 20))
	return f.addFile(title, info)
}

// addHybridRelease adds a release whose download link serves a hybrid (v1 + v2) torrent.
func (f *fakeProwlarr) addHybridRelease(title, file string, size int) string {
	info := fmt.Sprintf("d6:lengthi%de12:meta versioni2e4:name%d:%s12:piece lengthi16384e6:pieces20:%se", size, len(file), file, strings.Repeat("y", 20))
	return f.addFile(title, info)
}

func (f *fakeProwlarr) addFile(title, info string) string {
	name := fmt.Sprintf("t%d", len(f.releases))
	f.files[name] = "d4:info" + info + "e"
	f.releases = append(f.releases, map[string]any{
		"guid": name, "title": title, "indexer": "Homebrew Hub", "size": 4, "seeders": 5,
		"protocol": "torrent", "infoUrl": "https://example.com/" + name,
		"downloadUrl": f.proxyLink(name),
		"categories":  []map[string]any{{"id": 1000, "name": "Console"}},
	})
	return name
}

func (f *fakeProwlarr) proxyLink(name string) string {
	return f.URL + "/dl/" + name + "?apikey=" + prowlarrKey + "&link=x"
}

// addMagnetRelease adds a magnet-only release. As in Prowlarr's search API, its magnetUrl is a
// link to the download proxy, which redirects to the magnet link.
func (f *fakeProwlarr) addMagnetRelease(title, hash string) {
	name := fmt.Sprintf("m%d", len(f.releases))
	f.magnets[name] = "magnet:?xt=urn:btih:" + hash + "&dn=" + url.QueryEscape(title)
	f.releases = append(f.releases, map[string]any{
		"guid": name, "title": title, "indexer": "Homebrew Hub", "size": 4, "seeders": 50,
		"protocol": "torrent", "magnetUrl": f.proxyLink(name),
		"categories": []map[string]any{},
	})
	// A Usenet result is dropped.
	f.releases = append(f.releases, map[string]any{"title": "usenet " + title, "protocol": "usenet", "downloadUrl": "http://x"})
}

// fakeQBittorrent keeps torrents in memory and requires a signed-in session. It behaves like
// qBittorrent 5.2 on libtorrent 2.x: a torrent's id is its truncated v2 hash when it has one,
// the hashes filter matches ids only, and a refused add is 409.
type fakeQBittorrent struct {
	*httptest.Server
	mu       sync.Mutex
	torrents map[string]*qbittorrent.Torrent
	files    map[string][]qbittorrent.File
	deleted  []string
	down     bool   // every call fails with 503
	reject   bool   // every add is refused
	onFiles  func() // called when a torrent's files are listed
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
		// qBittorrent 5.3: 204 and the QBT_SID_<port> cookie, no body.
		http.SetCookie(w, &http.Cookie{Name: "QBT_SID_8080", Value: "s1", Path: "/"})
		w.WriteHeader(http.StatusNoContent)
	})
	authed := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if c, err := r.Cookie("QBT_SID_8080"); err != nil || c.Value != "s1" {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.down {
				http.Error(w, "down", http.StatusServiceUnavailable)
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("POST /api/v2/torrents/add", authed(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var src torrent.Source
		var err error
		if fh := r.MultipartForm.File["torrents"]; len(fh) == 1 {
			file, _ := fh[0].Open()
			b, _ := io.ReadAll(file)
			src, err = torrent.Identify(b)
		} else {
			src, err = torrent.ParseMagnet(r.FormValue("urls"))
		}
		var id string
		if err == nil {
			id = src.IDs[len(src.IDs)-1] // libtorrent 2.x prefers v2
		}
		if f.reject || id == "" || f.torrents[id] != nil || r.FormValue("autoTMM") != "false" || r.FormValue("tags") != "rom-request" {
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, `{"success_count":0,"failure_count":1,"pending_count":0}`)
			return
		}
		t := &qbittorrent.Torrent{Hash: id, State: "downloading", SavePath: r.FormValue("savepath") + "/"}
		if len(src.IDs) == 2 {
			t.InfohashV1, t.InfohashV2 = src.IDs[0], src.IDs[1]+strings.Repeat("0", 24)
		} else {
			t.InfohashV1 = id
		}
		f.torrents[id] = t
		_, _ = io.WriteString(w, `{"success_count":1,"failure_count":0,"pending_count":0}`)
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
		if f.onFiles != nil {
			f.onFiles()
		}
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

func (f *fakeQBittorrent) setDown(down bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.down = down
}

// only returns the one torrent the fake has.
func (f *fakeQBittorrent) only(t *testing.T) qbittorrent.Torrent {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.torrents) != 1 {
		t.Fatalf("qBittorrent has %d torrents", len(f.torrents))
	}
	for _, tr := range f.torrents {
		return *tr
	}
	return qbittorrent.Torrent{}
}
