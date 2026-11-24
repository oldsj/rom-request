package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oldsj/rom-request/internal/config"
	"github.com/oldsj/rom-request/internal/qbittorrent"
)

const savePath = "/media/downloads/rom-request"

type rig struct {
	*client
	prowlarr       *fakeProwlarr
	qb             *fakeQBittorrent
	downloads, lib string
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{prowlarr: newFakeProwlarr(t), qb: newFakeQBittorrent(t), downloads: t.TempDir(), lib: t.TempDir()}
	for _, d := range []string{"GBA", "PS1", "bios"} {
		if err := os.Mkdir(filepath.Join(r.lib, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	r.client = newTestServer(t, func(cfg *config.Config) {
		cfg.ProwlarrURL, cfg.ProwlarrAPIKey = r.prowlarr.URL, prowlarrKey
		cfg.QBittorrentURL, cfg.QBittorrentUsername, cfg.QBittorrentPassword = r.qb.URL, "admin", "qb-pass"
		cfg.QBittorrentSavePath, cfg.DownloadDir, cfg.LibraryDir = savePath, r.downloads, r.lib
		cfg.ProwlarrBlockedIndexers = []string{"blueroms"}
	})
	return r
}

func (r *rig) tick() {
	r.t.Helper()
	if err := r.srv.Worker().Tick(r.t.Context()); err != nil {
		r.t.Fatal(err)
	}
}

// search returns the result ids by title.
func (r *rig) search(c *client) map[string]string {
	r.t.Helper()
	code, body, _ := c.do("GET", "/api/search?q=homebrew", nil)
	if code != 200 {
		r.t.Fatalf("search: %d %v", code, body)
	}
	ids := map[string]string{}
	for _, it := range body["items"].([]any) {
		m := it.(map[string]any)
		ids[m["title"].(string)] = m["id"].(string)
	}
	return ids
}

// finish completes a torrent in the fake qBittorrent and writes its file into the download folder.
func (r *rig) finish(hash, requestID, name, content string) {
	r.t.Helper()
	p := filepath.Join(r.downloads, requestID, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
	r.qb.set(hash, func(t *qbittorrent.Torrent) { t.Progress, t.State = 1, "stalledUP" },
		[]qbittorrent.File{{Name: name, Size: int64(len(content)), Progress: 1, Priority: 1}})
}

func (r *rig) request(c *client, id string) map[string]any {
	r.t.Helper()
	_, list, _ := c.do("GET", "/api/requests", nil)
	for _, it := range list["items"].([]any) {
		if m := it.(map[string]any); m["id"] == id {
			return m
		}
	}
	r.t.Fatalf("request %s not listed", id)
	return nil
}

func TestRequestLifecycle(t *testing.T) {
	r := newRig(t)
	r.prowlarr.addTorrentRelease("Homebrew Quest", "Homebrew Quest.gba", 9)
	magnetHash := "c12fe1c06bba254a9dc9f519b335aa7c1367a88a"
	r.prowlarr.addMagnetRelease("Homebrew Quest (v2)", magnetHash)
	r.do("POST", "/api/auth/signup", creds)

	if code, st, _ := r.do("GET", "/api/status", nil); code != 200 || st["prowlarr"] != true || st["qbittorrent"] != true || st["library"] != true {
		t.Fatalf("status: %d %v", code, st)
	}
	if code, p, _ := r.do("GET", "/api/platforms", nil); code != 200 || len(p["items"].([]any)) != 2 {
		t.Fatalf("platforms: %d %v", code, p)
	}

	// The browser never sees download links or the Prowlarr key.
	req, _ := http.NewRequestWithContext(t.Context(), "GET", r.base+"/api/search?q=homebrew", nil)
	resp, err := r.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if strings.Contains(string(raw), prowlarrKey) || strings.Contains(string(raw), "magnet:") || strings.Contains(string(raw), "usenet") {
		t.Fatalf("search leaks server-side data: %s", raw)
	}
	ids := r.search(r.client)
	if len(ids) != 2 {
		t.Fatalf("results: %v", ids)
	}

	// Validation: unknown platform, excluded folder, expired or foreign result.
	if code, body, _ := r.do("POST", "/api/requests", map[string]string{"releaseId": ids["Homebrew Quest"], "platform": "N64"}); code != 400 || errCode(body) != "unknown_platform" {
		t.Errorf("unknown platform: %d %v", code, body)
	}
	if code, _, _ := r.do("POST", "/api/requests", map[string]string{"releaseId": ids["Homebrew Quest"], "platform": "bios"}); code != 400 {
		t.Errorf("bios accepted: %d", code)
	}
	if code, _, _ := r.do("POST", "/api/requests", map[string]any{"releaseId": ids["Homebrew Quest"], "platform": "GBA", "sourceUrl": "javascript:alert(1)"}); code != 400 {
		t.Errorf("javascript: source URL accepted: %d", code)
	}
	eve := r.other()
	eve.do("POST", "/api/auth/signup", map[string]string{"email": "eve@example.com", "password": "another long password"})
	if code, body, _ := eve.do("POST", "/api/requests", map[string]string{"releaseId": ids["Homebrew Quest"], "platform": "GBA"}); code != 404 || errCode(body) != "release_expired" {
		t.Errorf("another user's result: %d %v", code, body)
	}

	// Request the .torrent release: added to qBittorrent in its own folder.
	code, created, _ := r.do("POST", "/api/requests", map[string]any{
		"releaseId": ids["Homebrew Quest"], "platform": "GBA", "license": "CC BY 4.0", "sourceUrl": "https://example.com/homebrew-quest",
	})
	if code != 201 || created["status"] != "downloading" || created["license"] != "CC BY 4.0" {
		t.Fatalf("create: %d %v", code, created)
	}
	id := created["id"].(string)
	var hash string
	r.qb.mu.Lock()
	for h, tr := range r.qb.torrents {
		if tr.SavePath == savePath+"/"+id+"/" {
			hash = h
		}
	}
	r.qb.mu.Unlock()
	if hash == "" {
		t.Fatalf("torrent not added under %s/%s", savePath, id)
	}
	if code, body, _ := r.do("POST", "/api/requests", map[string]string{"releaseId": ids["Homebrew Quest"], "platform": "PS1"}); code != 409 || errCode(body) != "already_requested" {
		t.Errorf("duplicate: %d %v", code, body)
	}

	// Another user can't see, retry or cancel it.
	if _, l, _ := eve.do("GET", "/api/requests", nil); len(l["items"].([]any)) != 0 {
		t.Errorf("eve sees requests: %v", l)
	}
	if code, _, _ := eve.do("DELETE", "/api/requests/"+id, nil); code != 404 {
		t.Errorf("eve's delete: %d", code)
	}
	if code, _, _ := eve.do("POST", "/api/requests/"+id+"/retry", nil); code != 404 {
		t.Errorf("eve's retry: %d", code)
	}

	// Progress, then completion and import.
	r.qb.set(hash, func(t *qbittorrent.Torrent) { t.Progress = 0.5 }, nil)
	r.tick()
	if got := r.request(r.client, id); got["progress"].(float64) != 0.5 || got["status"] != "downloading" {
		t.Errorf("progress: %v", got)
	}
	r.finish(hash, id, "Homebrew Quest.gba", "rom bytes")
	r.tick()
	got := r.request(r.client, id)
	sum := sha256.Sum256([]byte("rom bytes"))
	if got["status"] != "imported" || got["importedPath"] != "GBA/Homebrew Quest.gba" || got["importedSha256"] != hex.EncodeToString(sum[:]) {
		t.Fatalf("imported: %v", got)
	}
	if b, err := os.ReadFile(filepath.Join(r.lib, "GBA", "Homebrew Quest.gba")); err != nil || string(b) != "rom bytes" {
		t.Fatalf("library file: %q %v", b, err)
	}
	if code, body, _ := r.do("POST", "/api/requests/"+id+"/retry", nil); code != 409 || errCode(body) != "not_failed" {
		t.Errorf("retry imported: %d %v", code, body)
	}

	// The magnet release has the same file name: a conflict, never an overwrite.
	code, second, _ := r.do("POST", "/api/requests", map[string]string{"releaseId": ids["Homebrew Quest (v2)"], "platform": "GBA"})
	if code != 201 || second["status"] != "downloading" {
		t.Fatalf("magnet request: %d %v", code, second)
	}
	id2 := second["id"].(string)
	r.finish(magnetHash, id2, "Homebrew Quest.gba", "new!")
	r.tick()
	got = r.request(r.client, id2)
	if errObj, _ := got["error"].(map[string]any); got["status"] != "failed" || errObj["code"] != "library_conflict" {
		t.Fatalf("conflict: %v", got)
	}
	if b, _ := os.ReadFile(filepath.Join(r.lib, "GBA", "Homebrew Quest.gba")); string(b) != "rom bytes" {
		t.Fatal("existing ROM overwritten")
	}

	// Settle it and retry: picks up the torrent qBittorrent already has.
	if err := os.Rename(filepath.Join(r.lib, "GBA", "Homebrew Quest.gba"), filepath.Join(r.lib, "GBA", "Homebrew Quest (v1).gba")); err != nil {
		t.Fatal(err)
	}
	if code, body, _ := r.do("POST", "/api/requests/"+id2+"/retry", nil); code != 200 || body["status"] != "downloading" || body["error"] != nil {
		t.Fatalf("retry: %d %v", code, body)
	}
	r.tick()
	if got := r.request(r.client, id2); got["status"] != "imported" {
		t.Fatalf("after retry: %v", got)
	}

	// Removing an imported request leaves the library and the seeding torrent alone.
	if code, _, _ := r.do("DELETE", "/api/requests/"+id, nil); code != 204 {
		t.Errorf("delete imported: %d", code)
	}
	if _, err := os.Stat(filepath.Join(r.lib, "GBA", "Homebrew Quest (v1).gba")); err != nil {
		t.Error("library file removed")
	}
	if r.qb.get(hash) == nil {
		t.Error("imported torrent removed from qBittorrent")
	}
}

func TestCancelAndRemovedTorrent(t *testing.T) {
	r := newRig(t)
	r.prowlarr.addTorrentRelease("Homebrew Racer", "racer.gba", 4)
	r.prowlarr.addTorrentRelease("Homebrew Puzzle", "puzzle.gba", 4)
	r.do("POST", "/api/auth/signup", creds)
	ids := r.search(r.client)

	_, a, _ := r.do("POST", "/api/requests", map[string]string{"releaseId": ids["Homebrew Racer"], "platform": "GBA"})
	_, b, _ := r.do("POST", "/api/requests", map[string]string{"releaseId": ids["Homebrew Puzzle"], "platform": "GBA"})
	if a["status"] != "downloading" || b["status"] != "downloading" {
		t.Fatalf("create: %v %v", a, b)
	}

	// Cancelling a download removes its torrent and data from qBittorrent.
	if code, _, _ := r.do("DELETE", "/api/requests/"+a["id"].(string), nil); code != 204 {
		t.Fatalf("cancel: %d", code)
	}
	if len(r.qb.deleted) != 1 {
		t.Errorf("qBittorrent deletes: %v", r.qb.deleted)
	}

	// A torrent removed in qBittorrent fails the request with a way forward; retry re-adds it.
	// Not at once: qBittorrent adds torrents in the background.
	r.qb.mu.Lock()
	clear(r.qb.torrents)
	r.qb.mu.Unlock()
	r.tick()
	if got := r.request(r.client, b["id"].(string)); got["status"] != "downloading" {
		t.Fatalf("failed within the grace period: %v", got)
	}
	r.srv.Worker().SetClock(func() time.Time { return time.Now().Add(time.Hour) })
	r.tick()
	got := r.request(r.client, b["id"].(string))
	if e, _ := got["error"].(map[string]any); got["status"] != "failed" || e["code"] != "torrent_removed" {
		t.Fatalf("removed: %v", got)
	}
	if code, body, _ := r.do("POST", "/api/requests/"+b["id"].(string)+"/retry", nil); code != 200 || body["status"] != "downloading" {
		t.Fatalf("retry: %d %v", code, body)
	}
	r.qb.mu.Lock()
	n := len(r.qb.torrents)
	r.qb.mu.Unlock()
	if n != 1 {
		t.Errorf("torrent not re-added: %d", n)
	}
}

// create requests a search result for GBA and returns the new request.
func (r *rig) create(c *client, releaseID string) map[string]any {
	r.t.Helper()
	code, body, _ := c.do("POST", "/api/requests", map[string]string{"releaseId": releaseID, "platform": "GBA"})
	if code != 201 {
		r.t.Fatalf("create: %d %v", code, body)
	}
	return body
}

func TestHybridTorrent(t *testing.T) {
	r := newRig(t)
	r.prowlarr.addHybridRelease("Homebrew Hybrid", "hybrid.gba", 4)
	r.do("POST", "/api/auth/signup", creds)
	req := r.create(r.client, r.search(r.client)["Homebrew Hybrid"])
	if req["status"] != "downloading" {
		t.Fatalf("create: %v", req)
	}
	// qBittorrent (libtorrent 2.x) knows it by its v2 id, not the v1 hash.
	tor := r.qb.only(t)
	if tor.Hash == tor.InfohashV1 {
		t.Fatalf("fake used the v1 hash as the id: %+v", tor)
	}
	r.finish(tor.Hash, req["id"].(string), "hybrid.gba", "abcd")
	r.tick()
	if got := r.request(r.client, req["id"].(string)); got["status"] != "imported" {
		t.Fatalf("hybrid: %v", got)
	}
}

func TestQBittorrentRefuses(t *testing.T) {
	r := newRig(t)
	r.prowlarr.addTorrentRelease("Homebrew Racer", "racer.gba", 4)
	r.do("POST", "/api/auth/signup", creds)
	r.qb.mu.Lock()
	r.qb.reject = true
	r.qb.mu.Unlock()
	req := r.create(r.client, r.search(r.client)["Homebrew Racer"])
	if e, _ := req["error"].(map[string]any); req["status"] != "failed" || e["code"] != "qbittorrent_rejected" {
		t.Fatalf("refused add: %v", req)
	}
}

func TestQBittorrentDown(t *testing.T) {
	r := newRig(t)
	r.prowlarr.addTorrentRelease("Homebrew Racer", "racer.gba", 4)
	r.do("POST", "/api/auth/signup", creds)
	ids := r.search(r.client)

	// Down while requesting: the request waits in the queue.
	r.qb.setDown(true)
	req := r.create(r.client, ids["Homebrew Racer"])
	id := req["id"].(string)
	if req["status"] != "queued" {
		t.Fatalf("create while down: %v", req)
	}
	fetched := r.prowlarr.fetches.Load()

	// The worker backs off between attempts and gives up in the end, without fetching the
	// release from the indexer again each time.
	w := r.srv.Worker()
	ahead := time.Duration(0)
	for i := range 10 {
		ahead += 2 * time.Hour
		w.SetClock(func() time.Time { return time.Now().Add(ahead) })
		_ = w.Tick(t.Context())
		got := r.request(r.client, id)
		if want := map[bool]string{true: "failed", false: "queued"}[i == 9]; got["status"] != want {
			t.Fatalf("attempt %d: %v", i+1, got)
		}
	}
	if e, _ := r.request(r.client, id)["error"].(map[string]any); e["code"] != "submit_failed" {
		t.Errorf("gave up with %v", e)
	}
	if n := r.prowlarr.fetches.Load(); n != fetched {
		t.Errorf("release fetched %d more times while qBittorrent was down", n-fetched)
	}
	w.SetClock(time.Now)

	// Back up: retry adds it.
	r.qb.setDown(false)
	if code, body, _ := r.do("POST", "/api/requests/"+id+"/retry", nil); code != 200 || body["status"] != "downloading" {
		t.Fatalf("retry: %d %v", code, body)
	}

	// Removing while qBittorrent is down works; the torrent goes once it's back.
	r.qb.setDown(true)
	if code, _, _ := r.do("DELETE", "/api/requests/"+id, nil); code != 204 {
		t.Fatalf("delete while down: %d", code)
	}
	if _, l, _ := r.do("GET", "/api/requests", nil); len(l["items"].([]any)) != 0 {
		t.Errorf("removed request still listed: %v", l)
	}
	_ = w.Tick(t.Context())
	r.qb.setDown(false)
	r.qb.only(t)
	r.tick()
	r.qb.mu.Lock()
	left, deleted := len(r.qb.torrents), len(r.qb.deleted)
	r.qb.mu.Unlock()
	if left != 0 || deleted != 1 {
		t.Errorf("after qBittorrent came back: %d torrents, %d deletes", left, deleted)
	}
}

func TestImportInterruptedByShutdown(t *testing.T) {
	r := newRig(t)
	r.prowlarr.addTorrentRelease("Homebrew Racer", "racer.gba", 4)
	r.do("POST", "/api/auth/signup", creds)
	req := r.create(r.client, r.search(r.client)["Homebrew Racer"])
	id := req["id"].(string)
	tor := r.qb.only(t)
	r.finish(tor.Hash, id, "racer.gba", "rom!")

	// Shutdown begins during the import: it's handed back, not left importing.
	ctx, cancel := context.WithCancel(t.Context())
	r.qb.mu.Lock()
	r.qb.onFiles = cancel
	r.qb.mu.Unlock()
	_ = r.srv.Worker().Tick(ctx)
	if got := r.request(r.client, id); got["status"] != "downloading" {
		t.Fatalf("after shutdown: %v", got)
	}
	r.qb.mu.Lock()
	r.qb.onFiles = nil
	r.qb.mu.Unlock()
	r.tick()
	if got := r.request(r.client, id); got["status"] != "imported" {
		t.Fatalf("next round: %v", got)
	}
}
