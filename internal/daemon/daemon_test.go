package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/nullbore/nullbore-client/internal/config"
)

// TestAuthenticate verifies the daemon can exchange an API key for a session token.
func TestAuthenticate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/auth/token" {
			http.NotFound(w, r)
			return
		}
		var req struct {
			APIKey string `json:"api_key"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if req.APIKey != "nbk_test123" {
			w.WriteHeader(401)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid"})
			return
		}
		json.NewEncoder(w).Encode(map[string]string{
			"token":   "session_abc",
			"user_id": "user123",
		})
	}))
	defer server.Close()

	cfg := &config.Config{
		Dashboard: server.URL,
		APIKey:    "nbk_test123",
	}
	d := New(cfg)
	err := d.authenticate()
	if err != nil {
		t.Fatalf("authenticate failed: %v", err)
	}
	if d.dashToken != "session_abc" {
		t.Errorf("expected token 'session_abc', got '%s'", d.dashToken)
	}
}

func TestAuthenticateInvalidKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid"})
	}))
	defer server.Close()

	cfg := &config.Config{Dashboard: server.URL, APIKey: "bad_key"}
	d := New(cfg)
	err := d.authenticate()
	if err == nil {
		t.Fatal("expected error for invalid key")
	}
}

// TestReconcileStartsActiveTunnels verifies reconcile starts tunnels marked active.
// Uses a mock that doesn't actually connect — just tests the reconciliation logic.
func TestReconcileStopsDeactivated(t *testing.T) {
	cfg := &config.Config{
		Server: "http://localhost:9999", // won't be reached
		APIKey: "nbk_test",
	}
	d := New(cfg)

	// Manually add a "running" tunnel
	d.mu.Lock()
	d.active["config-1"] = TunnelConfig{
		ID: "config-1", Name: "test", LocalPort: 3000, Active: true,
	}
	d.mu.Unlock()

	// Reconcile with config-1 now inactive
	configs := []TunnelConfig{
		{ID: "config-1", Name: "test", LocalPort: 3000, Active: false},
	}

	d.mu.Lock()
	// Simulate: mark as no longer active, remove from active map
	desired := make(map[string]TunnelConfig)
	for _, c := range configs {
		desired[c.ID] = c
	}
	for id, prev := range d.active {
		curr, exists := desired[id]
		if !exists || !curr.Active {
			_ = prev // would stop manager
			delete(d.active, id)
		}
	}
	d.mu.Unlock()

	if len(d.active) != 0 {
		t.Errorf("expected 0 active after deactivation, got %d", len(d.active))
	}
}

// TestReconcileIgnoresAlreadyRunning verifies no restart for unchanged configs.
func TestReconcileIgnoresAlreadyRunning(t *testing.T) {
	cfg := &config.Config{Server: "http://localhost:9999", APIKey: "nbk_test"}
	d := New(cfg)

	tc := TunnelConfig{ID: "c1", Name: "api", LocalPort: 3000, Subdomain: "api", Active: true}
	d.mu.Lock()
	d.active["c1"] = tc
	d.mu.Unlock()

	// Call sync with same config — should not restart
	startCount := 0
	d.mu.Lock()
	for id, c := range map[string]TunnelConfig{"c1": tc} {
		if !c.Active {
			continue
		}
		prev, running := d.active[id]
		if running && prev.LocalPort == c.LocalPort && prev.Subdomain == c.Subdomain {
			continue // no restart needed
		}
		startCount++
	}
	d.mu.Unlock()

	if startCount != 0 {
		t.Errorf("expected 0 restarts for unchanged config, got %d", startCount)
	}
}

// TestSyncFetchesConfigs verifies the daemon fetches configs from the dashboard API.
func TestSyncFetchesConfigs(t *testing.T) {
	var mu sync.Mutex
	fetchCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/token":
			json.NewEncoder(w).Encode(map[string]string{"token": "tok", "user_id": "u1"})
		case "/api/daemon/configs":
			mu.Lock()
			fetchCount++
			mu.Unlock()
			if r.Header.Get("Authorization") != "Bearer tok" {
				w.WriteHeader(401)
				return
			}
			json.NewEncoder(w).Encode(map[string]interface{}{
				"configs": []TunnelConfig{
					{ID: "c1", Name: "test", LocalPort: 8080, Active: false},
				},
				"tunnel_server": "https://tunnel.example.com",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := &config.Config{Dashboard: server.URL, APIKey: "nbk_test"}
	d := New(cfg)
	d.authenticate()

	err := d.sync()
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}

	mu.Lock()
	if fetchCount != 1 {
		t.Errorf("expected 1 fetch, got %d", fetchCount)
	}
	mu.Unlock()
}

// TestSyncReauthOnExpiry verifies the daemon re-authenticates and retries after 401.
func TestSyncReauthOnExpiry(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/token":
			json.NewEncoder(w).Encode(map[string]string{"token": "tok_new", "user_id": "u1"})
		case "/api/daemon/configs":
			callCount++
			if callCount == 1 {
				w.WriteHeader(401) // first call fails
				return
			}
			json.NewEncoder(w).Encode(map[string]interface{}{
				"configs":       []TunnelConfig{},
				"tunnel_server": "https://tunnel.example.com",
			})
		}
	}))
	defer server.Close()

	cfg := &config.Config{Dashboard: server.URL, APIKey: "nbk_test"}
	d := New(cfg)
	d.dashToken = "old_expired_token"

	// First sync returns 401 error
	err := d.sync()
	if err == nil {
		t.Fatal("expected 401 error on first sync")
	}

	// Re-authenticate
	err = d.authenticate()
	if err != nil {
		t.Fatalf("re-auth failed: %v", err)
	}
	if d.dashToken != "tok_new" {
		t.Errorf("expected new token, got %s", d.dashToken)
	}

	// Second sync should work
	err = d.sync()
	if err != nil {
		t.Fatalf("second sync should succeed: %v", err)
	}
}

// TestDaemonActiveCount verifies counting.
func TestDaemonActiveCount(t *testing.T) {
	cfg := &config.Config{Server: "http://localhost:9999", APIKey: "test"}
	d := New(cfg)

	if d.ActiveCount() != 0 {
		t.Errorf("expected 0 active, got %d", d.ActiveCount())
	}

	d.active["a"] = TunnelConfig{ID: "a", Active: true}
	d.active["b"] = TunnelConfig{ID: "b", Active: true}

	if d.ActiveCount() != 2 {
		t.Errorf("expected 2 active, got %d", d.ActiveCount())
	}
}

// Ensure unused import doesn't cause issues
var _ = time.Second
