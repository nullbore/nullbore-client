package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// isTopLevelKey returns true if the key is a known top-level config key.
func isTopLevelKey(key string) bool {
	switch key {
	case "server", "api_key", "default_ttl", "dashboard", "tls_skip_verify", "device_id", "device_name":
		return true
	}
	return false
}

// TunnelSpec defines a persistent tunnel in the config file.
type TunnelSpec struct {
	Port      int    `json:"port"`
	Name      string `json:"name,omitempty"`
	Subdomain string `json:"subdomain,omitempty"`
	Host      string `json:"host,omitempty"` // local target host (default: localhost)
	TTL       string `json:"ttl,omitempty"`
	IdleTTL   bool   `json:"idle_ttl,omitempty"`
}

// Config holds client configuration.
type Config struct {
	Server        string `json:"server"`
	Dashboard     string `json:"dashboard"`
	APIKey        string `json:"api_key"`
	DefaultTTL    string `json:"default_ttl"`
	TLSSkipVerify bool   `json:"tls_skip_verify"`
	DeviceID      string `json:"device_id"`
	DeviceName    string `json:"device_name"`

	// Tunnels defines persistent tunnels managed by the daemon.
	Tunnels []TunnelSpec `json:"tunnels,omitempty"`

	// ExplicitKey, if set, takes precedence over APIKey and env vars.
	// Used by the daemon to pass tunnel-server-specific API keys.
	ExplicitKey string `json:"-"`

	// configPath is the path to the config file (for saving device_id)
	configPath string
}

// ConfigDir returns the NullBore config directory path, following XDG conventions.
// Priority: $XDG_CONFIG_HOME/nullbore → ~/.config/nullbore → ~/.nullbore (legacy)
func ConfigDir() string {
	// Check XDG_CONFIG_HOME first
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "nullbore")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	// Default XDG path
	return filepath.Join(home, ".config", "nullbore")
}

// resolveConfigPath finds the config file, migrating from legacy path if needed.
func resolveConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	xdgDir := ConfigDir()
	xdgPath := filepath.Join(xdgDir, "config.toml")
	legacyPath := filepath.Join(home, ".nullbore", "config.toml")

	// If XDG path exists, use it
	if _, err := os.Stat(xdgPath); err == nil {
		return xdgPath
	}

	// If legacy path exists, migrate it
	if _, err := os.Stat(legacyPath); err == nil {
		// Create XDG directory
		if mkErr := os.MkdirAll(xdgDir, 0700); mkErr == nil {
			// Copy file to new location
			if data, readErr := os.ReadFile(legacyPath); readErr == nil {
				if writeErr := os.WriteFile(xdgPath, data, 0600); writeErr == nil {
					// Rename old dir to signal migration
					backupPath := filepath.Join(home, ".nullbore.migrated")
					os.Rename(filepath.Join(home, ".nullbore"), backupPath)
					fmt.Fprintf(os.Stderr, "Config migrated: ~/.nullbore/ → %s\n", xdgDir)
					fmt.Fprintf(os.Stderr, "Old config backed up to ~/.nullbore.migrated/\n")
					return xdgPath
				}
			}
		}
		// Migration failed — fall back to legacy
		return legacyPath
	}

	// Neither exists — use XDG path (will be created on first write)
	return xdgPath
}

// defaultConfigTemplate is written when no config file exists yet.
const defaultConfigTemplate = `# NullBore client configuration
# Docs: https://nullbore.com/docs/configuration

# Tunnel server URL
server = "https://tunnel.nullbore.com"

# Your API key (get one at https://nullbore.com/dashboard)
# api_key = "nbk_..."

# Default TTL for tunnels (e.g. 30m, 1h, 2h, 24h, 168h)
# default_ttl = "1h"

# Dashboard URL (for daemon polling mode)
# dashboard = "https://nullbore.com"

# Skip TLS certificate verification (not recommended)
# tls_skip_verify = false

# Device name (auto-filled from hostname on first connect)
# device_name = ""

# --- Persistent tunnels (used by 'nullbore daemon') ---
# Uncomment and configure to keep tunnels open automatically.
#
# [[tunnels]]
# port = 3000
# name = "my-api"
# subdomain = "my-api"
# ttl = "1h"
# idle_ttl = false
# host = "localhost"
#
# [[tunnels]]
# port = 8080
# name = "web"
# ttl = "2h"
`

// Load reads config from the NullBore config file (XDG or legacy path).
func Load() (*Config, error) {
	path := resolveConfigPath()
	return LoadFrom(path)
}

// LoadFrom reads config from a specific file path.
// If path is empty or the file doesn't exist, returns defaults.
func LoadFrom(path string) (*Config, error) {
	cfg := &Config{
		Server:     "http://localhost:8443",
		DefaultTTL: "1h",
	}

	if path == "" {
		return cfg, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Write default config template
			dir := filepath.Dir(path)
			if mkErr := os.MkdirAll(dir, 0700); mkErr == nil {
				if wErr := os.WriteFile(path, []byte(defaultConfigTemplate), 0600); wErr == nil {
					cfg.configPath = path
					fmt.Fprintf(os.Stderr, "Created config: %s\n", path)
					fmt.Fprintf(os.Stderr, "Edit it to add your API key, then run: nullbore daemon\n")
				}
			}
			return cfg, nil
		}
		return nil, fmt.Errorf("reading config: %w", err)
	}

	// Parse TOML-ish config: key=value at top level, [[tunnels]] for tunnel blocks
	var currentTunnel *TunnelSpec
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Handle [[tunnels]] section header
		if line == "[[tunnels]]" {
			// Save previous tunnel if any
			if currentTunnel != nil && currentTunnel.Port > 0 {
				cfg.Tunnels = append(cfg.Tunnels, *currentTunnel)
			}
			currentTunnel = &TunnelSpec{}
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.Trim(strings.TrimSpace(parts[1]), `"'`)

		// If we're inside a [[tunnels]] block, parse tunnel keys
		if currentTunnel != nil {
			parsed := true
			switch key {
			case "port":
				p, _ := strconv.Atoi(val)
				currentTunnel.Port = p
			case "name":
				currentTunnel.Name = val
			case "subdomain":
				currentTunnel.Subdomain = val
			case "host":
				currentTunnel.Host = val
			case "ttl":
				currentTunnel.TTL = val
			case "idle_ttl":
				currentTunnel.IdleTTL = val == "true" || val == "1"
			default:
				parsed = false
			}
			if parsed {
				continue
			}
			// Not a tunnel key — exit the block and fall through to top-level
			if currentTunnel.Port > 0 {
				cfg.Tunnels = append(cfg.Tunnels, *currentTunnel)
			}
			currentTunnel = nil
		}

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
		case "device_name":
			cfg.DeviceName = val
		}
	}

	// Save last tunnel block if any
	if currentTunnel != nil && currentTunnel.Port > 0 {
		cfg.Tunnels = append(cfg.Tunnels, *currentTunnel)
	}

	cfg.configPath = path

	// Auto-generate device_id if missing
	if cfg.DeviceID == "" {
		cfg.DeviceID = generateDeviceID()
		cfg.appendToFile("device_id", cfg.DeviceID)
	}

	// Auto-fill device_name from hostname if missing
	if cfg.DeviceName == "" {
		if hostname, err := os.Hostname(); err == nil && hostname != "" {
			cfg.DeviceName = hostname
			cfg.appendToFile("device_name", cfg.DeviceName)
		}
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
