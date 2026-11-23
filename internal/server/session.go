package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/oldsj/rom-request/internal/auth"
	"github.com/oldsj/rom-request/internal/db/dbq"
	"github.com/oldsj/rom-request/internal/httpx"
)

// CookieName has the __Host- prefix: the browser only accepts it with Secure, Path=/ and no
// Domain, so a subdomain can't set or overwrite it.
const CookieName = "__Host-session"

type (
	userKey      struct{}
	tokenHashKey struct{}
)

// User is the signed-in user.
type User struct {
	ID    uuid.UUID
	Email string
	// Limited is a child's account: catalog games not rated mature, no release search.
	Limited bool
}

func userFrom(ctx context.Context) (User, bool) {
	u, ok := ctx.Value(userKey{}).(User)
	return u, ok
}

// sessionUser looks up the session cookie, if any, and puts its user in the context. Whether an
// operation needs one is the contract's business (its `security`), checked by the validator.
func (s *Server) sessionUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(CookieName)
		if err != nil || c.Value == "" || len(c.Value) > 128 {
			next.ServeHTTP(w, r)
			return
		}
		hash := auth.HashToken(c.Value)
		r = r.WithContext(context.WithValue(r.Context(), tokenHashKey{}, hash))
		row, err := s.q.GetSessionUser(r.Context(), hash)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			s.log.ErrorContext(r.Context(), "session lookup", "err", err)
			httpx.WriteError(w, http.StatusServiceUnavailable, "unavailable", "Try again shortly.")
			return
		default:
			r = r.WithContext(context.WithValue(r.Context(), userKey{}, User{ID: row.ID, Email: row.Email, Limited: row.Limited}))
		}
		next.ServeHTTP(w, r)
	})
}

// startSession stores a new session and returns its Set-Cookie value.
func (s *Server) startSession(ctx context.Context, userID uuid.UUID) (string, error) {
	token, hash, err := auth.NewSessionToken()
	if err != nil {
		return "", err
	}
	expires := time.Now().Add(s.cfg.SessionTTL)
	if err := s.q.CreateSession(ctx, dbq.CreateSessionParams{TokenHash: hash, UserID: userID, ExpiresAt: expires}); err != nil {
		return "", err
	}
	return sessionCookie(token, int(s.cfg.SessionTTL.Seconds())), nil
}

func sessionCookie(value string, maxAge int) string {
	c := &http.Cookie{
		Name:     CookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   true, // browsers allow Secure cookies on http://localhost, so development works
		SameSite: http.SameSiteLaxMode,
	}
	return c.String()
}

// SweepSessions deletes expired sessions and unfinished OIDC sign-ins, and expired search results (whose download links
// carry Prowlarr's API key), every 15 minutes until ctx ends.
func (s *Server) SweepSessions(ctx context.Context) {
	t := time.NewTicker(15 * time.Minute)
	defer t.Stop()
	for {
		n, err := s.q.DeleteExpiredSessions(ctx)
		if err != nil && ctx.Err() == nil {
			s.log.WarnContext(ctx, "session sweep", "err", err)
		} else if n > 0 {
			s.log.InfoContext(ctx, "expired sessions deleted", "count", n)
		}
		if _, err := s.q.DeleteExpiredSearchResults(ctx); err != nil && ctx.Err() == nil {
			s.log.WarnContext(ctx, "search result sweep", "err", err)
		}
		if _, err := s.q.DeleteExpiredOIDCLogins(ctx); err != nil && ctx.Err() == nil {
			s.log.WarnContext(ctx, "oidc sign-in sweep", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
