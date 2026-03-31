package daemon

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/nullbore/nullbore-client/internal/client"
	"github.com/nullbore/nullbore-client/internal/config"
	"github.com/nullbore/nullbore-client/internal/tunnel"
)

// TunnelConfig mirrors the dashboard's tunnel config.
type TunnelConfig struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	LocalPort int    `json:"local_port"`
	LocalHost string `json:"local_host,omitempty"` // target host (for Docker/network use, default localhost)
	Subdomain string `json:"subdomain"`
	TTL       string `json:"ttl"`
	IdleTTL   bool   `json:"idle_ttl"`
	Active    bool   `json:"active"`
	TunnelID  string `json:"tunnel_id,omitempty"`
}

// Daemon manages tunnel connections based on dashboard configs.
type Daemon struct {
	cfg       *config.Config
	dashToken string
	httpClient *http.Client

	mu       sync.Mutex
	managers map[string]*tunnel.Manager // config ID → running manager
	active   map[string]TunnelConfig    // config ID → config
	stopChs  map[string]chan struct{}    // config ID → stop signal
}

// New creates a new daemon.
func New(cfg *config.Config) *Daemon {
	return &Daemon{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: 15 * time.Second},
		managers:   make(map[string]*tunnel.Manager),
		active:     make(map[string]TunnelConfig),
		stopChs:    make(map[string]chan struct{}),
	}
}

// Run authenticates and starts the config sync loop.
func (d *Daemon) Run() error {
	log.Printf("nullbore daemon starting")
	log.Printf("dashboard: %s", d.cfg.DashboardURL())

	if err := d.authenticate(); err != nil {
		return fmt.Errorf("authentication failed: %w", err)
	}
	log.Printf("authenticated with dashboard")

	// Initial sync
	if err := d.sync(); err != nil {
		return fmt.Errorf("initial sync failed: %w", err)
	}

	// Poll for changes every 5s
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		if err := d.sync(); err != nil {
			log.Printf("sync error: %v (will retry)", err)
			// Re-auth on persistent failures
			if strings.Contains(err.Error(), "401") {
				d.authenticate()
			}
		}
	}
	return nil
}

// authenticate exchanges API key for a dashboard session token.
func (d *Daemon) authenticate() error {
	body := fmt.Sprintf(`{"api_key":"%s"}`, d.cfg.Token())
	resp, err := d.httpClient.Post(
		d.cfg.DashboardURL()+"/api/auth/token",
		"application/json",
		strings.NewReader(body),
	)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Token  string `json:"token"`
		UserID string `json:"user_id"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	d.dashToken = result.Token
	return nil
}

// sync fetches configs from dashboard and reconciles.
func (d *Daemon) sync() error {
	req, _ := http.NewRequest("GET", d.cfg.DashboardURL()+"/api/daemon/configs", nil)
	req.Header.Set("Authorization", "Bearer "+d.dashToken)

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		return fmt.Errorf("401 unauthorized")
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var result struct {
		Configs      []TunnelConfig `json:"configs"`
		TunnelServer string         `json:"tunnel_server"`
		TunnelAPIKey string         `json:"tunnel_api_key"`
	}
	json.NewDecoder(resp.Body).Decode(&result)

	// Override server URL if dashboard provides one
	tunnelServer := result.TunnelServer
	if tunnelServer == "" {
		tunnelServer = d.cfg.ServerURL()
	}

	// Use tunnel-specific API key if dashboard provides one, otherwise fall back to daemon key
	tunnelAPIKey := result.TunnelAPIKey
	if tunnelAPIKey == "" {
		tunnelAPIKey = d.cfg.Token()
	}

	d.reconcile(result.Configs, tunnelServer, tunnelAPIKey)
	return nil
}

// reconcile converges running tunnels toward desired state.
func (d *Daemon) reconcile(configs []TunnelConfig, tunnelServer, tunnelAPIKey string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	desired := make(map[string]TunnelConfig)
	for _, c := range configs {
		desired[c.ID] = c
	}

	// Stop tunnels that should no longer be active
	for id, prev := range d.active {
		curr, exists := desired[id]
		if !exists || !curr.Active {
			log.Printf("[daemon] stopping: %s (port %d)", prev.Name, prev.LocalPort)
			if mgr, ok := d.managers[id]; ok {
				mgr.Close()
				delete(d.managers, id)
			}
			delete(d.active, id)
		}
	}

	// Start tunnels that should be active
	for id, c := range desired {
		if !c.Active {
			continue
		}

		prev, running := d.active[id]
		if running && prev.LocalPort == c.LocalPort && prev.Subdomain == c.Subdomain {
			continue // already running, no change
		}

		// Config changed — stop old if running
		if running {
			if mgr, ok := d.managers[id]; ok {
				mgr.Close()
				delete(d.managers, id)
			}
		}

		log.Printf("[daemon] starting: %s (port %d → %s.tunnel.nullbore.com)",
			c.Name, c.LocalPort, c.Subdomain)

		// Create a config pointing at the tunnel server.
		// ExplicitKey bypasses env var override so the tunnel server key
		// is used instead of the dashboard API key.
		tunnelCfg := &config.Config{
			Server:      tunnelServer,
			ExplicitKey: tunnelAPIKey,
			DefaultTTL:  c.TTL,
		}
		apiClient := client.New(tunnelCfg)

		spec := tunnel.TunnelSpec{
			Port: c.LocalPort,
			Host: c.LocalHost,
			Name: c.Subdomain,
			TTL:  c.TTL,
		}

		mgr := tunnel.NewManager(tunnelCfg, apiClient)

		// Open the tunnel
		at, err := mgr.OpenTunnel(spec)
		if err != nil {
			log.Printf("[daemon] failed to open %s: %v", c.Name, err)
			continue
		}

		log.Printf("[daemon] ✓ %s → %s", c.Name, at.PublicURL)

		// Start reconnect loop in background
		go mgr.Run()

		d.managers[id] = mgr
		d.active[id] = c
	}
}

// Stop shuts down all tunnels.
func (d *Daemon) Stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for id, mgr := range d.managers {
		mgr.Close()
		delete(d.managers, id)
	}
	d.active = make(map[string]TunnelConfig)
}

// ActiveCount returns the number of running tunnels.
func (d *Daemon) ActiveCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.active)
}
