package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaults(t *testing.T) {
	// Ensure env vars don't interfere
	os.Unsetenv("NULLBORE_SERVER")
	os.Unsetenv("NULLBORE_API_KEY")

	cfg := &Config{
		Server:     "http://localhost:8443",
		DefaultTTL: "1h",
	}

	if got := cfg.ServerURL(); got != "http://localhost:8443" {
		t.Errorf("ServerURL() = %q, want %q", got, "http://localhost:8443")
	}
	if got := cfg.Token(); got != "" {
		t.Errorf("Token() = %q, want empty", got)
	}
}

func TestEnvOverrides(t *testing.T) {
	cfg := &Config{
		Server: "http://localhost:8443",
		APIKey: "file_key",
	}

	os.Setenv("NULLBORE_SERVER", "http://override:9090")
	os.Setenv("NULLBORE_API_KEY", "env_key")
	defer os.Unsetenv("NULLBORE_SERVER")
	defer os.Unsetenv("NULLBORE_API_KEY")

	if got := cfg.ServerURL(); got != "http://override:9090" {
		t.Errorf("ServerURL() = %q, want env override", got)
	}
	if got := cfg.Token(); got != "env_key" {
		t.Errorf("Token() = %q, want env override", got)
	}
}

func TestLoadFromFile(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.toml")

	configContent := `# NullBore config
server = "https://api.nullbore.com"
api_key = "nbk_test_abc123"
default_ttl = "2h"
`
	os.WriteFile(cfgPath, []byte(configContent), 0644)

	os.Unsetenv("NULLBORE_SERVER")
	os.Unsetenv("NULLBORE_API_KEY")

	cfg, err := LoadFrom(cfgPath)
	if err != nil {
		t.Fatalf("LoadFrom() error: %v", err)
	}

	if cfg.Server != "https://api.nullbore.com" {
		t.Errorf("Server = %q, want %q", cfg.Server, "https://api.nullbore.com")
	}
	if cfg.APIKey != "nbk_test_abc123" {
		t.Errorf("APIKey = %q, want %q", cfg.APIKey, "nbk_test_abc123")
	}
	if cfg.DefaultTTL != "2h" {
		t.Errorf("DefaultTTL = %q, want %q", cfg.DefaultTTL, "2h")
	}
}

func TestLoadNoFile(t *testing.T) {
	cfg, err := LoadFrom("/nonexistent/path/config.toml")
	if err != nil {
		t.Fatalf("LoadFrom() should not error on missing file: %v", err)
	}

	if cfg.Server != "http://localhost:8443" {
		t.Errorf("Server = %q, want default", cfg.Server)
	}
	if cfg.DefaultTTL != "1h" {
		t.Errorf("DefaultTTL = %q, want default", cfg.DefaultTTL)
	}
}

func TestLoadQuotedValues(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.toml")

	configContent := `server = 'https://single.quoted.com'
api_key = 'nbk_single'
`
	os.WriteFile(cfgPath, []byte(configContent), 0644)

	os.Unsetenv("NULLBORE_SERVER")
	os.Unsetenv("NULLBORE_API_KEY")

	cfg, err := LoadFrom(cfgPath)
	if err != nil {
		t.Fatalf("LoadFrom() error: %v", err)
	}

	if cfg.Server != "https://single.quoted.com" {
		t.Errorf("Server = %q, want single-quoted value stripped", cfg.Server)
	}
	if cfg.APIKey != "nbk_single" {
		t.Errorf("APIKey = %q, want single-quoted value stripped", cfg.APIKey)
	}
}

func TestLoadSkipsComments(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.toml")

	configContent := `# This is a comment
server = "https://example.com"
# Another comment
  # Indented comment
api_key = "nbk_test"
bad_line_no_equals
`
	os.WriteFile(cfgPath, []byte(configContent), 0644)

	os.Unsetenv("NULLBORE_SERVER")
	os.Unsetenv("NULLBORE_API_KEY")

	cfg, err := LoadFrom(cfgPath)
	if err != nil {
		t.Fatalf("LoadFrom() error: %v", err)
	}

	if cfg.Server != "https://example.com" {
		t.Errorf("Server = %q, want %q", cfg.Server, "https://example.com")
	}
	if cfg.APIKey != "nbk_test" {
		t.Errorf("APIKey = %q, want %q", cfg.APIKey, "nbk_test")
	}
}

