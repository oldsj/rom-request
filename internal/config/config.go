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
	// WorkerInterval is how often active requests are checked against qBittorrent.
	WorkerInterval time.Duration `env:"WORKER_INTERVAL" envDefault:"15s"`
}

// SearchEnabled reports whether Prowlarr is configured.
func (c Config) SearchEnabled() bool { return c.ProwlarrURL != "" }

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
	for name, u := range map[string]string{"PROWLARR_URL": c.ProwlarrURL, "QBITTORRENT_URL": c.QBittorrentURL} {
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

func isLocalDatabase(url string) bool {
	return strings.Contains(url, "@localhost") || strings.Contains(url, "@127.0.0.1") ||
		strings.Contains(url, "host=localhost") || strings.Contains(url, "host=127.0.0.1")
}

// TestDatabaseURL is the admin URL of a disposable PostgreSQL server for integration tests.
func TestDatabaseURL() string { return os.Getenv("TEST_DATABASE_URL") }

// InCI reports whether tests run in CI, where a missing test database is a failure, not a skip.
func InCI() bool { return os.Getenv("CI") == "true" }
