package tunnel

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/nullbore/nullbore-client/internal/client"
	"github.com/nullbore/nullbore-client/internal/config"
)

// TestManagerMultipleTunnels verifies opening and closing multiple tunnels.
func TestManagerMultipleTunnels(t *testing.T) {
	wsUpgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	var tunnelCount atomic.Int32

	// Mock server: REST API + WS control endpoint
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/tunnels", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)

		n := tunnelCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":         fmt.Sprintf("tunnel-%d", n),
			"slug":       fmt.Sprintf("slug-%d", n),
			"local_port": body["local_port"],
			"ttl":        "1h0m0s",
			"mode":       "relay",
			"created_at": "2026-03-30T00:00:00Z",
			"expires_at": "2026-03-30T01:00:00Z",
		})
	})
	mux.HandleFunc("DELETE /v1/tunnels/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "closed"})
	})
	mux.HandleFunc("GET /ws/control", func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		// Just keep the connection alive
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	cfg := &config.Config{
		Server: ts.URL,
		APIKey: "test_key",
	}
	apiClient := client.New(cfg)
	mgr := NewManager(cfg, apiClient)

	// Open 3 tunnels
	specs := []TunnelSpec{
		{Port: 3000, Name: "api"},
		{Port: 8080, Name: "web"},
		{Port: 5432, Name: "db"},
	}

	for _, spec := range specs {
		at, err := mgr.OpenTunnel(spec)
		if err != nil {
			t.Fatalf("OpenTunnel(%d) error: %v", spec.Port, err)
		}
		if at.Spec.Port != spec.Port {
			t.Errorf("tunnel port = %d, want %d", at.Spec.Port, spec.Port)
		}
		if at.PublicURL == "" {
			t.Error("PublicURL should not be empty")
		}
	}

	// Verify all 3 are tracked
	tunnels := mgr.Tunnels()
	if len(tunnels) != 3 {
		t.Fatalf("got %d tunnels, want 3", len(tunnels))
	}

	// Close all
	mgr.Close()

	// Verify manager is closed
	if !mgr.closed {
		t.Error("manager should be closed after Close()")
	}
}
