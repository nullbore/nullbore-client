package tunnel

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/nullbore/nullbore-client/internal/client"
	"github.com/nullbore/nullbore-client/internal/config"
)

// TunnelSpec defines a single tunnel to open.
type TunnelSpec struct {
	Port int
	Host string // target host (default "", meaning localhost)
	Name string
	TTL  string
}

// ActiveTunnel tracks a running tunnel.
type ActiveTunnel struct {
	Spec      TunnelSpec
	TunnelID  string
	Slug      string
	PublicURL string
	Connector *Connector
}

// Manager manages multiple tunnels with reconnection.
type Manager struct {
	cfg       *config.Config
	apiClient *client.Client
	tunnels   []*ActiveTunnel
	mu        sync.Mutex
	closed    bool
	done      chan struct{}
}

func NewManager(cfg *config.Config, apiClient *client.Client) *Manager {
	return &Manager{
		cfg:       cfg,
		apiClient: apiClient,
		done:      make(chan struct{}),
	}
}

// OpenTunnel creates and connects a single tunnel, adding it to the manager.
func (m *Manager) OpenTunnel(spec TunnelSpec) (*ActiveTunnel, error) {
	t, err := m.apiClient.CreateTunnel(spec.Port, spec.Name, spec.TTL)
	if err != nil {
		return nil, fmt.Errorf("creating tunnel for port %d: %w", spec.Port, err)
	}

	var conn *Connector
	if spec.Host != "" {
		conn = NewConnectorWithHost(m.cfg, t.ID, spec.Host, spec.Port)
	} else {
		conn = NewConnector(m.cfg, t.ID, spec.Port)
	}
	if err := conn.Connect(); err != nil {
		return nil, fmt.Errorf("connecting tunnel for port %d: %w", spec.Port, err)
	}

	// Use server-provided public_url if available, otherwise construct from server URL
	publicURL := t.PublicURL
	if publicURL == "" {
		publicURL = fmt.Sprintf("%s/t/%s", m.cfg.ServerURL(), t.Slug)
	}

	at := &ActiveTunnel{
		Spec:      spec,
		TunnelID:  t.ID,
		Slug:      t.Slug,
		PublicURL: publicURL,
		Connector: conn,
	}

	m.mu.Lock()
	m.tunnels = append(m.tunnels, at)
	m.mu.Unlock()

	return at, nil
}

// Run starts reconnection loops for all tunnels and blocks until Close() or all fail.
func (m *Manager) Run() error {
	m.mu.Lock()
	tunnels := make([]*ActiveTunnel, len(m.tunnels))
	copy(tunnels, m.tunnels)
	m.mu.Unlock()

	if len(tunnels) == 0 {
		return fmt.Errorf("no tunnels to manage")
	}

	var wg sync.WaitGroup
	errCh := make(chan error, len(tunnels))

	for _, at := range tunnels {
		wg.Add(1)
		go func(at *ActiveTunnel) {
			defer wg.Done()
			err := m.runTunnel(at)
			if err != nil {
				errCh <- fmt.Errorf("tunnel %s (port %d): %w", at.Slug, at.Spec.Port, err)
			}
		}(at)
	}

	// Wait for all tunnels to finish
	go func() {
		wg.Wait()
		close(errCh)
	}()

	// Collect first error (if any)
	for err := range errCh {
		if err != nil {
			return err
		}
	}
	return nil
}

// runTunnel handles reconnection for a single tunnel.
func (m *Manager) runTunnel(at *ActiveTunnel) error {
	backoff := NewBackoff()

	for {
		// Run until disconnect
		connected, err := at.Connector.runOnce()

		m.mu.Lock()
		closed := m.closed
		m.mu.Unlock()
		if closed || at.Connector.closed {
			return nil
		}

		if connected {
			backoff.Reset()
			if err != nil {
				log.Printf("[%s] disconnected: %v", at.Slug, err)
			} else {
				log.Printf("[%s] disconnected (clean)", at.Slug)
			}
		} else {
			if err != nil {
				log.Printf("[%s] connection failed: %v", at.Slug, err)
			}
		}

		// Wait before reconnecting
		d := backoff.Duration()
		log.Printf("[%s] reconnecting in %s...", at.Slug, d)

		select {
		case <-time.After(d):
		case <-m.done:
			return nil
		}

		// Re-create the tunnel (server may have restarted)
		log.Printf("[%s] re-registering tunnel...", at.Slug)
		t, err := m.apiClient.CreateTunnel(at.Spec.Port, at.Spec.Name, at.Spec.TTL)
		if err != nil {
			log.Printf("[%s] re-registration failed: %v", at.Slug, err)
			continue
		}

		log.Printf("[%s] re-registered: id=%s slug=%s", at.Slug, t.ID, t.Slug)

		// Update
		at.Connector.mu.Lock()
		at.Connector.tunnelID = t.ID
		at.Connector.mu.Unlock()

		m.mu.Lock()
		at.TunnelID = t.ID
		at.Slug = t.Slug
		if t.PublicURL != "" {
			at.PublicURL = t.PublicURL
		} else {
			at.PublicURL = fmt.Sprintf("%s/t/%s", m.cfg.ServerURL(), t.Slug)
		}
		m.mu.Unlock()

		// Reconnect control WebSocket
		if err := at.Connector.connect(); err != nil {
			log.Printf("[%s] reconnect failed: %v", at.Slug, err)
			continue
		}

		log.Printf("[%s] reconnected — forwarding %s → localhost:%d", at.Slug, at.PublicURL, at.Spec.Port)
	}
}

// Close shuts down all tunnels.
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	tunnels := make([]*ActiveTunnel, len(m.tunnels))
	copy(tunnels, m.tunnels)
	m.mu.Unlock()

	close(m.done)

	for _, at := range tunnels {
		at.Connector.Close()
		m.apiClient.CloseTunnel(at.TunnelID)
	}
}

// Tunnels returns a snapshot of active tunnels.
func (m *Manager) Tunnels() []*ActiveTunnel {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]*ActiveTunnel, len(m.tunnels))
	copy(result, m.tunnels)
	return result
}
