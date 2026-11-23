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
	var answer atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, answer.Load().(string))
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "", "", srv.Client())
	for body, want := range map[string]error{
		"Ok.":    nil,
		"Fails.": ErrRejected,
		`{"success_count":1,"failure_count":0,"pending_count":0}`: nil,
		`{"success_count":0,"failure_count":1,"pending_count":0}`: ErrRejected,
	} {
		answer.Store(body)
		if err := c.Add(t.Context(), Add{Magnet: "magnet:?xt=urn:btih:x", SavePath: "/d"}); !errors.Is(err, want) {
			t.Errorf("%s: %v", body, err)
		}
	}
}
