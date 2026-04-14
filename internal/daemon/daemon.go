package daemon

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/nullbore/nullbore-client/internal/client"
	"github.com/nullbore/nullbore-client/internal/debug"
	"github.com/nullbore/nullbore-client/internal/config"
	"github.com/nullbore/nullbore-client/internal/tunnel"
	"github.com/nullbore/nullbore-client/internal/update"
)

// Daemon manages persistent tunnel connections from config.toml or the dashboard.
type Daemon struct {
	cfg    *config.Config
	client *client.Client

	mu       sync.Mutex
	managers map[string]*tunnel.Manager // "port:name" → running manager
	specs    map[string]config.TunnelSpec

	// Dashboard mode
	dashMode   bool
	dashURL    string
	dashClient *http.Client
	dashToken  string // JWT session token from /api/auth/token exchange

	// Version for update checks
	version string
}

// New creates a new daemon.
func New(cfg *config.Config, version string) *Daemon {
	return &Daemon{
		cfg:      cfg,
		client:   client.New(cfg),
		managers: make(map[string]*tunnel.Manager),
		specs:    make(map[string]config.TunnelSpec),
		version:  version,
	}
}

// startUpdateChecker runs a background goroutine that checks for updates periodically.
func (d *Daemon) startUpdateChecker() {
	go func() {
		// Check once at startup (after a short delay)
		time.Sleep(5 * time.Second)
		d.checkUpdate()

		// Then every 6 hours
		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			d.checkUpdate()
		}
	}()
}

func (d *Daemon) checkUpdate() {
	rel, err := update.CheckLatest()
	if err != nil {
		return // silently ignore
	}
	if update.IsNewer(d.version, rel.TagName) {
		log.Printf("⬆ Update available: %s → %s", d.version, rel.TagName)
		log.Printf("  Run: nullbore update")
		log.Printf("  Or:  curl -fsSL https://nullbore.com/install.sh | sh")
	}
}

// specKey returns a stable identifier for a tunnel spec.
func specKey(s config.TunnelSpec) string {
	name := s.Name
	if name == "" {
		name = s.Subdomain
	}
	return name + ":" + fmt.Sprintf("%d", s.Port)
}

// DashboardConfig is the response from /api/daemon/configs.
type DashboardConfig struct {
	Configs      []DashboardTunnelConfig `json:"configs"`
	TunnelServer string                  `json:"tunnel_server"`
	TunnelAPIKey string                  `json:"tunnel_api_key,omitempty"`
}

// DashboardTunnelConfig is a tunnel config from the dashboard.
type DashboardTunnelConfig struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	LocalPort int    `json:"local_port"`
	Subdomain string `json:"subdomain"`
	TTL       string `json:"ttl"`
	IdleTTL   bool   `json:"idle_ttl"`
	Active    bool   `json:"active"`
	TunnelID  string `json:"tunnel_id,omitempty"`
}

// Run starts the daemon. If no local tunnel config is defined, it falls back
// to polling the dashboard for tunnel configs.
func (d *Daemon) Run() error {
	log.Printf("nullbore daemon starting")

	// If config.toml has [[tunnels]], use local config mode
	if len(d.cfg.Tunnels) > 0 {
		return d.runLocal()
	}

	// Otherwise try dashboard-polling mode
	return d.runDashboard()
}

// runLocal manages tunnels from config.toml.
func (d *Daemon) runLocal() error {
	d.startUpdateChecker()
	log.Printf("server: %s", d.cfg.ServerURL())
	log.Printf("config: %d tunnel(s) defined", len(d.cfg.Tunnels))

	// Initial sync from config
	d.reconcile(d.cfg.Tunnels)

	// Watch config for changes every 10s
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		newCfg, err := config.Load()
		if err != nil {
			log.Printf("config reload error: %v (will retry)", err)
			continue
		}

		// Check if core config changed (server, api_key, etc.) — requires full restart
		if coreConfigChanged(d.cfg, newCfg) {
			log.Printf("core config changed — restarting daemon")
			d.Stop()
			d.cfg = newCfg
			d.client = client.New(newCfg)
			d.reconcile(newCfg.Tunnels)
			continue
		}

		// Check if tunnel specs changed
		if tunnelsChanged(d.cfg.Tunnels, newCfg.Tunnels) {
			log.Printf("config changed: reconciling tunnels")
			d.cfg.Tunnels = newCfg.Tunnels
			d.reconcile(newCfg.Tunnels)
		}
	}
	return nil
}

