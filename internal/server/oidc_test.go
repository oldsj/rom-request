package server

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"

	"github.com/oldsj/rom-request/internal/config"
	"github.com/oldsj/rom-request/internal/oidc"
)

// fakeProvider is an OIDC provider: discovery, JWKS and a token endpoint that checks PKCE and
// signs ID tokens with an RSA key. The test plays the browser at the authorize step: approve
// issues a code for whatever claims the test gives.
type fakeProvider struct {
	t      *testing.T
	ts     *httptest.Server
	signer jose.Signer
	key    *rsa.PrivateKey

	mu    sync.Mutex
	codes map[string]pendingCode
	// audience, when set, replaces the ID token's aud (to test a token for another client).
	audience string
}

type pendingCode struct {
	challenge, nonce string
	claims           map[string]any
}

const testClientID = "rom-request"

func newFakeProvider(t *testing.T) *fakeProvider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: key, KeyID: "k1"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	p := &fakeProvider{t: t, signer: signer, key: key, codes: map[string]pendingCode{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{
			"issuer":                                p.ts.URL,
			"authorization_endpoint":                p.ts.URL + "/authorize",
			"token_endpoint":                        p.ts.URL + "/token",
			"jwks_uri":                              p.ts.URL + "/jwks",
			"userinfo_endpoint":                     p.ts.URL + "/userinfo",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
	})
	mux.HandleFunc("GET /userinfo", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) })
	mux.HandleFunc("POST /token", p.token)
	p.ts = httptest.NewTLSServer(mux)
	t.Cleanup(p.ts.Close)
	return p
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (p *fakeProvider) token(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	p.mu.Lock()
	pc, ok := p.codes[r.PostForm.Get("code")]
	delete(p.codes, r.PostForm.Get("code"))
	aud := p.audience
	p.mu.Unlock()
	sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	clientID, _, _ := r.BasicAuth()
	if clientID == "" {
		clientID = r.PostForm.Get("client_id")
	}
	if !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != pc.challenge || clientID != testClientID {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]string{"error": "invalid_grant"})
		return
	}
	if aud == "" {
		aud = testClientID
	}
	claims := map[string]any{
		"iss": p.ts.URL, "aud": aud, "nonce": pc.nonce,
		"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
	}
	for k, v := range pc.claims {
		claims[k] = v
	}
	payload, _ := json.Marshal(claims)
	jws, err := p.signer.Sign(payload)
	if err != nil {
		p.t.Error(err)
		return
	}
	idToken, _ := jws.CompactSerialize()
	writeJSON(w, map[string]any{"access_token": "at", "token_type": "Bearer", "expires_in": 3600, "id_token": idToken})
}

// approve plays the provider's sign-in page for the authorize URL and returns the callback
// query it would redirect the browser to.
func (p *fakeProvider) approve(authorizeURL string, claims map[string]any) string {
	p.t.Helper()
	u, err := url.Parse(authorizeURL)
	if err != nil || !strings.HasPrefix(authorizeURL, p.ts.URL+"/authorize?") {
		p.t.Fatalf("authorize URL %q", authorizeURL)
	}
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("client_id") != testClientID || q.Get("response_type") != "code" {
		p.t.Fatalf("authorize request: %v", q)
	}
	code := rand.Text()
	p.mu.Lock()
	p.codes[code] = pendingCode{challenge: q.Get("code_challenge"), nonce: q.Get("nonce"), claims: claims}
	p.mu.Unlock()
	return url.Values{"code": {code}, "state": {q.Get("state")}}.Encode()
}

