package server

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/oldsj/rom-request/internal/api"
	"github.com/oldsj/rom-request/internal/auth"
	"github.com/oldsj/rom-request/internal/db/dbq"
	"github.com/oldsj/rom-request/internal/oidc"
)

const (
	// oidcCookie binds a sign-in to the browser that started it. SameSite=Lax still sends it
	// on the provider's top-level redirect back.
	oidcCookie = "__Host-oidc"
	// oidcLoginTTL is how long the user has at the provider.
	oidcLoginTTL = 10 * time.Minute
	// oidcTimeout bounds each call to the provider.
	oidcTimeout = 15 * time.Second
)

// GetAuthMethods says which sign-in methods are on, for the sign-in page.
func (s *Server) GetAuthMethods(context.Context, api.GetAuthMethodsRequestObject) (api.GetAuthMethodsResponseObject, error) {
	m := api.GetAuthMethods200JSONResponse{
		Password: s.cfg.PasswordLogin,
		Signup:   s.cfg.PasswordLogin && s.cfg.AllowSignup,
		Oidc:     s.oidc != nil,
	}
	if s.oidc != nil {
		m.OidcName = &s.cfg.OIDCName
	}
	return m, nil
}

// OidcLogin sends the browser to the provider.
func (s *Server) OidcLogin(ctx context.Context, _ api.OidcLoginRequestObject) (api.OidcLoginResponseObject, error) {
	if s.oidc == nil {
		return api.OidcLogin302Response{Headers: loginError("oidc_off")}, nil
	}
	st, err := s.oidc.Begin(ctx)
	if err != nil {
		s.log.WarnContext(ctx, "oidc sign-in", "err", err)
		return api.OidcLogin302Response{Headers: loginError("oidc_unavailable")}, nil
	}
	if err := s.q.CreateOIDCLogin(ctx, dbq.CreateOIDCLoginParams{
		StateHash: auth.HashToken(st.State), Nonce: st.Nonce, Verifier: st.Verifier,
		ExpiresAt: time.Now().Add(oidcLoginTTL),
	}); err != nil {
		return nil, err
	}
	c := stateCookie(st.State, int(oidcLoginTTL.Seconds()))
	return api.OidcLogin302Response{Headers: api.RedirectResponseHeaders{Location: st.URL, SetCookie: &c}}, nil
}

// OidcCallback finishes the sign-in: checks the state against this browser's cookie, verifies
// the provider's ID token, finds or makes the account and starts a session.
func (s *Server) OidcCallback(ctx context.Context, req api.OidcCallbackRequestObject) (api.OidcCallbackResponseObject, error) {
	fail := func(code string) (api.OidcCallbackResponseObject, error) {
		return api.OidcCallback302Response{Headers: loginError(code)}, nil
	}
	p := req.Params
	if s.oidc == nil {
		return fail("oidc_off")
	}
	if p.Error != nil {
		return fail("oidc_denied") // the user cancelled, or the provider refused them
	}
	if p.State == nil || p.Code == nil || p.UnderscoreUnderscoreHostOidc == nil ||
		subtle.ConstantTimeCompare([]byte(*p.State), []byte(*p.UnderscoreUnderscoreHostOidc)) != 1 {
		return fail("oidc_state")
	}
	login, err := s.q.TakeOIDCLogin(ctx, auth.HashToken(*p.State))
	if errors.Is(err, pgx.ErrNoRows) {
		return fail("oidc_state") // used already, or too old
	}
	if err != nil {
		return nil, err
	}
	id, err := s.oidc.Finish(ctx, *p.Code, login.Nonce, login.Verifier)
	if errors.Is(err, oidc.ErrInvalid) {
		s.log.WarnContext(ctx, "oidc sign-in refused", "err", err)
		return fail("oidc_invalid")
	}
	if err != nil {
		s.log.WarnContext(ctx, "oidc sign-in", "err", err)
		return fail("oidc_unavailable")
	}
	in := func(groups []string) bool {
		return slices.ContainsFunc(id.Groups, func(g string) bool { return slices.Contains(groups, g) })
	}
	// An allowed group's member is a full user, whatever else; with no allowed groups, everyone
	// is, but for the limited groups' members.
	var limited bool
	switch {
	case len(s.cfg.OIDCAllowedGroups) > 0 && in(s.cfg.OIDCAllowedGroups):
	case in(s.cfg.OIDCLimitedGroups):
		limited = true
	case len(s.cfg.OIDCAllowedGroups) > 0:
		s.log.InfoContext(ctx, "oidc sign-in outside the allowed groups", "subject", id.Subject)
		return fail("oidc_forbidden")
	}
	user, code, err := s.oidcUser(ctx, id)
	if err != nil {
		return nil, err
	}
	if code != "" {
		return fail(code)
	}
	// Each sign-in follows the groups, which may have changed.
	if user.Limited != limited {
		if user, err = s.q.SetUserLimited(ctx, dbq.SetUserLimitedParams{ID: user.ID, Limited: limited}); err != nil {
			return nil, err
		}
	}
	cookie, err := s.startSession(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	return api.OidcCallback302Response{Headers: api.RedirectResponseHeaders{Location: "/", SetCookie: &cookie}}, nil
}

// oidcUser finds the account for id: the one linked to its subject; else an unlinked account
// with the same, provider-verified email, which it links; else a new account. code is set when
// the sign-in can't go ahead.
func (s *Server) oidcUser(ctx context.Context, id oidc.Identity) (dbq.User, string, error) {
	user, err := s.q.GetUserByOIDCSubject(ctx, pgText(id.Subject))
	if err == nil || !errors.Is(err, pgx.ErrNoRows) {
		return user, "", err
	}
	email := normalizeEmail(id.Email)
	if email == "" {
		return dbq.User{}, "oidc_no_email", nil
	}
	if id.EmailVerified {
		existing, err := s.q.GetUserByEmail(ctx, email)
		switch {
		case err == nil:
			user, err := s.q.LinkOIDCSubject(ctx, dbq.LinkOIDCSubjectParams{ID: existing.ID, OidcSubject: pgText(id.Subject)})
			if errors.Is(err, pgx.ErrNoRows) {
				return dbq.User{}, "oidc_conflict", nil // linked to another identity already
			}
			return user, "", err
		case !errors.Is(err, pgx.ErrNoRows):
			return dbq.User{}, "", err
		}
	}
	user, err = s.q.CreateOIDCUser(ctx, dbq.CreateOIDCUserParams{Email: email, OidcSubject: pgText(id.Subject)})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		// The email has an account, but the provider hasn't verified it's theirs.
		return dbq.User{}, "oidc_conflict", nil
	}
	return user, "", err
}

func loginError(code string) api.RedirectResponseHeaders {
	return api.RedirectResponseHeaders{Location: "/login?" + url.Values{"error": {code}}.Encode()}
}

func stateCookie(value string, maxAge int) string {
	c := &http.Cookie{
		Name: oidcCookie, Value: value, Path: "/", MaxAge: maxAge,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
	}
	return c.String()
}

func pgText(s string) pgtype.Text { return pgtype.Text{String: s, Valid: true} }
