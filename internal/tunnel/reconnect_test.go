package tunnel

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/nullbore/nullbore-client/internal/client"
	"github.com/nullbore/nullbore-client/internal/config"
)

// TestReconnectAfterDisconnect verifies that the reconnect loop
// re-registers the tunnel and reconnects after the control WS drops.
func TestReconnectAfterDisconnect(t *testing.T) {
	var createCount atomic.Int32
	var controlCount atomic.Int32
	upgrader := websocket.Upgrader{}

	mux := http.NewServeMux()

	// Tunnel creation endpoint
	mux.HandleFunc("POST /v1/tunnels", func(w http.ResponseWriter, r *http.Request) {
		createCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":         fmt.Sprintf("tunnel-%d", createCount.Load()),
			"slug":       "reconnect-test",
			"client_id":  "test",
			"local_port": 3000,
			"status":     "active",
			"created_at": time.Now().Format(time.RFC3339),
			"expires_at": time.Now().Add(time.Hour).Format(time.RFC3339),
		})
	})

	// Control WS — accept then immediately close (simulates disconnect)
	mux.HandleFunc("GET /ws/control", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		controlCount.Add(1)
		conn.Close()
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg := &config.Config{Server: srv.URL, APIKey: "test-key"}
	apiClient := client.New(cfg)
	connector := NewConnector(cfg, "tunnel-1", 3000)

	done := make(chan struct{})
	go func() {
		defer close(done)
		RunWithFullReconnect(cfg, apiClient, 3000, "reconnect-test", "1h", connector)
	}()

	// Wait for at least 2 reconnect cycles
	deadline := time.After(10 * time.Second)
	for {
		if controlCount.Load() >= 2 && createCount.Load() >= 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("timed out: creates=%d controls=%d", createCount.Load(), controlCount.Load())
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}

	connector.Close()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunWithFullReconnect did not exit after Close()")
	}

	t.Logf("reconnected: creates=%d controls=%d", createCount.Load(), controlCount.Load())
}

// TestReconnectServerDown verifies graceful shutdown when server is unreachable.
func TestReconnectServerDown(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	url := srv.URL
	srv.Close() // immediately close — server is "down"

	cfg := &config.Config{Server: url, APIKey: "test-key"}
	apiClient := client.New(cfg)
	connector := NewConnector(cfg, "test", 3000)

	done := make(chan error, 1)
	go func() {
		done <- RunWithFullReconnect(cfg, apiClient, 3000, "test", "1h", connector)
	}()

	// Let it fail a couple times with backoff, then shut down
	time.Sleep(1500 * time.Millisecond)
	connector.Close()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunWithFullReconnect did not exit after Close()")
	}
}
