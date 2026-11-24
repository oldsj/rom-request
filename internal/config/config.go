// Package config reads and validates the process environment. It is the only package that
// reads it (golangci-lint's forbidigo enforces this).
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
)

// Config is the server's configuration. Secrets have no defaults: a missing one stops startup.
type Config struct {
	// DatabaseURL is a libpq URL or DSN. Secret.
	DatabaseURL string `env:"DATABASE_URL,required,notEmpty"`
	// ListenAddr is where the HTTP server listens.
	ListenAddr string `env:"LISTEN_ADDR" envDefault:":8080"`
	// LogLevel is debug, info, warn or error.
	LogLevel slog.Level `env:"LOG_LEVEL" envDefault:"info"`
	// AllowSignup lets anyone create an account. Off by default: turn it on to create the
	// first account, then off again for a private app.
	AllowSignup bool `env:"ALLOW_SIGNUP" envDefault:"false"`
	// SessionTTL is how long a sign-in lasts.
	SessionTTL time.Duration `env:"SESSION_TTL" envDefault:"720h"`
	// TrustedProxies are the CIDRs of reverse proxies whose X-Forwarded-For is believed, for
	// the client address used by rate limiting. Empty means use the TCP peer address.
	TrustedProxies []netip.Prefix `env:"TRUSTED_PROXIES" envSeparator:","`
	// AuthRatePerMinute is the sign-in and sign-up attempts allowed per client address.
	AuthRatePerMinute int `env:"AUTH_RATE_PER_MINUTE" envDefault:"10"`
	// PasswordLogin allows signing in (and, with AllowSignup, signing up) with an email and
	// password. Turn it off to sign in only through OIDC.
	PasswordLogin bool `env:"PASSWORD_LOGIN" envDefault:"true"`

	// OIDC sign-in (for example Pocket ID). Optional: set the issuer, client id and redirect URL
	// to turn it on. An account is made on first sign-in, whatever AllowSignup says: who may
	// sign in is the identity provider's call, and OIDCAllowedGroups narrows it further.

	// OIDCIssuer is the provider's issuer URL, e.g. https://id.example.com.
	OIDCIssuer string `env:"OIDC_ISSUER"`
	// OIDCClientID and OIDCClientSecret identify this app to the provider. The secret is
	// secret; leave it empty for a public client (PKCE is always used).
	OIDCClientID     string `env:"OIDC_CLIENT_ID"`
	OIDCClientSecret string `env:"OIDC_CLIENT_SECRET"`
	// OIDCRedirectURL is this app's callback, registered with the provider:
	// https://<host>/api/auth/oidc/callback.
	OIDCRedirectURL string `env:"OIDC_REDIRECT_URL"`
	// OIDCAllowedGroups, if set, admits only members of one of these groups (from the
	// OIDCGroupsClaim claim; requested with the "groups" scope).
	OIDCAllowedGroups []string `env:"OIDC_ALLOWED_GROUPS" envSeparator:","`
	// OIDCLimitedGroups admit their members as limited users (children): they browse and
	// request only catalog games not rated mature, and can't search releases. A member of an
	// allowed group too is not limited.
	OIDCLimitedGroups []string `env:"OIDC_LIMITED_GROUPS" envSeparator:","`
	OIDCGroupsClaim   string   `env:"OIDC_GROUPS_CLAIM" envDefault:"groups"`
	// OIDCName names the provider on the sign-in button.
	OIDCName string `env:"OIDC_NAME" envDefault:"single sign-on"`

	// MigrateOnStart applies database migrations before serving.
	MigrateOnStart bool `env:"MIGRATE_ON_START" envDefault:"true"`

	// Integrations. Each is optional: what isn't configured is reported by GET /api/status and
	// the operations that need it answer 503.

	// ProwlarrURL is Prowlarr's base URL, e.g. http://prowlarr.media:9696.
	ProwlarrURL string `env:"PROWLARR_URL"`
	// ProwlarrAPIKey is Prowlarr's API key (Settings > General). Secret.
	ProwlarrAPIKey string `env:"PROWLARR_API_KEY"`
	// ProwlarrCategories limits searches to these Newznab category ids (1000 is Console).
	// Empty searches every category.
	ProwlarrCategories []int `env:"PROWLARR_CATEGORIES" envSeparator:","`
	// ProwlarrBlockedIndexers are indexer names (as Prowlarr shows them) whose results are
	// dropped. BlueRoms serves its own web page and images, not the game, as a "release".
	ProwlarrBlockedIndexers []string `env:"PROWLARR_BLOCKED_INDEXERS" envSeparator:"," envDefault:"BlueRoms"`

	// QBittorrentURL is qBittorrent's Web UI base URL, e.g. http://qbittorrent.torrent:8080.
	QBittorrentURL string `env:"QBITTORRENT_URL"`
	// QBittorrentUsername and QBittorrentPassword sign in to the Web UI. Leave both empty when
	// qBittorrent bypasses authentication for this client's address. The password is secret.
	QBittorrentUsername string `env:"QBITTORRENT_USERNAME"`
	QBittorrentPassword string `env:"QBITTORRENT_PASSWORD"`
	// QBittorrentCategory is set on every torrent, if not empty. It must already exist.
	QBittorrentCategory string `env:"QBITTORRENT_CATEGORY"`
	// QBittorrentSavePath is the folder, as qBittorrent sees it, under which each request
	// downloads into a folder named after the request's id.
	QBittorrentSavePath string `env:"QBITTORRENT_SAVE_PATH"`

	// DownloadDir is QBittorrentSavePath as this process sees it (a read-only mount of the
	// same folder). Imports read only below it.
	DownloadDir string `env:"DOWNLOAD_DIR"`
	// LibraryDir is the ROM library RomM scans: one top-level folder per platform. Imports
	// write only below it, and never overwrite.
	LibraryDir string `env:"LIBRARY_DIR"`
	// LibraryExclude are top-level library folders that aren't platforms. Folders starting
	// with ".", "#" or "@" are always skipped.
	LibraryExclude []string `env:"LIBRARY_EXCLUDE" envSeparator:"," envDefault:"bios"`
	// IGDBClientID and IGDBClientSecret are a Twitch application's credentials (the same ones
	// RomM uses), for the game catalog. Optional; the secret is secret.
	IGDBClientID     string `env:"IGDB_CLIENT_ID"`
	IGDBClientSecret string `env:"IGDB_CLIENT_SECRET"`
	// CatalogPlatforms maps library folders to IGDB platform slugs where the names differ and
	// the built-in aliases don't cover it, e.g. "megadrive=genesis-slash-megadrive".
	CatalogPlatforms map[string]string `env:"CATALOG_PLATFORMS" envSeparator:"," envKeyValSeparator:"="`

	// RomMURL is RomM's base URL, e.g. http://romm.romm.svc:8080. With RomMAPIToken, the worker
	// asks RomM for a quick scan of a platform folder after each import.
	RomMURL string `env:"ROMM_URL"`
	// RomMAPIToken is a RomM client API token (an admin's, made in RomM under Profile, with only
	// the tasks.run scope). Secret. Without it, new games wait for RomM's own scheduled scan.
	RomMAPIToken string `env:"ROMM_API_TOKEN"`

	// WorkerInterval is how often active requests are checked against qBittorrent.
	WorkerInterval time.Duration `env:"WORKER_INTERVAL" envDefault:"15s"`
}

