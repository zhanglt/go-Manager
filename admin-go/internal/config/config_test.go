package config

import (
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	for _, name := range []string{
		"MANAGER_SERVER_PORT", "CTRL_SERVER_IP", "CTRL_SERVER_PORT", "MANAGER_SSL",
		"CTRL_TLS_VERIFY", "HTTP_MAX_HEADER_LENGTH", "PATH_PREFIX", "MANAGER_INTERNAL_ADDR", "IS_DEV",
		"MANAGER_MAX_BODY_BYTES", "MANAGER_MAX_CONNECTIONS", "MANAGER_READ_HEADER_TIMEOUT",
		"MANAGER_READ_TIMEOUT", "MANAGER_WRITE_TIMEOUT", "MANAGER_IDLE_TIMEOUT",
		"MANAGER_SHUTDOWN_TIMEOUT", "CTRL_REQUEST_TIMEOUT",
		"MANAGER_SESSION_MAX_ENTRIES",
		"MANAGER_CACHE_MAX_ENTRIES", "MANAGER_CACHE_MAX_BYTES", "MANAGER_CACHE_TTL",
		"MANAGER_SUPPORT_COMMAND", "MANAGER_SUPPORT_TEMP_DIR", "MANAGER_SUPPORT_TIMEOUT",
		"MANAGER_SUPPORT_MAX_FILE_BYTES", "MANAGER_SUPPORT_MAX_CONCURRENT",
		"MANAGER_PUBLIC_URL", "MANAGER_SSO_STATE_TTL", "MANAGER_SSO_MAX_PENDING",
	} {
		t.Setenv(name, "")
	}
	// An explicitly empty value is different from an absent value for most compatibility options.
	for _, name := range []string{"MANAGER_SERVER_PORT", "CTRL_SERVER_IP", "CTRL_SERVER_PORT", "MANAGER_SSL", "CTRL_TLS_VERIFY", "HTTP_MAX_HEADER_LENGTH", "MANAGER_MAX_BODY_BYTES", "MANAGER_MAX_CONNECTIONS", "MANAGER_READ_HEADER_TIMEOUT", "MANAGER_READ_TIMEOUT", "MANAGER_WRITE_TIMEOUT", "MANAGER_IDLE_TIMEOUT", "MANAGER_SHUTDOWN_TIMEOUT", "CTRL_REQUEST_TIMEOUT", "MANAGER_SESSION_MAX_ENTRIES", "MANAGER_CACHE_MAX_ENTRIES", "MANAGER_CACHE_MAX_BYTES", "MANAGER_CACHE_TTL", "MANAGER_SUPPORT_COMMAND", "MANAGER_SUPPORT_TEMP_DIR", "MANAGER_SUPPORT_TIMEOUT", "MANAGER_SUPPORT_MAX_FILE_BYTES", "MANAGER_SUPPORT_MAX_CONCURRENT", "MANAGER_SSO_STATE_TTL", "MANAGER_SSO_MAX_PENDING"} {
		t.Setenv(name, map[string]string{
			"MANAGER_SERVER_PORT": "8443", "CTRL_SERVER_IP": "127.0.0.1", "CTRL_SERVER_PORT": "10443",
			"MANAGER_SSL": "on", "CTRL_TLS_VERIFY": "false", "HTTP_MAX_HEADER_LENGTH": "32k",
			"MANAGER_MAX_BODY_BYTES": "50m", "MANAGER_MAX_CONNECTIONS": "1024",
			"MANAGER_READ_HEADER_TIMEOUT": "10s", "MANAGER_READ_TIMEOUT": "2m",
			"MANAGER_WRITE_TIMEOUT": "15m", "MANAGER_IDLE_TIMEOUT": "2m",
			"MANAGER_SHUTDOWN_TIMEOUT": "30s", "CTRL_REQUEST_TIMEOUT": "60s",
			"MANAGER_SESSION_MAX_ENTRIES": "10000",
			"MANAGER_CACHE_MAX_ENTRIES":   "1000", "MANAGER_CACHE_MAX_BYTES": "64m",
			"MANAGER_CACHE_TTL": "5m", "MANAGER_SUPPORT_COMMAND": "/usr/local/bin/support",
			"MANAGER_SUPPORT_TEMP_DIR": "/tmp/neuvector-support", "MANAGER_SUPPORT_TIMEOUT": "10m",
			"MANAGER_SUPPORT_MAX_FILE_BYTES": "64m", "MANAGER_SUPPORT_MAX_CONCURRENT": "2",
			"MANAGER_SSO_STATE_TTL": "5m", "MANAGER_SSO_MAX_PENDING": "1024",
		}[name])
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Server.Address != "0.0.0.0:8443" || cfg.Server.MaxHeaderBytes != 32*1024 {
		t.Fatalf("unexpected server defaults: %+v", cfg.Server)
	}
	if cfg.Server.MaxBodyBytes != 50*1024*1024 || cfg.Server.MaxConnections != 1024 || cfg.Server.ReadTimeout != 2*time.Minute || cfg.Server.WriteTimeout != 15*time.Minute || cfg.Server.IdleTimeout != 2*time.Minute {
		t.Fatalf("unexpected server limits: %+v", cfg.Server)
	}
	if cfg.Server.Development {
		t.Fatal("development mode enabled by default")
	}
	if got := cfg.Controller.BaseURL.String(); got != "https://127.0.0.1:10443/v1" {
		t.Fatalf("controller URL = %q", got)
	}
	if cfg.Cache.MaxEntries != 1000 || cfg.Cache.MaxBytes != 64*1024*1024 || cfg.Cache.TTL != 5*time.Minute {
		t.Fatalf("unexpected cache defaults: %+v", cfg.Cache)
	}
	if cfg.Support.Command != "/usr/local/bin/support" || cfg.Support.TempDir != "/tmp/neuvector-support" || cfg.Support.Timeout != 10*time.Minute || cfg.Support.MaxFileBytes != 64*1024*1024 || cfg.Support.MaxConcurrent != 2 {
		t.Fatalf("unexpected support defaults: %+v", cfg.Support)
	}
	if cfg.SSO.PublicURL != nil || cfg.SSO.TTL != 5*time.Minute || cfg.SSO.MaxEntries != 1024 {
		t.Fatalf("unexpected SSO defaults: %+v", cfg.SSO)
	}
}

func TestLoadDevelopmentMode(t *testing.T) {
	t.Setenv("IS_DEV", "true")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.Server.Development {
		t.Fatal("development mode is disabled")
	}
}

func TestLoadSSOPublicURL(t *testing.T) {
	t.Setenv("MANAGER_PUBLIC_URL", "https://manager.example:8443/")
	t.Setenv("MANAGER_SSO_STATE_TTL", "2m")
	t.Setenv("MANAGER_SSO_MAX_PENDING", "64")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.SSO.PublicURL == nil || cfg.SSO.PublicURL.String() != "https://manager.example:8443" || cfg.SSO.TTL != 2*time.Minute || cfg.SSO.MaxEntries != 64 {
		t.Fatalf("unexpected SSO config: %+v", cfg.SSO)
	}
}

func TestLoadReportsMultipleErrors(t *testing.T) {
	t.Setenv("MANAGER_SERVER_PORT", "nope")
	t.Setenv("CTRL_SERVER_PORT", "70000")
	t.Setenv("MANAGER_SSL", "sometimes")
	t.Setenv("HTTP_MAX_HEADER_LENGTH", "0")
	t.Setenv("MANAGER_MAX_BODY_BYTES", "0")
	t.Setenv("MANAGER_MAX_CONNECTIONS", "0")
	t.Setenv("MANAGER_READ_TIMEOUT", "0s")
	t.Setenv("MANAGER_SUPPORT_COMMAND", "relative/support")
	t.Setenv("MANAGER_SUPPORT_TEMP_DIR", "relative/output")
	t.Setenv("MANAGER_SUPPORT_TIMEOUT", "0s")
	t.Setenv("MANAGER_SUPPORT_MAX_FILE_BYTES", "0")
	t.Setenv("MANAGER_SUPPORT_MAX_CONCURRENT", "0")
	t.Setenv("MANAGER_PUBLIC_URL", "http://manager.example/path")
	t.Setenv("MANAGER_SSO_STATE_TTL", "0s")
	t.Setenv("MANAGER_SSO_MAX_PENDING", "0")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() error = nil, want aggregated validation error")
	}
	for _, want := range []string{"MANAGER_SERVER_PORT", "CTRL_SERVER_PORT", "MANAGER_SSL", "HTTP_MAX_HEADER_LENGTH", "MANAGER_MAX_BODY_BYTES", "MANAGER_MAX_CONNECTIONS", "MANAGER_READ_TIMEOUT", "MANAGER_SUPPORT_COMMAND", "MANAGER_SUPPORT_TEMP_DIR", "MANAGER_SUPPORT_TIMEOUT", "MANAGER_SUPPORT_MAX_FILE_BYTES", "MANAGER_SUPPORT_MAX_CONCURRENT", "MANAGER_PUBLIC_URL", "MANAGER_SSO_STATE_TTL", "MANAGER_SSO_MAX_PENDING"} {
		if !contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

func TestNormalizePrefix(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
		ok    bool
	}{
		{"", "", true}, {" manager ", "/manager", true}, {"/manager/", "/manager", true},
		{"/a//b", "", false}, {"/../admin", "", false}, {"/admin?q=1", "", false},
	} {
		got, err := normalizePrefix(test.input)
		if (err == nil) != test.ok || got != test.want {
			t.Errorf("normalizePrefix(%q) = %q, %v; want %q, ok=%v", test.input, got, err, test.want, test.ok)
		}
	}
}

func contains(value, part string) bool {
	for i := 0; i+len(part) <= len(value); i++ {
		if value[i:i+len(part)] == part {
			return true
		}
	}
	return false
}
