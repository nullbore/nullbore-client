package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nullbore/nullbore-client/internal/config"
)

func newTestServer(handler http.HandlerFunc) (*httptest.Server, *Client) {
	ts := httptest.NewServer(handler)
	cfg := &config.Config{
		Server: ts.URL,
		APIKey: "nbk_test_key",
	}
	return ts, New(cfg)
}

func TestCreateTunnel(t *testing.T) {
	ts, c := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/v1/tunnels" {
			t.Errorf("path = %s, want /v1/tunnels", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer nbk_test_key" {
			t.Errorf("auth header = %q, want Bearer token", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("content-type = %q, want application/json", got)
		}

		// Decode the request body
		var body map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)
		if body["local_port"].(float64) != 3000 {
			t.Errorf("local_port = %v, want 3000", body["local_port"])
		}
		if body["name"] != "myapp" {
			t.Errorf("name = %v, want myapp", body["name"])
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(Tunnel{
			ID:        "test-id-123",
			Slug:      "myapp",
			ClientID:  "testclient",
			LocalPort: 3000,
			Name:      "myapp",
			TTL:       "1h",
			Mode:      "relay",
			CreatedAt: "2026-03-30T00:00:00Z",
			ExpiresAt: "2026-03-30T01:00:00Z",
		})
	})
	defer ts.Close()

	tunnel, err := c.CreateTunnel(3000, "myapp", "1h")
	if err != nil {
		t.Fatalf("CreateTunnel() error: %v", err)
	}
	if tunnel.ID != "test-id-123" {
		t.Errorf("ID = %q, want %q", tunnel.ID, "test-id-123")
	}
	if tunnel.Slug != "myapp" {
		t.Errorf("Slug = %q, want %q", tunnel.Slug, "myapp")
	}
	if tunnel.Mode != "relay" {
		t.Errorf("Mode = %q, want %q", tunnel.Mode, "relay")
	}
}

func TestCreateTunnelMinimal(t *testing.T) {
	ts, c := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)

		// Name and TTL should be absent when empty
		if _, ok := body["name"]; ok {
			t.Errorf("name should not be in request when empty")
		}
		if _, ok := body["ttl"]; ok {
			t.Errorf("ttl should not be in request when empty")
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(Tunnel{
			ID:   "gen-id",
			Slug: "a1b2c3d4e5f6",
		})
	})
	defer ts.Close()

	tunnel, err := c.CreateTunnel(8080, "", "")
	if err != nil {
		t.Fatalf("CreateTunnel() error: %v", err)
	}
	if tunnel.ID != "gen-id" {
		t.Errorf("ID = %q, want %q", tunnel.ID, "gen-id")
	}
}

func TestListTunnels(t *testing.T) {
	ts, c := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/v1/tunnels" {
			t.Errorf("path = %s, want /v1/tunnels", r.URL.Path)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]Tunnel{
			{ID: "t1", Slug: "first", LocalPort: 3000},
			{ID: "t2", Slug: "second", LocalPort: 8080},
		})
	})
	defer ts.Close()

	tunnels, err := c.ListTunnels()
	if err != nil {
		t.Fatalf("ListTunnels() error: %v", err)
	}
	if len(tunnels) != 2 {
		t.Fatalf("got %d tunnels, want 2", len(tunnels))
	}
	if tunnels[0].Slug != "first" {
		t.Errorf("tunnels[0].Slug = %q, want %q", tunnels[0].Slug, "first")
	}
	if tunnels[1].LocalPort != 8080 {
		t.Errorf("tunnels[1].LocalPort = %d, want 8080", tunnels[1].LocalPort)
	}
}

func TestCloseTunnel(t *testing.T) {
	ts, c := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" {
			t.Errorf("method = %s, want DELETE", r.Method)
		}
		if r.URL.Path != "/v1/tunnels/test-id-123" {
			t.Errorf("path = %s, want /v1/tunnels/test-id-123", r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	defer ts.Close()

	err := c.CloseTunnel("test-id-123")
	if err != nil {
		t.Fatalf("CloseTunnel() error: %v", err)
	}
}

func TestHealth(t *testing.T) {
	ts, c := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Errorf("path = %s, want /health", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status":  "ok",
			"version": "0.1.0",
		})
	})
	defer ts.Close()

	health, err := c.Health()
	if err != nil {
		t.Fatalf("Health() error: %v", err)
	}
	if health["status"] != "ok" {
		t.Errorf("status = %q, want ok", health["status"])
	}
	if health["version"] != "0.1.0" {
		t.Errorf("version = %q, want 0.1.0", health["version"])
	}
}

func TestServerError(t *testing.T) {
	ts, c := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":"rate limit exceeded"}`))
	})
	defer ts.Close()

	_, err := c.CreateTunnel(3000, "", "")
	if err == nil {
		t.Fatal("expected error on 429 response")
	}
	// Should contain the status code
	if got := err.Error(); !contains(got, "429") {
		t.Errorf("error = %q, should contain status code 429", got)
	}
}

func TestNoAuthHeader(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("expected no auth header, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer ts.Close()

	cfg := &config.Config{
		Server: ts.URL,
		APIKey: "", // no key
	}
	c := New(cfg)

	_, err := c.Health()
	if err != nil {
		t.Fatalf("Health() error: %v", err)
	}
}

func TestUnreachableServer(t *testing.T) {
	cfg := &config.Config{
		Server: "http://127.0.0.1:1", // nothing listening
	}
	c := New(cfg)

	_, err := c.Health()
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