// OIDCEnabled reports whether OIDC sign-in is configured.
func (c Config) OIDCEnabled() bool { return c.OIDCIssuer != "" }

// CatalogEnabled reports whether the IGDB catalog is configured.
func (c Config) CatalogEnabled() bool { return c.IGDBClientID != "" }

// SearchEnabled reports whether Prowlarr is configured.
func (c Config) SearchEnabled() bool { return c.ProwlarrURL != "" }

// RomMScanEnabled reports whether imports ask RomM to scan.
func (c Config) RomMScanEnabled() bool { return c.RomMURL != "" && c.RomMAPIToken != "" }

// LibraryEnabled reports whether the ROM library folder is configured.
func (c Config) LibraryEnabled() bool { return c.LibraryDir != "" }

// DownloadsEnabled reports whether requests can be downloaded and imported: qBittorrent, the
// download folder in both views, and the library.
func (c Config) DownloadsEnabled() bool { return c.QBittorrentURL != "" && c.LibraryEnabled() }

// Load reads the environment and validates it.
func Load() (Config, error) {
	cfg, err := env.ParseAs[Config]()
	if err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	return cfg, cfg.validate()
}

func (c Config) validate() error {
	var errs []error
	if c.SessionTTL < time.Minute || c.SessionTTL > 90*24*time.Hour {
		errs = append(errs, errors.New("SESSION_TTL must be between 1m and 2160h"))
	}
	if c.AuthRatePerMinute < 1 {
		errs = append(errs, errors.New("AUTH_RATE_PER_MINUTE must be at least 1"))
	}
	errs = append(errs, c.validateIntegrations()...)
	errs = append(errs, c.validateOIDC()...)
	if strings.Contains(c.DatabaseURL, "sslmode=disable") && !isLocalDatabase(c.DatabaseURL) {
		errs = append(errs, errors.New("DATABASE_URL: sslmode=disable is only allowed for localhost"))
	}
	if len(errs) > 0 {
		return fmt.Errorf("config: %w", errors.Join(errs...))
	}
	return nil
}