// runDashboard polls the dashboard for tunnel configs.
func (d *Daemon) runDashboard() error {
	d.startUpdateChecker()
	d.dashURL = d.cfg.DashboardURL()
	d.dashMode = true
	log.Printf("dashboard: %s/dashboard", d.dashURL)

	// Authenticate with dashboard
	// Don't follow redirects — auth failures return 302 → /login
	d.dashClient = &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	if d.cfg.InsecureSkipVerify() {
		d.dashClient.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
	}

	// Exchange API key for a session JWT. The raw API key authenticates
	// successfully (dashboardAuth → GET /api/daemon/configs returns 200)
	// but /api/daemon/configs requires a session JWT to know which user's
	// configs to return. Without this exchange, pollDashboard always gets
	// an empty config list because the dashboard can't resolve a user from
	// a raw API key on the configs endpoint.
	if err := d.exchangeToken(d.dashClient, d.dashURL); err != nil {
		return fmt.Errorf("dashboard token exchange failed: %w", err)
	}
	debug.Printf("authenticated with dashboard (session token obtained)")

	// Initial poll
	d.pollDashboard(d.dashClient, d.dashURL)

	// Poll every 5s
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		d.pollDashboard(d.dashClient, d.dashURL)
	}
	return nil
}

// reportTunnelConnected tells the dashboard about a successful tunnel connection.
func (d *Daemon) reportTunnelConnected(name string, port int, tunnelID, publicURL string) {
	if !d.dashMode || d.dashClient == nil {
		return
	}
	deviceName := d.cfg.DeviceName
	if deviceName == "" {
		deviceName, _ = os.Hostname()
	}
	body := map[string]interface{}{
		"name":        name,
		"local_port":  port,
		"tunnel_id":   tunnelID,
		"public_url":  publicURL,
		"device_name": deviceName,
	}
	data, _ := json.Marshal(body)
	req, err := http.NewRequest("POST", d.dashURL+"/api/daemon/report", bytes.NewReader(data))
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+d.dashboardBearerToken())
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.dashClient.Do(req)
	if err != nil {
		debug.Printf("[dashboard] report error: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		debug.Printf("[dashboard] report failed (%d): %s", resp.StatusCode, string(body))
	}
}