func newOIDCServer(t *testing.T, mutate func(*config.Config)) (*client, *fakeProvider) {
	t.Helper()
	p := newFakeProvider(t)
	c := newTestServer(t, func(cfg *config.Config) {
		cfg.PasswordLogin = true
		cfg.OIDCIssuer = p.ts.URL
		cfg.OIDCClientID = testClientID
		cfg.OIDCGroupsClaim = "groups"
		cfg.OIDCName = "Pocket ID"
		if mutate != nil {
			mutate(cfg)
		}
	})
	cfg := c.srv.cfg
	c.srv.oidc = oidc.New(oidc.Config{
		Issuer: cfg.OIDCIssuer, ClientID: cfg.OIDCClientID, RedirectURL: c.base + "/api/auth/oidc/callback",
		GroupsClaim: cfg.OIDCGroupsClaim, WantGroups: len(cfg.OIDCAllowedGroups) > 0,
	}, p.ts.Client())
	c.http.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return c, p
}

// get makes a browser navigation and returns the status and Location.
func (c *client) get(path string) (int, string) {
	c.t.Helper()
	req, _ := http.NewRequestWithContext(c.t.Context(), "GET", c.base+path, nil)
	req.Header.Set("Sec-Fetch-Site", "none")
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode, resp.Header.Get("Location")
}

// signIn goes through the whole flow as the user claims describe, and returns where the
// callback sends the browser.
func (c *client) signIn(p *fakeProvider, claims map[string]any) string {
	c.t.Helper()
	code, loc := c.get("/api/auth/oidc/login")
	if code != http.StatusFound {
		c.t.Fatalf("oidc login: %d %s", code, loc)
	}
	_, back := c.get("/api/auth/oidc/callback?" + p.approve(loc, claims))
	return back
}

func ada(extra map[string]any) map[string]any {
	claims := map[string]any{"sub": "ada-sub", "email": "Ada@Example.com", "email_verified": true}
	for k, v := range extra {
		claims[k] = v
	}
	return claims
}

func TestOIDCSignIn(t *testing.T) {
	c, p := newOIDCServer(t, nil)

	if code, m, _ := c.do("GET", "/api/auth/methods", nil); code != 200 || m["oidc"] != true || m["oidcName"] != "Pocket ID" || m["password"] != true {
		t.Fatalf("methods: %d %v", code, m)
	}

	if back := c.signIn(p, ada(nil)); back != "/" {
		t.Fatalf("callback went to %q", back)
	}
	code, me, _ := c.do("GET", "/api/auth/me", nil)
	if code != 200 || me["email"] != "ada@example.com" {
		t.Fatalf("me: %d %v", code, me)
	}

	// The same subject is the same account, even when the email changed at the provider.
	o := c.other()
	if back := o.signIn(p, ada(map[string]any{"email": "ada@new.example"})); back != "/" {
		t.Fatalf("second sign-in went to %q", back)
	}
	if _, me2, _ := o.do("GET", "/api/auth/me", nil); me2["id"] != me["id"] {
		t.Errorf("second sign-in is another account: %v vs %v", me2, me)
	}

	// An account made through OIDC has no password to sign in with.
	if code, _, _ := o.other().do("POST", "/api/auth/login", map[string]string{"email": "ada@example.com", "password": "a long enough password"}); code != 401 {
		t.Errorf("password sign-in to an OIDC account: %d", code)
	}
}

func TestOIDCLinksVerifiedEmail(t *testing.T) {
	c, p := newOIDCServer(t, nil)
	_, user, _ := c.other().do("POST", "/api/auth/signup", creds)

	// Not verified by the provider: the existing account isn't handed over.
	if back := c.signIn(p, ada(map[string]any{"email_verified": false})); back != "/login?error=oidc_conflict" {
		t.Fatalf("unverified email: %q", back)
	}
	if back := c.signIn(p, ada(nil)); back != "/" {
		t.Fatalf("verified email: %q", back)
	}
	if _, me, _ := c.do("GET", "/api/auth/me", nil); me["id"] != user["id"] {
		t.Errorf("signed in as %v, want the password account %v", me, user)
	}
	// Linked: another identity with the same email can't take it.
	if back := c.other().signIn(p, ada(map[string]any{"sub": "someone-else"})); back != "/login?error=oidc_conflict" {
		t.Errorf("second identity: %q", back)
	}
}