func (c Config) validateIntegrations() []error {
	var errs []error
	for name, u := range map[string]string{"PROWLARR_URL": c.ProwlarrURL, "QBITTORRENT_URL": c.QBittorrentURL, "ROMM_URL": c.RomMURL} {
		if u == "" {
			continue
		}
		if p, err := url.Parse(u); err != nil || (p.Scheme != "http" && p.Scheme != "https") || p.Host == "" {
			errs = append(errs, fmt.Errorf("%s must be an http(s) URL", name))
		}
	}
	if c.ProwlarrURL != "" && c.ProwlarrAPIKey == "" {
		errs = append(errs, errors.New("PROWLARR_API_KEY is required with PROWLARR_URL"))
	}
	if c.RomMAPIToken != "" && c.RomMURL == "" {
		errs = append(errs, errors.New("ROMM_API_TOKEN needs ROMM_URL"))
	}
	if (c.IGDBClientID == "") != (c.IGDBClientSecret == "") {
		errs = append(errs, errors.New("IGDB_CLIENT_ID and IGDB_CLIENT_SECRET go together"))
	}
	if c.IGDBClientID != "" && c.LibraryDir == "" {
		errs = append(errs, errors.New("IGDB_CLIENT_ID needs LIBRARY_DIR: the catalog browses the library's platforms"))
	}
	if (c.QBittorrentUsername == "") != (c.QBittorrentPassword == "") {
		errs = append(errs, errors.New("QBITTORRENT_USERNAME and QBITTORRENT_PASSWORD go together"))
	}
	if c.QBittorrentURL != "" {
		if c.QBittorrentSavePath == "" || c.DownloadDir == "" || c.LibraryDir == "" {
			errs = append(errs, errors.New("QBITTORRENT_URL needs QBITTORRENT_SAVE_PATH, DOWNLOAD_DIR and LIBRARY_DIR"))
		}
		if c.QBittorrentSavePath != "" && !strings.HasPrefix(c.QBittorrentSavePath, "/") {
			errs = append(errs, errors.New("QBITTORRENT_SAVE_PATH must be absolute"))
		}
	}
	for name, dir := range map[string]string{"DOWNLOAD_DIR": c.DownloadDir, "LIBRARY_DIR": c.LibraryDir} {
		if dir != "" && !filepath.IsAbs(dir) {
			errs = append(errs, fmt.Errorf("%s must be absolute", name))
		}
	}
	if c.WorkerInterval < time.Second || c.WorkerInterval > time.Hour {
		errs = append(errs, errors.New("WORKER_INTERVAL must be between 1s and 1h"))
	}
	return errs
}

func (c Config) validateOIDC() []error {
	var errs []error
	if !c.OIDCEnabled() {
		if c.OIDCClientID != "" || c.OIDCRedirectURL != "" {
			errs = append(errs, errors.New("OIDC_CLIENT_ID and OIDC_REDIRECT_URL need OIDC_ISSUER"))
		}
		if !c.PasswordLogin {
			errs = append(errs, errors.New("PASSWORD_LOGIN=false needs OIDC sign-in (OIDC_ISSUER)"))
		}
		return errs
	}
	if c.OIDCClientID == "" || c.OIDCRedirectURL == "" {
		errs = append(errs, errors.New("OIDC_ISSUER needs OIDC_CLIENT_ID and OIDC_REDIRECT_URL"))
	}
	for name, u := range map[string]string{"OIDC_ISSUER": c.OIDCIssuer, "OIDC_REDIRECT_URL": c.OIDCRedirectURL} {
		if u != "" && !secureURL(u) {
			errs = append(errs, fmt.Errorf("%s must be an https URL (http only for localhost)", name))
		}
	}
	if c.OIDCRedirectURL != "" && !strings.HasSuffix(c.OIDCRedirectURL, "/api/auth/oidc/callback") {
		errs = append(errs, errors.New("OIDC_REDIRECT_URL must end in /api/auth/oidc/callback"))
	}
	if c.OIDCGroupsClaim == "" {
		errs = append(errs, errors.New("OIDC_GROUPS_CLAIM must not be empty"))
	}
	return errs
}

// secureURL is an absolute https URL, or http for localhost.
func secureURL(u string) bool {
	p, err := url.Parse(u)
	if err != nil || p.Host == "" {
		return false
	}
	switch p.Scheme {
	case "https":
		return true
	case "http":
		h := p.Hostname()
		return h == "localhost" || h == "127.0.0.1" || h == "::1"
	}
	return false
}

func isLocalDatabase(url string) bool {
	return strings.Contains(url, "@localhost") || strings.Contains(url, "@127.0.0.1") ||
		strings.Contains(url, "host=localhost") || strings.Contains(url, "host=127.0.0.1")
}

// TestDatabaseURL is the admin URL of a disposable PostgreSQL server for integration tests.
func TestDatabaseURL() string { return os.Getenv("TEST_DATABASE_URL") }

// InCI reports whether tests run in CI, where a missing test database is a failure, not a skip.
func InCI() bool { return os.Getenv("CI") == "true" }
