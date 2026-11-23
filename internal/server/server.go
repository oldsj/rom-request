// Package server wires the HTTP stack together and implements api.StrictServerInterface.
package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/jackc/pgx/v5/pgxpool"
	nethttpmiddleware "github.com/oapi-codegen/nethttp-middleware"

	"github.com/oldsj/rom-request/internal/api"
	"github.com/oldsj/rom-request/internal/config"
	"github.com/oldsj/rom-request/internal/db/dbq"
	"github.com/oldsj/rom-request/internal/httpx"
	"github.com/oldsj/rom-request/internal/igdb"
	"github.com/oldsj/rom-request/internal/library"
	"github.com/oldsj/rom-request/internal/oidc"
	"github.com/oldsj/rom-request/internal/prowlarr"
	"github.com/oldsj/rom-request/internal/qbittorrent"
	"github.com/oldsj/rom-request/internal/requests"
	"github.com/oldsj/rom-request/web"
)

// MaxBodyBytes caps every API request body.
const MaxBodyBytes = 1 << 20

// Server holds what the handlers need.
type Server struct {
	cfg      config.Config
	log      *slog.Logger
	pool     *pgxpool.Pool
	q        *dbq.Queries
	draining atomic.Bool

	// Integrations; nil when not configured.
	search  *prowlarr.Client
	lib     *library.Library
	reqs    *requests.Service
	oidc    *oidc.Client
	catalog *igdb.Client
}

var _ api.StrictServerInterface = (*Server)(nil)

// integrationTimeout bounds each call to Prowlarr or qBittorrent.
const integrationTimeout = 25 * time.Second

// New makes a Server with the integrations cfg configures. cfg has been validated.
func New(cfg config.Config, log *slog.Logger, pool *pgxpool.Pool) (*Server, error) {
	s := &Server{cfg: cfg, log: log, pool: pool, q: dbq.New(pool)}
	hc := &http.Client{Timeout: integrationTimeout}
	if cfg.SearchEnabled() {
		c, err := prowlarr.New(cfg.ProwlarrURL, cfg.ProwlarrAPIKey, hc)
		if err != nil {
			return nil, err
		}
		s.search = c
	}
	if cfg.LibraryEnabled() {
		s.lib = library.New(cfg.LibraryDir, cfg.LibraryExclude)
	}
	if cfg.DownloadsEnabled() {
		qb, err := qbittorrent.New(cfg.QBittorrentURL, cfg.QBittorrentUsername, cfg.QBittorrentPassword, hc)
		if err != nil {
			return nil, err
		}
		s.reqs = requests.New(s.q, qb, s.lib, hc, requests.Config{
			SavePath: cfg.QBittorrentSavePath, DownloadDir: cfg.DownloadDir, Category: cfg.QBittorrentCategory,
		}, log)
	}
	if cfg.CatalogEnabled() {
		s.catalog = igdb.New(igdb.Config{ClientID: cfg.IGDBClientID, ClientSecret: cfg.IGDBClientSecret}, hc)
	}
	if cfg.OIDCEnabled() {
		s.oidc = oidc.New(oidc.Config{
			Issuer: cfg.OIDCIssuer, ClientID: cfg.OIDCClientID, ClientSecret: cfg.OIDCClientSecret,
			RedirectURL: cfg.OIDCRedirectURL, GroupsClaim: cfg.OIDCGroupsClaim, WantGroups: len(cfg.OIDCAllowedGroups) > 0,
		}, &http.Client{Timeout: oidcTimeout})
	}
	return s, nil
}

// Worker is the request worker, or nil when downloads aren't configured.
func (s *Server) Worker() *requests.Service { return s.reqs }

// Drain makes /readyz fail, so the load balancer stops sending traffic before shutdown.
func (s *Server) Drain() { s.draining.Store(true) }

