// Package oidc signs users in through an OpenID Connect provider (such as Pocket ID) with the
// authorization code flow and PKCE. The provider is discovered on first use, not at startup, so
// the app starts while the provider is down.
package oidc

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"sync"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// Config is the client's registration with the provider.
type Config struct {
	Issuer       string
	ClientID     string
	ClientSecret string // empty for a public client
	RedirectURL  string
	// GroupsClaim names the claim listing the user's groups. WantGroups asks for the "groups"
	// scope, which Pocket ID needs before it includes them.
	GroupsClaim string
	WantGroups  bool
}

// Client talks to one provider.
type Client struct {
	cfg Config
	hc  *http.Client

	mu       sync.Mutex
	provider *gooidc.Provider
}

// New makes a Client. hc bounds every call to the provider.
func New(cfg Config, hc *http.Client) *Client {
	return &Client{cfg: cfg, hc: hc}
}

func (c *Client) setup(ctx context.Context) (*gooidc.Provider, oauth2.Config, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.provider == nil {
		p, err := gooidc.NewProvider(gooidc.ClientContext(ctx, c.hc), c.cfg.Issuer)
		if err != nil {
			return nil, oauth2.Config{}, fmt.Errorf("oidc: discovery: %w", err)
		}
		c.provider = p
	}
	scopes := []string{gooidc.ScopeOpenID, "email", "profile"}
	if c.cfg.WantGroups {
		scopes = append(scopes, "groups")
	}
	return c.provider, oauth2.Config{
		ClientID: c.cfg.ClientID, ClientSecret: c.cfg.ClientSecret, RedirectURL: c.cfg.RedirectURL,
		Endpoint: c.provider.Endpoint(), Scopes: scopes,
	}, nil
}

// Start is a sign-in begun: send the browser to URL, and keep State, Nonce and Verifier for
// the callback.
type Start struct {
	URL, State, Nonce, Verifier string
}

// Begin starts a sign-in.
func (c *Client) Begin(ctx context.Context) (Start, error) {
	_, oc, err := c.setup(ctx)
	if err != nil {
		return Start{}, err
	}
	st := Start{State: random(), Nonce: random(), Verifier: oauth2.GenerateVerifier()}
	st.URL = oc.AuthCodeURL(st.State, gooidc.Nonce(st.Nonce), oauth2.S256ChallengeOption(st.Verifier))
	return st, nil
}

// Identity is who the provider says signed in.
type Identity struct {
	Subject       string
	Email         string
	EmailVerified bool
	Groups        []string
}

// ErrInvalid means the provider's answer didn't check out (bad code, token or nonce).
var ErrInvalid = errors.New("oidc: sign-in not valid")

// Finish exchanges the code for tokens, verifies the ID token (signature, issuer, audience,
// expiry, nonce) and returns the identity in it.
func (c *Client) Finish(ctx context.Context, code, nonce, verifier string) (Identity, error) {
	provider, oc, err := c.setup(ctx)
	if err != nil {
		return Identity{}, err
	}
	ctx = context.WithValue(ctx, oauth2.HTTPClient, c.hc)
	tok, err := oc.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		var re *oauth2.RetrieveError
		if errors.As(err, &re) {
			return Identity{}, fmt.Errorf("%w: token exchange: %s", ErrInvalid, re.ErrorCode)
		}
		return Identity{}, fmt.Errorf("oidc: token exchange: %w", err)
	}
	raw, ok := tok.Extra("id_token").(string)
	if !ok {
		return Identity{}, fmt.Errorf("%w: no id_token", ErrInvalid)
	}
	idt, err := provider.VerifierContext(gooidc.ClientContext(ctx, c.hc), &gooidc.Config{ClientID: c.cfg.ClientID}).Verify(ctx, raw)
	if err != nil {
		return Identity{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if idt.Nonce != nonce {
		return Identity{}, fmt.Errorf("%w: nonce", ErrInvalid)
	}
	claims := map[string]any{}
	if err := idt.Claims(&claims); err != nil {
		return Identity{}, fmt.Errorf("%w: claims: %w", ErrInvalid, err)
	}
	// Some providers put email or groups only in userinfo.
	if _, ok := claims["email"]; !ok || (c.cfg.WantGroups && claims[c.cfg.GroupsClaim] == nil) {
		ui, err := provider.UserInfo(gooidc.ClientContext(ctx, c.hc), oauth2.StaticTokenSource(tok))
		if err == nil && ui.Subject == idt.Subject {
			extra := map[string]any{}
			if ui.Claims(&extra) == nil {
				for k, v := range extra {
					if _, have := claims[k]; !have {
						claims[k] = v
					}
				}
			}
		}
	}
	id := Identity{Subject: idt.Subject}
	id.Email, _ = claims["email"].(string)
	id.EmailVerified, _ = claims["email_verified"].(bool)
	switch g := claims[c.cfg.GroupsClaim].(type) {
	case []any:
		for _, v := range g {
			if s, ok := v.(string); ok {
				id.Groups = append(id.Groups, s)
			}
		}
	case string:
		id.Groups = []string{g}
	}
	return id, nil
}

func random() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b) // crypto/rand.Read never fails
	return base64.RawURLEncoding.EncodeToString(b)
}
