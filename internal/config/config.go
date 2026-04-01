package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config holds client configuration.
type Config struct {
	Server        string `json:"server"`
	Dashboard     string `json:"dashboard"`
	APIKey        string `json:"api_key"`
	DefaultTTL    string `json:"default_ttl"`
	TLSSkipVerify bool   `json:"tls_skip_verify"`
	DeviceID      string `json:"device_id"`

	// ExplicitKey, if set, takes precedence over APIKey and env vars.
	// Used by the daemon to pass tunnel-server-specific API keys.
	ExplicitKey string `json:"-"`

	// configPath is the path to the config file (for saving device_id)
	configPath string
}

// Load reads config from ~/.nullbore/config.toml (simple key=value parsing).
func Load() (*Config, error) {
	cfg := &Config{
		Server:     "http://localhost:8443",
		DefaultTTL: "1h",
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return cfg, nil
	}

	path := filepath.Join(home, ".nullbore", "config.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, fmt.Errorf("reading config: %w", err)
	}

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.Trim(strings.TrimSpace(parts[1]), `"'`)

		switch key {
		case "server":
			cfg.Server = val
		case "api_key":
			cfg.APIKey = val
		case "default_ttl":
			cfg.DefaultTTL = val
		case "dashboard":
			cfg.Dashboard = val
		case "tls_skip_verify":
			cfg.TLSSkipVerify = val == "true" || val == "1" || val == "yes"
		case "device_id":
			cfg.DeviceID = val
		}
	}

	cfg.configPath = path

	// Auto-generate device_id if missing
	if cfg.DeviceID == "" {
		cfg.DeviceID = generateDeviceID()
		cfg.appendToFile("device_id", cfg.DeviceID)
	}

	return cfg, nil
}

// generateDeviceID creates a random 16-byte hex device identifier.
func generateDeviceID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// appendToFile appends a key=value line to the config file.
func (c *Config) appendToFile(key, value string) {
	if c.configPath == "" {
		return
	}
	f, err := os.OpenFile(c.configPath, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "\n# Auto-generated device identifier\n%s = \"%s\"\n", key, value)
}

// ServerURL returns the base server URL, with env override.
func (c *Config) ServerURL() string {
	if v := os.Getenv("NULLBORE_SERVER"); v != "" {
		return v
	}
	return c.Server
}

// Token returns the API key. ExplicitKey takes highest precedence,
// then NULLBORE_API_KEY env var, then the config file value.
func (c *Config) Token() string {
	if c.ExplicitKey != "" {
		return c.ExplicitKey
	}
	if v := os.Getenv("NULLBORE_API_KEY"); v != "" {
		return v
	}
	return c.APIKey
}

// DashboardURL returns the dashboard URL, with env override.
func (c *Config) DashboardURL() string {
	if v := os.Getenv("NULLBORE_DASHBOARD"); v != "" {
		return v
	}
	if c.Dashboard != "" {
		return c.Dashboard
	}
	return "https://nullbore.com"
}

// InsecureSkipVerify returns whether to skip TLS verification, with env override.
func (c *Config) InsecureSkipVerify() bool {
	if v := os.Getenv("NULLBORE_TLS_SKIP_VERIFY"); v == "1" || v == "true" {
		return true
	}
	return c.TLSSkipVerify
}
