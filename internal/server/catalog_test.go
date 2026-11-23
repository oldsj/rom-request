package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/oldsj/rom-request/internal/config"
	"github.com/oldsj/rom-request/internal/igdb"
)

// fakeIGDB is Twitch's token endpoint, IGDB's API and its image server. It knows the Game Boy
// Advance ("gba", id 24) and PlayStation ("ps", id 7).
type fakeIGDB struct {
	ts      *httptest.Server
	tokens  atomic.Int32
	queries atomic.Int32
	// revoke makes the next API call answer 401, as for an expired token.
	revoke atomic.Bool
	down   atomic.Bool
	last   atomic.Value // the last games query
}

var gbaGames = []string{
	`{"id":1,"name":"The Legend of Zelda: The Minish Cap","first_release_date":1099008000,"summary":"Link shrinks.","cover":{"id":9,"image_id":"co1abc"}}`,
	`{"id":2,"name":"Metroid Fusion","first_release_date":1037577600}`,
	`{"id":3,"name":"Pokémon FireRed Version","cover":{"id":10,"image_id":"co2def"}}`,
}

func newFakeIGDB(t *testing.T) *fakeIGDB {
	t.Helper()
	f := &fakeIGDB{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("client_id") != "cid" || r.Form.Get("client_secret") != "csecret" || r.Form.Get("grant_type") != "client_credentials" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		n := f.tokens.Add(1)
		writeJSON(w, map[string]any{"access_token": "tok" + strconv.Itoa(int(n)), "expires_in": 5000000, "token_type": "bearer"})
	})
	mux.HandleFunc("POST /v4/{endpoint}", func(w http.ResponseWriter, r *http.Request) {
		f.queries.Add(1)
		if f.down.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if f.revoke.CompareAndSwap(true, false) || r.Header.Get("Client-ID") != "cid" ||
			r.Header.Get("Authorization") != "Bearer tok"+strconv.Itoa(int(f.tokens.Load())) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		b, _ := io.ReadAll(r.Body)
		q := string(b)
		w.Header().Set("Content-Type", "application/json")
		switch r.PathValue("endpoint") {
		case "platforms":
			var out []string
			if strings.Contains(q, `"gba"`) {
				out = append(out, `{"id":24,"name":"Game Boy Advance","slug":"gba"}`)
			}
			if strings.Contains(q, `"ps"`) {
				out = append(out, `{"id":7,"name":"PlayStation","slug":"ps"}`)
			}
			_, _ = io.WriteString(w, "["+strings.Join(out, ",")+"]")
		case "games":
			// One game by id: a catalog request's lookup.
			if m := regexp.MustCompile(`where id = (\d+);`).FindStringSubmatch(q); m != nil {
				for _, g := range gbaGames {
					if strings.HasPrefix(g, `{"id":`+m[1]+`,`) {
						_, _ = io.WriteString(w, "["+strings.Replace(g, "{", `{"platforms":[24],`, 1)+"]")
						return
					}
				}
				_, _ = io.WriteString(w, "[]")
				return
			}
			f.last.Store(q)
			if !strings.Contains(q, "where platforms = (24)") || !strings.Contains(q, "offset 0;") {
				_, _ = io.WriteString(w, "[]")
				return
			}
			games := gbaGames
			if m := regexp.MustCompile(`search "([^"]*)"`).FindStringSubmatch(q); m != nil {
				games = nil
				for _, g := range gbaGames {
					if strings.Contains(strings.ToLower(g), strings.ToLower(m[1])) {
						games = append(games, g)
					}
				}
			}
			_, _ = io.WriteString(w, "["+strings.Join(games, ",")+"]")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	mux.HandleFunc("GET /img/t_cover_big/{name}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("name") != "co1abc.jpg" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = io.WriteString(w, "\xff\xd8\xff jpeg bytes")
	})
	f.ts = httptest.NewServer(mux)
	t.Cleanup(f.ts.Close)
	return f
}

func newCatalogServer(t *testing.T) (*client, *fakeIGDB, string) {
	t.Helper()
	f := newFakeIGDB(t)
	lib := t.TempDir()
	for _, p := range []string{"gba/Legend of Zelda, The - The Minish Cap (USA).gba", "gba/.hidden", "psx/Crash Bandicoot (USA).chd", "atari7800/Joust.a78"} {
		if err := os.MkdirAll(filepath.Join(lib, filepath.Dir(p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(lib, p), []byte("rom"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	c := newTestServer(t, func(cfg *config.Config) {
		cfg.LibraryDir = lib
		cfg.IGDBClientID, cfg.IGDBClientSecret = "cid", "csecret"
	})
	c.srv.catalog = igdb.New(igdb.Config{
		ClientID: "cid", ClientSecret: "csecret",
		TokenURL: f.ts.URL + "/oauth2/token", APIURL: f.ts.URL + "/v4", ImageURL: f.ts.URL + "/img",
	}, f.ts.Client())
	c.do("POST", "/api/auth/signup", creds)
	return c, f, lib
}

func TestCatalogBrowse(t *testing.T) {
	c, f, lib := newCatalogServer(t)

	if code, st, _ := c.do("GET", "/api/status", nil); code != 200 || st["catalog"] != true {
		t.Fatalf("status: %d %v", code, st)
	}

	code, body, _ := c.do("GET", "/api/catalog/platforms", nil)
	if code != 200 {
		t.Fatalf("platforms: %d %v", code, body)
	}
	got := map[string]any{}
	for _, it := range body["items"].([]any) {
		p := it.(map[string]any)
		got[p["name"].(string)] = p["catalogName"]
	}
	// psx is RomM's folder for IGDB's "ps"; IGDB doesn't know atari7800 in this fake.
	if got["gba"] != "Game Boy Advance" || got["psx"] != "PlayStation" || got["atari7800"] != nil || len(got) != 3 {
		t.Errorf("platforms: %v", got)
	}

	code, body, _ = c.do("GET", "/api/catalog/games?platform=gba", nil)
	if code != 200 {
		t.Fatalf("games: %d %v", code, body)
	}
	items := body["items"].([]any)
	if len(items) != 3 || body["nextOffset"] != nil {
		t.Fatalf("games: %v", body)
	}
	zelda, metroid := items[0].(map[string]any), items[1].(map[string]any)
	if zelda["inLibrary"] != true || zelda["year"] != float64(2004) || zelda["coverId"] != "co1abc" || zelda["summary"] != "Link shrinks." {
		t.Errorf("zelda: %v", zelda)
	}
	if metroid["inLibrary"] != false || metroid["coverId"] != nil {
		t.Errorf("metroid: %v", metroid)
	}
	if q, _ := f.last.Load().(string); !strings.Contains(q, "sort total_rating_count desc") || !strings.Contains(q, "game_type = (0,8,9,10,11)") {
		t.Errorf("query: %s", q)
	}

	// The library is read on every page: a newly imported game shows at once.
	if err := os.WriteFile(filepath.Join(lib, "gba", "Metroid Fusion (Europe).gba"), []byte("rom"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, body, _ = c.do("GET", "/api/catalog/games?platform=gba&sort=name", nil)
	if m := body["items"].([]any)[1].(map[string]any); m["inLibrary"] != true {
		t.Errorf("metroid after import: %v", m)
	}
	if q, _ := f.last.Load().(string); !strings.Contains(q, "sort name asc") {
		t.Errorf("query: %s", q)
	}

	// A search, with quotes that would otherwise end IGDB's string.
	c.do("GET", `/api/catalog/games?platform=gba&q=pok%22%3B%20fields%20*`, nil)
	if q, _ := f.last.Load().(string); !strings.HasPrefix(q, `search "pok ; fields *"; fields `) {
		t.Errorf("search query: %s", q)
	}
	_, body, _ = c.do("GET", "/api/catalog/games?platform=gba&q=fire", nil)
	if items := body["items"].([]any); len(items) != 1 || items[0].(map[string]any)["name"] != "Pokémon FireRed Version" {
		t.Errorf("search: %v", body)
	}
	if q, _ := f.last.Load().(string); strings.Contains(q, "sort ") {
		t.Errorf("search sorted: %s", q)
	}

	for path, want := range map[string]string{
		"/api/catalog/games?platform=atari7800": "not_in_catalog",
		"/api/catalog/games?platform=n64":       "unknown_platform",
		"/api/catalog/games?platform=..":        "unknown_platform",
	} {
		if code, body, _ := c.do("GET", path, nil); code != 404 || errCode(body) != want {
			t.Errorf("%s: %d %v", path, code, body)
		}
	}

	// One token for all of it; platforms are cached.
	if n := f.tokens.Load(); n != 1 {
		t.Errorf("tokens fetched: %d", n)
	}
}

func TestCatalogTokenRenewal(t *testing.T) {
	c, f, _ := newCatalogServer(t)
	c.do("GET", "/api/catalog/platforms", nil)
	f.revoke.Store(true)
	if code, body, _ := c.do("GET", "/api/catalog/games?platform=gba", nil); code != 200 {
		t.Fatalf("after revoke: %d %v", code, body)
	}
	if n := f.tokens.Load(); n != 2 {
		t.Errorf("tokens fetched: %d", n)
	}
	f.down.Store(true)
	if code, body, _ := c.do("GET", "/api/catalog/games?platform=gba", nil); code != 502 || errCode(body) != "catalog_unavailable" {
		t.Errorf("down: %d %v", code, body)
	}
}

func TestCatalogCover(t *testing.T) {
	c, _, _ := newCatalogServer(t)
	get := func(cl *client, path string) (int, http.Header, string) {
		req, _ := http.NewRequestWithContext(t.Context(), "GET", cl.base+path, nil)
		resp, err := cl.http.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, resp.Header, string(b)
	}
	code, hdr, body := get(c, "/api/catalog/covers/co1abc")
	if code != 200 || hdr.Get("Content-Type") != "image/jpeg" || !strings.HasPrefix(body, "\xff\xd8\xff") ||
		hdr.Get("Cache-Control") != "private, max-age=2592000, immutable" {
		t.Errorf("cover: %d %v %q", code, hdr, body)
	}
	if code, _, _ := get(c, "/api/catalog/covers/nope"); code != 502 {
		t.Errorf("missing cover: %d", code)
	}
	if code, _, _ := get(c, "/api/catalog/covers/..%2Fsecret"); code != 400 && code != 404 {
		t.Errorf("bad id: %d", code)
	}
	if code, _, _ := get(c.other(), "/api/catalog/covers/co1abc"); code != 401 {
		t.Errorf("signed out: %d", code)
	}
}

func TestCatalogOff(t *testing.T) {
	c := newTestServer(t, nil)
	c.do("POST", "/api/auth/signup", creds)
	if _, st, _ := c.do("GET", "/api/status", nil); st["catalog"] != false {
		t.Errorf("status: %v", st)
	}
	if code, body, _ := c.do("GET", "/api/catalog/platforms", nil); code != 503 || errCode(body) != "not_configured" {
		t.Errorf("platforms: %d %v", code, body)
	}
}
