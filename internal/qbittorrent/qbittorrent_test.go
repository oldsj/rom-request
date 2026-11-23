package qbittorrent

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestSignsInAgainWhenSessionExpires(t *testing.T) {
	var logins, session atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v2/auth/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Referer") == "" {
			http.Error(w, "csrf", http.StatusUnauthorized)
			return
		}
		n := logins.Add(1)
		session.Store(n)
		http.SetCookie(w, &http.Cookie{Name: "SID", Value: string(rune('0' + n)), Path: "/"})
		_, _ = io.WriteString(w, "Ok.")
	})
	mux.HandleFunc("GET /api/v2/torrents/info", func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie("SID")
		if err != nil || c.Value != string(rune('0'+session.Load())) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		if r.URL.Query().Get("hashes") != "a|b" {
			http.Error(w, "bad query", http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, `[{"hash":"a","state":"stoppedUP","progress":1}]`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c, err := New(srv.URL, "u", "p", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	ts, err := c.Info(t.Context(), []string{"a", "b"})
	if err != nil || len(ts) != 1 || !ts[0].Complete() {
		t.Fatalf("Info = %v, %v", ts, err)
	}
	session.Store(99) // the server forgets the session
	if _, err := c.Info(t.Context(), []string{"a", "b"}); err != nil {
		t.Fatalf("after expiry: %v", err)
	}
	if logins.Load() != 2 {
		t.Errorf("logins = %d", logins.Load())
	}
}

func TestAddResponses(t *testing.T) {
	type answer struct {
		status int
		body   string
	}
	var current atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		a := current.Load().(answer)
		w.WriteHeader(a.status)
		_, _ = io.WriteString(w, a.body)
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "", "", srv.Client())
	for _, tc := range []struct {
		answer
		want error
	}{
		{answer{200, "Ok."}, nil},
		{answer{200, "Fails."}, ErrRejected},
		{answer{200, `{"success_count":1,"failure_count":0,"pending_count":0}`}, nil},
		{answer{200, `{"success_count":0,"failure_count":1,"pending_count":0}`}, ErrRejected},
		// qBittorrent 5.2+: a refused or duplicate add is 409.
		{answer{409, `{"success_count":0,"failure_count":1,"pending_count":0}`}, ErrRejected},
		{answer{415, "Torrent file is not valid"}, ErrRejected},
	} {
		current.Store(tc.answer)
		if err := c.Add(t.Context(), Add{Magnet: "magnet:?xt=urn:btih:x", SavePath: "/d"}); !errors.Is(err, tc.want) {
			t.Errorf("%d %s: %v", tc.status, tc.body, err)
		}
	}
	current.Store(answer{500, "boom"})
	if err := c.Add(t.Context(), Add{Magnet: "magnet:?xt=urn:btih:x", SavePath: "/d"}); err == nil || errors.Is(err, ErrRejected) {
		t.Errorf("500: %v", err)
	}
}

func TestMatches(t *testing.T) {
	v1 := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	v2 := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	// A hybrid on libtorrent 2.x: its id is the truncated v2 hash.
	tor := Torrent{Hash: v2[:40], InfohashV1: v1, InfohashV2: v2}
	if !tor.Matches([]string{v1}) || !tor.Matches([]string{"x", v2[:40]}) || tor.Matches([]string{"c"}) {
		t.Errorf("Matches wrong for %+v", tor)
	}
}

// qBittorrent up to 5.2 answers a sign-in with 200 "Ok.", 5.3 with 204; a refusal sets no cookie.
func TestSignInAnswers(t *testing.T) {
	for _, tc := range []struct {
		name   string
		answer func(w http.ResponseWriter)
		ok     bool
	}{
		{"5.2", func(w http.ResponseWriter) { setSID(w); _, _ = io.WriteString(w, "Ok.") }, true},
		{"5.3", func(w http.ResponseWriter) { setSID(w); w.WriteHeader(http.StatusNoContent) }, true},
		{"refused", func(w http.ResponseWriter) { _, _ = io.WriteString(w, "Fails.") }, false},
		{"refused 204", func(w http.ResponseWriter) { w.WriteHeader(http.StatusNoContent) }, false},
		{"banned", func(w http.ResponseWriter) { w.WriteHeader(http.StatusForbidden) }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { tc.answer(w) }))
			defer srv.Close()
			c, err := New(srv.URL, "u", "p", srv.Client())
			if err != nil {
				t.Fatal(err)
			}
			if err := c.login(t.Context()); (err == nil) != tc.ok {
				t.Errorf("login: %v", err)
			}
		})
	}
}

func setSID(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: "SID", Value: "s", Path: "/"})
}
