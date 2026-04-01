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
	// Create a temp config file
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, ".nullbore")
	os.MkdirAll(configDir, 0755)

	configContent := `# NullBore config
server = "https://api.nullbore.com"
api_key = "nbk_test_abc123"
default_ttl = "2h"
`
	os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(configContent), 0644)

	// Override HOME so Load() finds our temp config
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	// Clear env overrides
	os.Unsetenv("NULLBORE_SERVER")
	os.Unsetenv("NULLBORE_API_KEY")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
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
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() should not error on missing file: %v", err)
	}

	// Should return defaults
	if cfg.Server != "http://localhost:8443" {
		t.Errorf("Server = %q, want default", cfg.Server)
	}
	if cfg.DefaultTTL != "1h" {
		t.Errorf("DefaultTTL = %q, want default", cfg.DefaultTTL)
	}
}

func TestLoadQuotedValues(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, ".nullbore")
	os.MkdirAll(configDir, 0755)

	// Test single-quoted values
	configContent := `server = 'https://single.quoted.com'
api_key = 'nbk_single'
`
	os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(configContent), 0644)

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	os.Unsetenv("NULLBORE_SERVER")
	os.Unsetenv("NULLBORE_API_KEY")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
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
	configDir := filepath.Join(tmpDir, ".nullbore")
	os.MkdirAll(configDir, 0755)

	configContent := `# This is a comment
server = "https://example.com"
# Another comment
  # Indented comment
api_key = "nbk_test"
bad_line_no_equals
`
	os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(configContent), 0644)

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	os.Unsetenv("NULLBORE_SERVER")
	os.Unsetenv("NULLBORE_API_KEY")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Server != "https://example.com" {
		t.Errorf("Server = %q, want %q", cfg.Server, "https://example.com")
	}
	if cfg.APIKey != "nbk_test" {
		t.Errorf("APIKey = %q, want %q", cfg.APIKey, "nbk_test")
	}
}

func TestTunnelConfigParsing(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".nullbore", "config.toml")
	os.MkdirAll(filepath.Dir(cfgPath), 0755)
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

	// Override home dir
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", dir)
	defer os.Setenv("HOME", origHome)

	cfg, err := Load()
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

	// First tunnel
	if cfg.Tunnels[0].Port != 3000 || cfg.Tunnels[0].Name != "my-api" || cfg.Tunnels[0].TTL != "2h" {
		t.Errorf("tunnel[0] = %+v", cfg.Tunnels[0])
	}

	// Second tunnel
	if cfg.Tunnels[1].Port != 5432 || cfg.Tunnels[1].Name != "postgres" || cfg.Tunnels[1].Subdomain != "db" || !cfg.Tunnels[1].IdleTTL {
		t.Errorf("tunnel[1] = %+v", cfg.Tunnels[1])
	}

	// Third tunnel (minimal — just port)
	if cfg.Tunnels[2].Port != 8080 {
		t.Errorf("tunnel[2] = %+v", cfg.Tunnels[2])
	}
}

func TestNoTunnels(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".nullbore", "config.toml")
	os.MkdirAll(filepath.Dir(cfgPath), 0755)
	os.WriteFile(cfgPath, []byte(`
server = "https://tunnel.nullbore.com"
api_key = "nbk_test"
`), 0600)

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", dir)
	defer os.Setenv("HOME", origHome)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if len(cfg.Tunnels) != 0 {
		t.Errorf("tunnels = %d, want 0", len(cfg.Tunnels))
	}
}
