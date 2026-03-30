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
