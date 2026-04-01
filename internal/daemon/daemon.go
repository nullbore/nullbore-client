package daemon

import (
	"log"
	"sync"
	"time"

	"github.com/nullbore/nullbore-client/internal/client"
	"github.com/nullbore/nullbore-client/internal/config"
	"github.com/nullbore/nullbore-client/internal/tunnel"
)

// Daemon manages persistent tunnel connections from config.toml.
// The config file is the single source of truth for what tunnels should run.
// One-off `nullbore open` tunnels coexist independently on the same device.
type Daemon struct {
	cfg    *config.Config
	client *client.Client

	mu       sync.Mutex
	managers map[string]*tunnel.Manager // "port:name" → running manager
	specs    map[string]config.TunnelSpec
}

// New creates a new daemon.
func New(cfg *config.Config) *Daemon {
	return &Daemon{
		cfg:      cfg,
		client:   client.New(cfg),
		managers: make(map[string]*tunnel.Manager),
		specs:    make(map[string]config.TunnelSpec),
	}
}

// specKey returns a stable identifier for a tunnel spec.
func specKey(s config.TunnelSpec) string {
	name := s.Name
	if name == "" {
		name = s.Subdomain
	}
	return name + ":" + string(rune(s.Port+'0'))
}

// Run starts the daemon: opens tunnels from config and watches for changes.
func (d *Daemon) Run() error {
	log.Printf("nullbore daemon starting")
	log.Printf("server: %s", d.cfg.ServerURL())

	if len(d.cfg.Tunnels) == 0 {
		log.Printf("no tunnels defined in config — daemon will idle")
		log.Printf("add [[tunnels]] blocks to ~/.nullbore/config.toml")
		// Keep running so one-off `nullbore open` tunnels still work on same key
		select {}
	}

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

		// Check if tunnel specs changed
		if tunnelsChanged(d.cfg.Tunnels, newCfg.Tunnels) {
			log.Printf("config changed: reconciling tunnels")
			d.cfg.Tunnels = newCfg.Tunnels
			d.reconcile(newCfg.Tunnels)
		}
	}
	return nil
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

		spec := tunnel.TunnelSpec{
			Port: s.Port,
			Host: s.Host,
			Name: s.Subdomain,
			TTL:  ttl,
		}

		at, err := mgr.OpenTunnel(spec)
		if err != nil {
			log.Printf("[daemon] failed to open %s: %v (will retry on next cycle)", name, err)
			continue
		}

		log.Printf("[daemon] ✓ %s → %s", name, at.PublicURL)

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
