package server

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/oldsj/rom-request/internal/config"
	"github.com/oldsj/rom-request/internal/testdb"
)

// These are integration tests: the real handler stack against a real, migrated PostgreSQL.

type client struct {
	t    *testing.T
	base string
	http *http.Client
	srv  *Server
}

func newTestServer(t *testing.T, mutate func(*config.Config)) *client {
	t.Helper()
	pool := testdb.New(t)
	cfg := config.Config{AllowSignup: true, SessionTTL: time.Hour, AuthRatePerMinute: 100, LibraryExclude: []string{"bios"}}
	if mutate != nil {
		mutate(&cfg)
	}
	srv, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), pool)
	if err != nil {
		t.Fatal(err)
	}
	h, err := srv.Handler()
	if err != nil {
		t.Fatal(err)
	}
	// TLS, because the session cookie is Secure.
	ts := httptest.NewTLSServer(h)
	t.Cleanup(ts.Close)
	hc := ts.Client()
	hc.Jar, _ = cookiejar.New(nil)
	return &client{t: t, base: ts.URL, http: hc, srv: srv}
}

// other is a second, signed-out browser on the same server.
func (c *client) other() *client {
	o := &client{t: c.t, base: c.base, http: &http.Client{Transport: c.http.Transport}, srv: c.srv}
	o.http.Jar, _ = cookiejar.New(nil)
	return o
}

func (c *client) do(method, path string, body any) (int, map[string]any, http.Header) {
	c.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequestWithContext(c.t.Context(), method, c.base+path, r)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out, resp.Header
}

func errCode(body map[string]any) string {
	e, _ := body["error"].(map[string]any)
	s, _ := e["code"].(string)
	return s
}

var creds = map[string]string{"email": "Ada@Example.com", "password": "a long enough password"}

func TestAuthFlow(t *testing.T) {
	c := newTestServer(t, nil)

	if code, body, _ := c.do("GET", "/api/requests", nil); code != 401 || errCode(body) != "unauthorized" {
		t.Fatalf("requests before sign-in: %d %v", code, body)
	}

	code, body, hdr := c.do("POST", "/api/auth/signup", creds)
	if code != 201 || body["email"] != "ada@example.com" {
		t.Fatalf("signup: %d %v", code, body)
	}
	setCookie := hdr.Get("Set-Cookie")
	for _, want := range []string{"__Host-session=", "HttpOnly", "Secure", "SameSite=Lax", "Path=/"} {
		if !strings.Contains(setCookie, want) {
			t.Errorf("Set-Cookie %q lacks %s", setCookie, want)
		}
	}
	if code, _, _ := c.do("POST", "/api/auth/signup", creds); code != 409 {
		t.Errorf("duplicate signup: %d", code)
	}

	if code, list, _ := c.do("GET", "/api/requests", nil); code != 200 || len(list["items"].([]any)) != 0 {
		t.Fatalf("list: %d %v", code, list)
	}

	if code, _, _ := c.do("POST", "/api/auth/logout", nil); code != 204 {
		t.Errorf("logout: %d", code)
	}
	if code, _, _ := c.do("GET", "/api/auth/me", nil); code != 401 {
		t.Errorf("me after logout: %d", code)
	}

	if code, _, _ := c.do("POST", "/api/auth/login", map[string]string{"email": "ada@example.com", "password": "wrong password!!"}); code != 401 {
		t.Errorf("wrong password: %d", code)
	}
	if code, me, _ := c.do("POST", "/api/auth/login", creds); code != 200 || me["email"] != "ada@example.com" {
		t.Errorf("login: %d %v", code, me)
	}
	if code, _, _ := c.do("GET", "/api/auth/me", nil); code != 200 {
		t.Errorf("me after login: %d", code)
	}
}

func TestIntegrationsOff(t *testing.T) {
	c := newTestServer(t, nil)
	c.do("POST", "/api/auth/signup", creds)
	if code, st, _ := c.do("GET", "/api/status", nil); code != 200 || st["prowlarr"] != false || st["qbittorrent"] != false || st["library"] != false {
		t.Errorf("status: %d %v", code, st)
	}
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{"GET", "/api/search?q=game", nil},
		{"GET", "/api/platforms", nil},
		{"POST", "/api/requests", map[string]string{"releaseId": "0190a5b2-0000-7000-8000-000000000000", "platform": "GBA"}},
	} {
		if code, body, _ := c.do(tc.method, tc.path, tc.body); code != 503 || errCode(body) != "not_configured" {
			t.Errorf("%s %s: %d %v", tc.method, tc.path, code, body)
		}
	}
}

func TestValidation(t *testing.T) {
	c := newTestServer(t, nil)
	cases := []struct {
		name string
		body any
	}{
		{"short password", map[string]string{"email": "a@example.com", "password": "short"}},
		{"bad email", map[string]string{"email": "nope", "password": "a long enough password"}},
		{"unknown field", map[string]any{"email": "a@example.com", "password": "a long enough password", "admin": true}},
	}
	for _, tc := range cases {
		if code, body, _ := c.do("POST", "/api/auth/signup", tc.body); code != 400 || errCode(body) != "invalid_request" {
			t.Errorf("%s: %d %v", tc.name, code, body)
		}
	}
	if code, body, _ := c.do("GET", "/api/nope", nil); code != 404 || errCode(body) != "not_found" {
		t.Errorf("unknown endpoint: %d %v", code, body)
	}
}

func TestSignupDisabledByDefault(t *testing.T) {
	c := newTestServer(t, func(cfg *config.Config) { cfg.AllowSignup = false })
	if code, body, _ := c.do("POST", "/api/auth/signup", creds); code != 403 || errCode(body) != "signup_disabled" {
		t.Fatalf("signup: %d %v", code, body)
	}
}

func TestLoginRateLimited(t *testing.T) {
	c := newTestServer(t, func(cfg *config.Config) { cfg.AuthRatePerMinute = 2 })
	bad := map[string]string{"email": "x@example.com", "password": "wrong password!!"}
	c.do("POST", "/api/auth/login", bad)
	c.do("POST", "/api/auth/login", bad)
	code, body, hdr := c.do("POST", "/api/auth/login", bad)
	if code != 429 || errCode(body) != "rate_limited" || hdr.Get("Retry-After") == "" {
		t.Fatalf("third attempt: %d %v", code, body)
	}
}

func TestCrossOriginRefused(t *testing.T) {
	c := newTestServer(t, nil)
	req, _ := http.NewRequestWithContext(t.Context(), "POST", c.base+"/api/auth/login", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("cross-site POST: %d", resp.StatusCode)
	}
}

func TestProbesAndHeaders(t *testing.T) {
	c := newTestServer(t, nil)
	for _, p := range []string{"/healthz", "/readyz"} {
		resp, err := c.http.Get(c.base + p)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 204 {
			t.Errorf("%s: %d", p, resp.StatusCode)
		}
	}
	_, _, hdr := c.do("GET", "/api/auth/me", nil)
	if hdr.Get("Content-Security-Policy") == "" || hdr.Get("Cache-Control") != "no-store" {
		t.Errorf("API headers: CSP=%q Cache-Control=%q", hdr.Get("Content-Security-Policy"), hdr.Get("Cache-Control"))
	}
}

func TestBodyLimit(t *testing.T) {
	c := newTestServer(t, nil)
	big := map[string]string{"email": "a@example.com", "password": strings.Repeat("x", MaxBodyBytes)}
	if code, _, _ := c.do("POST", "/api/auth/signup", big); code != 413 {
		t.Fatalf("oversized body: %d", code)
	}
}
