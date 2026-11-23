package config

import (
	"testing"
)

func TestLoadRequiresDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	if _, err := Load(); err == nil {
		t.Fatal("Load() succeeded without DATABASE_URL")
	}
}

func TestLoadDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@db.example:5432/app")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AllowSignup {
		t.Error("signup must be off by default")
	}
	if cfg.ListenAddr != ":8080" {
		t.Errorf("ListenAddr = %q", cfg.ListenAddr)
	}
}

func TestLoadRejectsPlaintextRemoteDatabase(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@db.example:5432/app?sslmode=disable")
	if _, err := Load(); err == nil {
		t.Fatal("sslmode=disable to a remote host was accepted")
	}
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/app?sslmode=disable")
	if _, err := Load(); err != nil {
		t.Fatalf("localhost without TLS rejected: %v", err)
	}
}

func TestLoadValidatesTTL(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost/app")
	t.Setenv("SESSION_TTL", "1s")
	if _, err := Load(); err == nil {
		t.Fatal("SESSION_TTL=1s accepted")
	}
}

func TestLoadIntegrations(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost/app")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SearchEnabled() || cfg.DownloadsEnabled() || cfg.LibraryEnabled() {
		t.Error("integrations enabled without configuration")
	}

	t.Setenv("PROWLARR_URL", "http://prowlarr:9696")
	if _, err := Load(); err == nil {
		t.Error("PROWLARR_URL accepted without an API key")
	}
	t.Setenv("PROWLARR_API_KEY", "k")
	t.Setenv("QBITTORRENT_URL", "http://qbittorrent:8080")
	if _, err := Load(); err == nil {
		t.Error("QBITTORRENT_URL accepted without its folders")
	}
	t.Setenv("QBITTORRENT_SAVE_PATH", "/media/downloads/rom-request")
	t.Setenv("DOWNLOAD_DIR", "/data/downloads")
	t.Setenv("LIBRARY_DIR", "/data/library")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.SearchEnabled() || !cfg.DownloadsEnabled() || !cfg.LibraryEnabled() {
		t.Errorf("integrations not enabled: %+v", cfg)
	}

	t.Setenv("PROWLARR_URL", "file:///etc/passwd")
	if _, err := Load(); err == nil {
		t.Error("non-http PROWLARR_URL accepted")
	}
	t.Setenv("PROWLARR_URL", "http://prowlarr:9696")
	t.Setenv("LIBRARY_DIR", "relative/library")
	if _, err := Load(); err == nil {
		t.Error("relative LIBRARY_DIR accepted")
	}
}

func TestLoadOIDC(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost/app")
	t.Setenv("PASSWORD_LOGIN", "false")
	if _, err := Load(); err == nil {
		t.Error("PASSWORD_LOGIN=false accepted without OIDC")
	}
	t.Setenv("OIDC_ISSUER", "https://id.example.com")
	if _, err := Load(); err == nil {
		t.Error("OIDC_ISSUER accepted without a client")
	}
	t.Setenv("OIDC_CLIENT_ID", "rom-request")
	t.Setenv("OIDC_REDIRECT_URL", "https://roms.example.com/api/auth/oidc/callback")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.OIDCEnabled() || cfg.PasswordLogin || cfg.OIDCGroupsClaim != "groups" {
		t.Errorf("oidc config: %+v", cfg)
	}
	for _, bad := range []struct{ key, value string }{
		{"OIDC_ISSUER", "http://id.example.com"},
		{"OIDC_REDIRECT_URL", "https://roms.example.com/elsewhere"},
		{"OIDC_REDIRECT_URL", "http://roms.example.com/api/auth/oidc/callback"},
	} {
		t.Run(bad.key+"="+bad.value, func(t *testing.T) {
			t.Setenv(bad.key, bad.value)
			if _, err := Load(); err == nil {
				t.Error("accepted")
			}
		})
	}
	t.Setenv("OIDC_ISSUER", "http://localhost:1411")
	if _, err := Load(); err != nil {
		t.Errorf("http issuer on localhost: %v", err)
	}
}

func TestLoadCatalog(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost/app")
	t.Setenv("IGDB_CLIENT_ID", "id")
	if _, err := Load(); err == nil {
		t.Error("IGDB_CLIENT_ID accepted without a secret")
	}
	t.Setenv("IGDB_CLIENT_SECRET", "secret")
	if _, err := Load(); err == nil {
		t.Error("catalog accepted without a library")
	}
	t.Setenv("LIBRARY_DIR", "/data/library")
	t.Setenv("CATALOG_PLATFORMS", "megadrive=genesis-slash-megadrive,pce=turbografx16--1")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.CatalogEnabled() || cfg.CatalogPlatforms["pce"] != "turbografx16--1" {
		t.Errorf("catalog config: %+v", cfg)
	}
}