// Handler is the whole application: probes, the API under /api and the SPA.
func (s *Server) Handler() (http.Handler, error) {
	apiHandler, err := s.apiHandler()
	if err != nil {
		return nil, err
	}

	app := http.NewServeMux()
	app.Handle("/api/", http.StripPrefix("/api", apiHandler))
	app.Handle("/", web.Handler(web.Dist()))

	// Go's CSRF defence: refuses cross-origin unsafe requests using Sec-Fetch-Site and Origin.
	// Together with SameSite=Lax cookies this needs no tokens.
	csrf := http.NewCrossOriginProtection()
	csrf.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		httpx.WriteError(w, http.StatusForbidden, "cross_origin", "Cross-origin request refused.")
	}))

	root := http.NewServeMux()
	// Probes skip the access log and middleware; they would only add noise.
	root.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	root.HandleFunc("GET /readyz", s.ready)
	root.Handle("/", httpx.Chain(app,
		httpx.Recover(s.log),
		httpx.ClientIP(s.cfg.TrustedProxies),
		httpx.AccessLog(s.log),
		httpx.SecurityHeaders,
		csrf.Handler,
	))
	return root, nil
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	if s.draining.Load() {
		http.Error(w, "draining", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.pool.Ping(ctx); err != nil {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) apiHandler() (http.Handler, error) {
	spec, err := api.GetSpec()
	if err != nil {
		return nil, err
	}
	spec.Servers = nil // mounted under /api with the prefix stripped
	// kin-openapi checks only the formats it is told about.
	openapi3.DefineStringFormatValidator("email", openapi3.NewRegexpFormatValidator(openapi3.FormatOfStringForEmail))

	validator := nethttpmiddleware.OapiRequestValidatorWithOptions(spec, &nethttpmiddleware.Options{
		DoNotValidateServers: true,
		Options: openapi3filter.Options{
			// Operations that declare the session scheme need a signed-in user; sessionUser
			// has already looked the cookie up.
			AuthenticationFunc: func(ctx context.Context, in *openapi3filter.AuthenticationInput) error {
				if _, ok := userFrom(in.RequestValidationInput.Request.Context()); !ok {
					return errors.New("not signed in")
				}
				return nil
			},
		},
		ErrorHandlerWithOpts: s.validationError,
	})

	strict := api.NewStrictHandlerWithOptions(s, nil, api.StrictHTTPServerOptions{
		RequestErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, _ error) {
			httpx.WriteError(w, http.StatusBadRequest, "bad_request", "The request body could not be read.")
		},
		ResponseErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			s.log.ErrorContext(r.Context(), "handler failed", "path", r.URL.Path, "err", err)
			httpx.WriteError(w, http.StatusInternalServerError, "internal", "Something went wrong.")
		},
	})
	handler := api.HandlerWithOptions(strict, api.StdHTTPServerOptions{BaseRouter: http.NewServeMux()})

	limiter := httpx.NewRateLimiter(s.cfg.AuthRatePerMinute)
	limitAuth := func(next http.Handler) http.Handler {
		limited := limiter.Middleware(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if (r.Method == http.MethodPost && (r.URL.Path == "/auth/login" || r.URL.Path == "/auth/signup")) ||
				(r.Method == http.MethodGet && (r.URL.Path == "/auth/oidc/login" || r.URL.Path == "/auth/oidc/callback")) {
				limited.ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}

	return httpx.Chain(handler,
		noStore,
		httpx.MaxBody(MaxBodyBytes),
		limitAuth,
		s.sessionUser,
		validator,
	), nil
}

func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) validationError(_ context.Context, err error, w http.ResponseWriter, _ *http.Request, opts nethttpmiddleware.ErrorHandlerOpts) {
	var secErr *openapi3filter.SecurityRequirementsError
	switch {
	case errors.As(err, &secErr):
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "Sign in to continue.")
	case opts.MatchedRoute == nil && opts.StatusCode == http.StatusNotFound:
		httpx.WriteError(w, http.StatusNotFound, "not_found", "No such endpoint.")
	case opts.StatusCode == http.StatusMethodNotAllowed:
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed.")
	default:
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", validationMessage(err))
	}
}

// validationMessage keeps the first line of the validator's message, which names the field and
// the rule, and drops the schema dump that follows.
func validationMessage(err error) string {
	msg, _, _ := strings.Cut(err.Error(), "\n")
	if len(msg) > 300 {
		msg = msg[:300]
	}
	return msg
}
