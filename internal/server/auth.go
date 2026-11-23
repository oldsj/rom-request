package server

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/oldsj/rom-request/internal/api"
	"github.com/oldsj/rom-request/internal/auth"
	"github.com/oldsj/rom-request/internal/db/dbq"
)

func normalizeEmail(e string) string { return strings.ToLower(strings.TrimSpace(e)) }

// Signup creates an account and signs it in.
func (s *Server) Signup(ctx context.Context, req api.SignupRequestObject) (api.SignupResponseObject, error) {
	if !s.cfg.AllowSignup {
		return api.Signup403JSONResponse{ForbiddenJSONResponse: api.ForbiddenJSONResponse(apiError("signup_disabled", "Sign-up is turned off."))}, nil
	}
	hash, err := auth.HashPassword(req.Body.Password)
	if err != nil {
		return nil, err
	}
	user, err := s.q.CreateUser(ctx, dbq.CreateUserParams{Email: normalizeEmail(string(req.Body.Email)), PasswordHash: hash})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return api.Signup409JSONResponse{ConflictJSONResponse: api.ConflictJSONResponse(apiError("email_taken", "That email already has an account."))}, nil
	}
	if err != nil {
		return nil, err
	}
	cookie, err := s.startSession(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	return api.Signup201JSONResponse{
		Body:    api.User{Id: user.ID, Email: user.Email},
		Headers: api.Signup201ResponseHeaders{SetCookie: &cookie},
	}, nil
}

// Login checks the password and starts a new session.
func (s *Server) Login(ctx context.Context, req api.LoginRequestObject) (api.LoginResponseObject, error) {
	wrong := api.Login401JSONResponse{UnauthorizedJSONResponse: api.UnauthorizedJSONResponse(apiError("invalid_credentials", "Wrong email or password."))}
	user, err := s.q.GetUserByEmail(ctx, normalizeEmail(string(req.Body.Email)))
	if errors.Is(err, pgx.ErrNoRows) {
		auth.BurnTime(req.Body.Password) // same timing whether or not the account exists
		return wrong, nil
	}
	if err != nil {
		return nil, err
	}
	ok, err := auth.CheckPassword(user.PasswordHash, req.Body.Password)
	if err != nil {
		return nil, err
	}
	if !ok {
		return wrong, nil
	}
	cookie, err := s.startSession(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	return api.Login200JSONResponse{
		Body:    api.User{Id: user.ID, Email: user.Email},
		Headers: api.Login200ResponseHeaders{SetCookie: &cookie},
	}, nil
}

// Logout ends the session, if there is one, and clears the cookie.
func (s *Server) Logout(ctx context.Context, _ api.LogoutRequestObject) (api.LogoutResponseObject, error) {
	if hash, ok := ctx.Value(tokenHashKey{}).([]byte); ok {
		if err := s.q.DeleteSession(ctx, hash); err != nil {
			return nil, err
		}
	}
	cleared := sessionCookie("", -1)
	return api.Logout204Response{Headers: api.Logout204ResponseHeaders{SetCookie: &cleared}}, nil
}

// GetMe returns the signed-in user.
func (s *Server) GetMe(ctx context.Context, _ api.GetMeRequestObject) (api.GetMeResponseObject, error) {
	u, ok := userFrom(ctx)
	if !ok {
		return api.GetMe401JSONResponse{UnauthorizedJSONResponse: api.UnauthorizedJSONResponse(errUnauthorized)}, nil
	}
	return api.GetMe200JSONResponse{Id: u.ID, Email: u.Email}, nil
}