// exchangeToken exchanges the raw API key for a JWT session token via
// POST /api/auth/token. The session token is then used for all subsequent
// dashboard API calls. Without this exchange, /api/daemon/configs returns
// an empty list because it can't resolve the user from a raw API key.
//
// If the exchange fails, it falls back to the raw API key — some dashboard
// deployments (e.g. older self-hosted) may not have /api/auth/token and
// accept raw keys on all endpoints.
func (d *Daemon) exchangeToken(httpClient *http.Client, dashURL string) error {
	apiKey := d.cfg.Token()
	debug.Printf("[dashboard] exchanging API key for session token (key_prefix=%s)", safePrefix(apiKey))

	body := fmt.Sprintf(`{"api_key":%q}`, apiKey)
	req, err := http.NewRequest("POST", dashURL+"/api/auth/token", strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		debug.Printf("[dashboard] token exchange connection failed: %v (falling back to raw key)", err)
		d.dashToken = apiKey
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		// 404 = endpoint not supported (old dashboard); 401 = bad key
		if resp.StatusCode == 404 {
			debug.Printf("[dashboard] /api/auth/token not found — using raw API key (older dashboard?)")
			d.dashToken = apiKey
			return nil
		}
		return fmt.Errorf("token exchange failed (status %d): %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil || result.Token == "" {
		debug.Printf("[dashboard] token exchange returned invalid response — using raw key")
		d.dashToken = apiKey
		return nil
	}

	d.dashToken = result.Token
	debug.Printf("[dashboard] session token obtained (len=%d)", len(d.dashToken))
	return nil
}

// dashboardBearerToken returns the token to use as Bearer in dashboard API calls.
// Prefers the exchanged session JWT; falls back to the raw API key.
func (d *Daemon) dashboardBearerToken() string {
	if d.dashToken != "" {
		return d.dashToken
	}
	return d.cfg.Token()
}

// dashboardAuth validates connectivity + auth against the dashboard by
// hitting /api/daemon/configs. We don't parse the response yet — that's
// pollDashboard's job — we just confirm the server is reachable and
// doesn't reject us.
func (d *Daemon) dashboardAuth(httpClient *http.Client, dashURL string) error {
	url := dashURL + "/api/daemon/configs"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	token := d.cfg.Token()
	debug.Printf("[dashboard] auth url=%s key_prefix=%s key_len=%d", url, safePrefix(token), len(token))
	req.Header.Set("Authorization", "Bearer "+token)
	if hostname, _ := os.Hostname(); hostname != "" {
		req.Header.Set("X-NullBore-Device-Hostname", hostname)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}
	defer func() {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	debug.Printf("[dashboard] auth response: status=%d content-type=%s", resp.StatusCode, resp.Header.Get("Content-Type"))

	if resp.StatusCode == 401 || resp.StatusCode == 302 || resp.StatusCode == 303 {
		return fmt.Errorf("invalid API key (status %d)", resp.StatusCode)
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// pollDashboard fetches configs from the dashboard and reconciles.
func (d *Daemon) pollDashboard(httpClient *http.Client, dashURL string) {
	req, err := http.NewRequest("GET", dashURL+"/api/daemon/configs", nil)
	if err != nil {
		debug.Printf("[dashboard] request error: %v", err)
		return
	}
	req.Header.Set("Authorization", "Bearer "+d.dashboardBearerToken())
	if hostname, _ := os.Hostname(); hostname != "" {
		req.Header.Set("X-NullBore-Device-Hostname", hostname)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		debug.Printf("[dashboard] poll error: %v (will retry)", err)
		return
	}
	defer func() {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	if resp.StatusCode == 401 || resp.StatusCode == 302 || resp.StatusCode == 303 {
		// Session token may have expired — re-exchange.
		debug.Printf("[dashboard] poll got %d — re-exchanging token", resp.StatusCode)
		if err := d.exchangeToken(httpClient, dashURL); err != nil {
			log.Printf("[dashboard] token re-exchange failed: %v (will retry)", err)
		}
		return
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("[dashboard] auth failed (%d) — check API key (will retry)", resp.StatusCode)
		debug.Printf("[dashboard] response body: %s", string(body))
		return
	}

	var dashCfg DashboardConfig
	if err := json.NewDecoder(resp.Body).Decode(&dashCfg); err != nil {
		debug.Printf("[dashboard] parse error: %v", err)
		return
	}

	// Override tunnel server from dashboard if provided
	if dashCfg.TunnelServer != "" && d.cfg.ServerURL() != dashCfg.TunnelServer {
		d.cfg.Server = dashCfg.TunnelServer
	}

	// Convert active dashboard configs to TunnelSpecs
	var specs []config.TunnelSpec
	for _, c := range dashCfg.Configs {
		if !c.Active {
			continue
		}
		spec := config.TunnelSpec{
			Port:      c.LocalPort,
			Name:      c.Name,
			Subdomain: c.Subdomain,
			TTL:       c.TTL,
			IdleTTL:   c.IdleTTL,
		}
		specs = append(specs, spec)
	}

	// Reconcile if changed
	if tunnelsChanged(d.cfg.Tunnels, specs) {
		if len(specs) == 0 {
			debug.Printf("[dashboard] no active tunnels — waiting for configs")
		} else {
			debug.Printf("[dashboard] %d active tunnel(s) — reconciling", len(specs))
		}
		d.cfg.Tunnels = specs
		d.reconcile(specs)
	}

	// Re-report active tunnels so dashboard stays in sync after restarts
	d.reportActiveTunnels()
}

// reportActiveTunnels sends the current state of all active tunnels to the dashboard.
func (d *Daemon) reportActiveTunnels() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for key, mgr := range d.managers {
		spec := d.specs[key]
		for _, at := range mgr.Tunnels() {
			d.reportTunnelConnected(spec.Name, spec.Port, at.TunnelID, at.PublicURL)
		}
	}
}

// coreConfigChanged returns true if server, api_key, dashboard, or tls settings changed.
func coreConfigChanged(old, new *config.Config) bool {
	return old.Server != new.Server ||
		old.APIKey != new.APIKey ||
		old.Dashboard != new.Dashboard ||
		old.TLSSkipVerify != new.TLSSkipVerify ||
		old.DefaultTTL != new.DefaultTTL
}

// tunnelsChanged compares two tunnel spec lists.
func tunnelsChanged(old, new []config.TunnelSpec) bool {
	if len(old) != len(new) {
		return true
	}
	for i := range old {
		if old[i].Port != new[i].Port || old[i].Name != new[i].Name ||
			old[i].Subdomain != new[i].Subdomain || old[i].TTL != new[i].TTL ||
			old[i].Host != new[i].Host || old[i].IdleTTL != new[i].IdleTTL {
			return true
		}
	}
	return false
}

// reconcile converges running tunnels toward the desired config state.
func (d *Daemon) reconcile(specs []config.TunnelSpec) {
	d.mu.Lock()
	defer d.mu.Unlock()

	desired := make(map[string]config.TunnelSpec)
	for _, s := range specs {
		key := specKey(s)
		desired[key] = s
	}

	// Stop tunnels no longer in config
	for key, mgr := range d.managers {
		if _, ok := desired[key]; !ok {
			prev := d.specs[key]
			log.Printf("[daemon] stopping: %s (port %d) — removed from config", prev.Name, prev.Port)
			mgr.Close()
			delete(d.managers, key)
			delete(d.specs, key)
		}
	}

	// Start or update tunnels
	for key, s := range desired {
		prev, running := d.specs[key]
		if running && prev.Port == s.Port && prev.Subdomain == s.Subdomain &&
			prev.Host == s.Host && prev.TTL == s.TTL {
			continue // no change
		}

		// Config changed — stop old if running
		if running {
			if mgr, ok := d.managers[key]; ok {
				log.Printf("[daemon] restarting: %s (config changed)", s.Name)
				mgr.Close()
				delete(d.managers, key)
			}
		}

		name := s.Name
		if name == "" {
			name = s.Subdomain
		}
		if name == "" {
			name = "unnamed"
		}

		ttl := s.TTL
		if ttl == "" {
			ttl = d.cfg.DefaultTTL
		}

		log.Printf("[daemon] opening: %s (port %d)", name, s.Port)

		mgr := tunnel.NewManager(d.cfg, d.client)

		// In local-config mode (config.toml), s.Name is the user's intended
		// tunnel slug — they explicitly set it and want to claim that name
		// on the server.
		//
		// In dashboard mode, s.Subdomain is the tunnel slug (set when the
		// user has a namespace). s.Name is just a display label from the
		// dashboard UI. An empty subdomain means the user hasn't claimed a
		// namespace, so we must NOT pass a name to the server — the server
		// would reject it as "named tunnels require account subdomain
		// (Dev+)". Let the server generate a random slug instead.
		tunnelName := s.Subdomain
		if tunnelName == "" && !d.dashMode {
			tunnelName = s.Name
		}

		// Parse auth from config (user:pass format)
		var authUser, authPass string
		if s.Auth != "" {
			if parts := strings.SplitN(s.Auth, ":", 2); len(parts) == 2 {
				authUser, authPass = parts[0], parts[1]
			}
		}

		spec := tunnel.TunnelSpec{
			Port:     s.Port,
			Host:     s.Host,
			Name:     tunnelName,
			TTL:      ttl,
			Source:   "daemon",
			AuthUser: authUser,
			AuthPass: authPass,
		}

		at, err := mgr.OpenTunnel(spec)
		if err != nil {
			log.Printf("[daemon] failed to open %s: %v (will retry on next cycle)", name, err)
			continue
		}

		log.Printf("[daemon] ✓ %s → %s", name, at.PublicURL)

		// Report to dashboard if in dashboard mode
		d.reportTunnelConnected(s.Name, s.Port, at.TunnelID, at.PublicURL)

		// Start reconnect loop in background
		go mgr.Run()

		d.managers[key] = mgr
		d.specs[key] = s
	}
}

// Stop shuts down all tunnels.
func (d *Daemon) Stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for key, mgr := range d.managers {
		mgr.Close()
		delete(d.managers, key)
	}
	d.specs = make(map[string]config.TunnelSpec)
}

// ActiveCount returns the number of running tunnels.
func (d *Daemon) ActiveCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.managers)
}

func safePrefix(key string) string {
	if len(key) > 12 {
		return key[:12] + "..."
	}
	return key
}