func TestTunnelConfigParsing(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.toml")

	os.WriteFile(cfgPath, []byte(`
server = "https://tunnel.nullbore.com"
api_key = "nbk_test123"

[[tunnels]]
port = 3000
name = "my-api"
ttl = "2h"

[[tunnels]]
port = 5432
name = "postgres"
subdomain = "db"
idle_ttl = true

[[tunnels]]
port = 8080
`), 0600)

	cfg, err := LoadFrom(cfgPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if cfg.Server != "https://tunnel.nullbore.com" {
		t.Errorf("server = %q", cfg.Server)
	}
	if cfg.APIKey != "nbk_test123" {
		t.Errorf("api_key = %q", cfg.APIKey)
	}

	if len(cfg.Tunnels) != 3 {
		t.Fatalf("tunnels = %d, want 3", len(cfg.Tunnels))
	}

	if cfg.Tunnels[0].Port != 3000 || cfg.Tunnels[0].Name != "my-api" || cfg.Tunnels[0].TTL != "2h" {
		t.Errorf("tunnel[0] = %+v", cfg.Tunnels[0])
	}
	if cfg.Tunnels[1].Port != 5432 || cfg.Tunnels[1].Name != "postgres" || cfg.Tunnels[1].Subdomain != "db" || !cfg.Tunnels[1].IdleTTL {
		t.Errorf("tunnel[1] = %+v", cfg.Tunnels[1])
	}
	if cfg.Tunnels[2].Port != 8080 {
		t.Errorf("tunnel[2] = %+v", cfg.Tunnels[2])
	}
}

func TestNoTunnels(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.toml")

	os.WriteFile(cfgPath, []byte(`
server = "https://tunnel.nullbore.com"
api_key = "nbk_test"
`), 0600)

	cfg, err := LoadFrom(cfgPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if len(cfg.Tunnels) != 0 {
		t.Errorf("tunnels = %d, want 0", len(cfg.Tunnels))
	}
}

func TestXDGMigration(t *testing.T) {
	tmpDir := t.TempDir()

	// Set up environment to use tmpDir as HOME
	origHome := os.Getenv("HOME")
	origXDG := os.Getenv("XDG_CONFIG_HOME")
	os.Setenv("HOME", tmpDir)
	os.Unsetenv("XDG_CONFIG_HOME")
	defer func() {
		os.Setenv("HOME", origHome)
		if origXDG != "" {
			os.Setenv("XDG_CONFIG_HOME", origXDG)
		} else {
			os.Unsetenv("XDG_CONFIG_HOME")
		}
	}()

	// Create legacy config
	legacyDir := filepath.Join(tmpDir, ".nullbore")
	os.MkdirAll(legacyDir, 0755)
	os.WriteFile(filepath.Join(legacyDir, "config.toml"), []byte(`
server = "https://test.nullbore.com"
api_key = "nbk_migrate_test"
`), 0600)

	// Load should trigger migration
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Server != "https://test.nullbore.com" {
		t.Errorf("Server = %q, want migrated value", cfg.Server)
	}
	if cfg.APIKey != "nbk_migrate_test" {
		t.Errorf("APIKey = %q, want migrated value", cfg.APIKey)
	}

	// XDG path should now exist
	xdgPath := filepath.Join(tmpDir, ".config", "nullbore", "config.toml")
	if _, err := os.Stat(xdgPath); os.IsNotExist(err) {
		t.Error("XDG config file should exist after migration")
	}

	// Legacy dir should be renamed
	if _, err := os.Stat(legacyDir); !os.IsNotExist(err) {
		t.Error("Legacy dir should be renamed after migration")
	}
	if _, err := os.Stat(filepath.Join(tmpDir, ".nullbore.migrated")); os.IsNotExist(err) {
		t.Error("Backup dir should exist after migration")
	}
}

func TestConfigDir(t *testing.T) {
	origXDG := os.Getenv("XDG_CONFIG_HOME")
	defer func() {
		if origXDG != "" {
			os.Setenv("XDG_CONFIG_HOME", origXDG)
		} else {
			os.Unsetenv("XDG_CONFIG_HOME")
		}
	}()

	// With XDG_CONFIG_HOME set
	os.Setenv("XDG_CONFIG_HOME", "/custom/config")
	if got := ConfigDir(); got != "/custom/config/nullbore" {
		t.Errorf("ConfigDir() with XDG = %q, want /custom/config/nullbore", got)
	}

	// Without XDG_CONFIG_HOME (falls back to ~/.config)
	os.Unsetenv("XDG_CONFIG_HOME")
	dir := ConfigDir()
	if dir == "" {
		t.Skip("no HOME set")
	}
	if !filepath.IsAbs(dir) {
		t.Errorf("ConfigDir() = %q, want absolute path", dir)
	}
}