func TestOIDCAllowedGroups(t *testing.T) {
	c, p := newOIDCServer(t, func(cfg *config.Config) { cfg.OIDCAllowedGroups = []string{"games", "admins"} })
	_, loc := c.get("/api/auth/oidc/login")
	if !strings.Contains(loc, "groups") {
		t.Errorf("groups scope not requested: %s", loc)
	}
	if back := c.signIn(p, ada(map[string]any{"groups": []string{"family"}})); back != "/login?error=oidc_forbidden" {
		t.Errorf("outside the groups: %q", back)
	}
	if back := c.signIn(p, ada(nil)); back != "/login?error=oidc_forbidden" {
		t.Errorf("no groups claim: %q", back)
	}
	if back := c.signIn(p, ada(map[string]any{"groups": []string{"family", "games"}})); back != "/" {
		t.Errorf("in a group: %q", back)
	}
}

func TestOIDCRefusesForgedCallbacks(t *testing.T) {
	c, p := newOIDCServer(t, nil)

	// A callback in a browser that didn't start the sign-in (login CSRF).
	_, loc := c.get("/api/auth/oidc/login")
	query := p.approve(loc, ada(nil))
	victim := c.other()
	if _, back := victim.get("/api/auth/oidc/callback?" + query); back != "/login?error=oidc_state" {
		t.Errorf("callback without the state cookie: %q", back)
	}
	// The real one still works, once.
	if _, back := c.get("/api/auth/oidc/callback?" + query); back != "/" {
		t.Errorf("callback: %q", back)
	}
	if _, back := c.get("/api/auth/oidc/callback?" + query); back != "/login?error=oidc_state" {
		t.Errorf("replayed callback: %q", back)
	}

	// The user cancelled at the provider.
	if _, back := c.get("/api/auth/oidc/callback?error=access_denied&state=x"); back != "/login?error=oidc_denied" {
		t.Errorf("denied: %q", back)
	}

	// A token for another client.
	p.audience = "another-app"
	if back := c.other().signIn(p, ada(nil)); back != "/login?error=oidc_invalid" {
		t.Errorf("wrong audience: %q", back)
	}
	p.audience = ""

	// No email to name the account by.
	if back := c.signIn(p, map[string]any{"sub": "anon"}); back != "/login?error=oidc_no_email" {
		t.Errorf("no email: %q", back)
	}
}

func TestOIDCProviderDown(t *testing.T) {
	c, p := newOIDCServer(t, nil)
	p.ts.Close()
	if code, loc := c.get("/api/auth/oidc/login"); code != http.StatusFound || loc != "/login?error=oidc_unavailable" {
		t.Errorf("provider down: %d %q", code, loc)
	}
}

func TestPasswordLoginOff(t *testing.T) {
	c, _ := newOIDCServer(t, func(cfg *config.Config) { cfg.PasswordLogin = false })
	if code, body, _ := c.do("POST", "/api/auth/login", creds); code != 403 || errCode(body) != "password_login_disabled" {
		t.Errorf("login: %d %v", code, body)
	}
	if code, body, _ := c.do("POST", "/api/auth/signup", creds); code != 403 || errCode(body) != "signup_disabled" {
		t.Errorf("signup: %d %v", code, body)
	}
	if _, m, _ := c.do("GET", "/api/auth/methods", nil); m["password"] != false || m["signup"] != false || m["oidc"] != true {
		t.Errorf("methods: %v", m)
	}
}

func TestOIDCOff(t *testing.T) {
	c := newTestServer(t, func(cfg *config.Config) { cfg.PasswordLogin = true })
	c.http.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if _, loc := c.get("/api/auth/oidc/login"); loc != "/login?error=oidc_off" {
		t.Errorf("login: %q", loc)
	}
	if _, m, _ := c.do("GET", "/api/auth/methods", nil); m["oidc"] != false || m["oidcName"] != nil {
		t.Errorf("methods: %v", m)
	}
}
