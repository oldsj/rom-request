package server

import (
	"testing"

	"github.com/oldsj/rom-request/internal/igdb"
)

// catalogRig is the request rig with the fake IGDB as its catalog.
func catalogRig(t *testing.T) *rig {
	t.Helper()
	r := newRig(t)
	f := newFakeIGDB(t)
	r.srv.catalog = igdb.New(igdb.Config{
		ClientID: "cid", ClientSecret: "csecret",
		TokenURL: f.ts.URL + "/oauth2/token", APIURL: f.ts.URL + "/v4", ImageURL: f.ts.URL + "/img",
	}, f.ts.Client())
	r.do("POST", "/api/auth/signup", creds)
	return r
}

func TestCatalogRequest(t *testing.T) {
	r := catalogRig(t)
	// The soundtrack and the other platform's release aren't the game.
	r.prowlarr.addTorrentRelease("Metroid Fusion OST GBA", "ost.mp3", 4)
	r.prowlarr.addTorrentRelease("Metroid Fusion (Europe) [GBA]", "Metroid Fusion (Europe).gba", 4)
	r.prowlarr.addTorrentRelease("Metroid Fusion (USA) [GBA]", "Metroid Fusion (USA).gba", 4)
	r.prowlarr.addTorrentRelease("Metroid Fusion (USA) PS1", "metroid.bin", 4)

	code, req, _ := r.do("POST", "/api/catalog/requests", map[string]any{"platform": "GBA", "gameId": 2})
	if code != 201 || req["status"] != "searching" || req["gameName"] != "Metroid Fusion" || req["gameId"] != float64(2) || req["title"] != "Metroid Fusion" {
		t.Fatalf("request: %d %v", code, req)
	}
	id := req["id"].(string)
	if code, body, _ := r.do("POST", "/api/catalog/requests", map[string]any{"platform": "GBA", "gameId": 2}); code != 409 || errCode(body) != "already_requested" {
		t.Errorf("again: %d %v", code, body)
	}
	for _, c := range []struct {
		platform string
		id, want int
	}{{"GBA", 99, 404}, {"N64", 2, 400}} { // not in IGDB; not a library folder
		if code, _, _ := r.do("POST", "/api/catalog/requests", map[string]any{"platform": c.platform, "gameId": c.id}); code != c.want {
			t.Errorf("%v: %d", c, code)
		}
	}

	r.tick()
	got := r.request(r.client, id)
	if got["status"] != "downloading" || got["title"] != "Metroid Fusion (USA) [GBA]" || got["gameName"] != "Metroid Fusion" {
		t.Fatalf("after search: %v", got)
	}
	tor := r.qb.only(t)
	r.finish(tor.Hash, id, "Metroid Fusion (USA).gba", "rom")
	r.tick()
	if got := r.request(r.client, id); got["status"] != "imported" || got["importedPath"] != "GBA/Metroid Fusion (USA).gba" {
		t.Fatalf("imported: %v", got)
	}

	code, list, _ := r.do("GET", "/api/catalog/requests?platform=GBA", nil)
	if items := list["items"].([]any); code != 200 || len(items) != 1 || items[0].(map[string]any)["id"] != id {
		t.Errorf("catalog requests: %d %v", code, list)
	}
	if _, list, _ := r.other().do("GET", "/api/catalog/requests?platform=GBA", nil); list["items"] != nil {
		t.Errorf("signed out: %v", list)
	}
}

func TestCatalogRequestNoRelease(t *testing.T) {
	r := catalogRig(t)
	r.prowlarr.addTorrentRelease("Pokemon Alpha Sapphire 3DS", "alpha.3ds", 4)
	code, req, _ := r.do("POST", "/api/catalog/requests", map[string]any{"platform": "GBA", "gameId": 3})
	if code != 201 {
		t.Fatalf("request: %d %v", code, req)
	}
	id := req["id"].(string)
	r.tick()
	got := r.request(r.client, id)
	if e, _ := got["error"].(map[string]any); got["status"] != "failed" || e["code"] != "no_release" {
		t.Fatalf("no release: %v", got)
	}
	// A failed request doesn't block a new one; retry searches again.
	if code, body, _ := r.do("POST", "/api/requests/"+id+"/retry", nil); code != 200 || body["status"] != "searching" {
		t.Fatalf("retry: %d %v", code, body)
	}
	r.prowlarr.addTorrentRelease("Pokemon FireRed Version (U) [GBA]", "Pokemon FireRed (USA).gba", 4)
	r.tick()
	if got := r.request(r.client, id); got["status"] != "downloading" {
		t.Fatalf("after retry: %v", got)
	}
	// Cancelling removes the torrent too.
	if code, _, _ := r.do("DELETE", "/api/requests/"+id, nil); code != 204 {
		t.Fatalf("delete: %d", code)
	}
}
