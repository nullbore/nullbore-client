package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config holds client configuration.
type Config struct {
	Server        string `json:"server"`
	APIKey        string `json:"api_key"`
	DefaultTTL    string `json:"default_ttl"`
	TLSSkipVerify bool   `json:"tls_skip_verify"`
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
		case "tls_skip_verify":
			cfg.TLSSkipVerify = val == "true" || val == "1" || val == "yes"
		}
	}

	return cfg, nil
}

// ServerURL returns the base server URL, with env override.
func (c *Config) ServerURL() string {
	if v := os.Getenv("NULLBORE_SERVER"); v != "" {
		return v
	}
	return c.Server
}

// Token returns the API key, with env override.
func (c *Config) Token() string {
	if v := os.Getenv("NULLBORE_API_KEY"); v != "" {
		return v
	}
	return c.APIKey
}

// InsecureSkipVerify returns whether to skip TLS verification, with env override.
func (c *Config) InsecureSkipVerify() bool {
	if v := os.Getenv("NULLBORE_TLS_SKIP_VERIFY"); v == "1" || v == "true" {
		return true
	}
	return c.TLSSkipVerify
}
