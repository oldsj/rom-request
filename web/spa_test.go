package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestHandler(t *testing.T) {
	dist := fstest.MapFS{
		"index.html":          {Data: []byte("<!doctype html><title>app</title>")},
		"assets/index-abc.js": {Data: []byte("console.log(1)")},
		"favicon.svg":         {Data: []byte("<svg/>")},
	}
	h := Handler(dist)
	cases := []struct {
		path, wantBody, wantCache string
		wantCode                  int
	}{
		{"/", "<title>app</title>", "no-cache", 200},
		{"/requests/123", "<title>app</title>", "no-cache", 200},
		{"/assets/index-abc.js", "console.log", "immutable", 200},
		{"/assets/missing.js", "", "", 404},
		{"/favicon.svg", "<svg/>", "no-cache", 200},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, c.path, nil))
		if rec.Code != c.wantCode {
			t.Errorf("%s: code %d, want %d", c.path, rec.Code, c.wantCode)
			continue
		}
		if !strings.Contains(rec.Body.String(), c.wantBody) {
			t.Errorf("%s: body %q", c.path, rec.Body)
		}
		if !strings.Contains(rec.Header().Get("Cache-Control"), c.wantCache) {
			t.Errorf("%s: Cache-Control %q", c.path, rec.Header().Get("Cache-Control"))
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", rec.Code)
	}
}
