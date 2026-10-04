package client

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestNormalizeMode(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"", "", false},
		{"relay", "", false},
		{"RELAY", "", false},
		{"tls-passthrough", ModeTLSPassthrough, false},
		{" TLS-Passthrough ", ModeTLSPassthrough, false},
		{"direct", "", true},
		{"tls", "", true},
	}
	for _, tt := range tests {
		got, err := NormalizeMode(tt.in)
		if (err != nil) != tt.wantErr {
			t.Errorf("NormalizeMode(%q) err = %v, wantErr %v", tt.in, err, tt.wantErr)
			continue
		}
		if got != tt.want {
			t.Errorf("NormalizeMode(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestCreateTunnelBodyModeOnlyWhenSet(t *testing.T) {
	base := TunnelOptions{Port: 8443, Source: "cli"}

	for _, mode := range []string{"", ModeRelay} {
		o := base
		o.Mode = mode
		body := createTunnelBody(o, "host")
		if _, ok := body["mode"]; ok {
			t.Errorf("mode=%q: body should omit mode, got %v", mode, body)
		}
	}

	o := base
	o.Mode = ModeTLSPassthrough
	body := createTunnelBody(o, "host")
	if body["mode"] != ModeTLSPassthrough {
		t.Errorf("body[mode] = %v, want %q", body["mode"], ModeTLSPassthrough)
	}
	// Existing fields untouched.
	if body["local_port"] != 8443 || body["source"] != "cli" || body["device_name"] != "host" {
		t.Errorf("unexpected base fields: %v", body)
	}
	for _, k := range []string{"name", "ttl", "auth_user", "auth_pass"} {
		if _, ok := body[k]; ok {
			t.Errorf("unset optional field %q should be omitted: %v", k, body)
		}
	}
}

func TestTunnelOptionsValidate(t *testing.T) {
	if err := (TunnelOptions{Mode: ModeTLSPassthrough}).Validate(); err != nil {
		t.Errorf("passthrough alone should be valid: %v", err)
	}
	if err := (TunnelOptions{AuthUser: "u", AuthPass: "p"}).Validate(); err != nil {
		t.Errorf("relay + auth should be valid: %v", err)
	}
	err := (TunnelOptions{Mode: ModeTLSPassthrough, AuthUser: "u", AuthPass: "p"}).Validate()
	if err == nil || !strings.Contains(err.Error(), "basic auth") {
		t.Errorf("passthrough + auth should be rejected, got %v", err)
	}
	if err := (TunnelOptions{Mode: "bogus"}).Validate(); err == nil {
		t.Error("unknown mode should be rejected")
	}
}

func TestCreateTunnelNoModeByDefault(t *testing.T) {
	ts, c := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)
		if _, ok := body["mode"]; ok {
			t.Errorf("default create should not send mode, got %v", body["mode"])
		}
		json.NewEncoder(w).Encode(Tunnel{ID: "id1", Slug: "s", Mode: "relay"})
	})
	defer ts.Close()

	if _, err := c.CreateTunnelFull(3000, "", "", "cli", "", ""); err != nil {
		t.Fatalf("CreateTunnelFull: %v", err)
	}
	if _, err := c.CreateTunnelWithOptions(TunnelOptions{Port: 3000, Mode: ModeRelay}); err != nil {
		t.Fatalf("CreateTunnelWithOptions(relay): %v", err)
	}
}

func TestCreateTunnelSendsPassthroughMode(t *testing.T) {
	ts, c := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)
		if body["mode"] != ModeTLSPassthrough {
			t.Errorf("mode = %v, want %q", body["mode"], ModeTLSPassthrough)
		}
		json.NewEncoder(w).Encode(Tunnel{ID: "id1", Slug: "s", Mode: ModeTLSPassthrough})
	})
	defer ts.Close()

	tun, err := c.CreateTunnelWithOptions(TunnelOptions{Port: 8443, Source: "cli", Mode: ModeTLSPassthrough})
	if err != nil {
		t.Fatalf("CreateTunnelWithOptions: %v", err)
	}
	if tun.Mode != ModeTLSPassthrough {
		t.Errorf("Mode = %q", tun.Mode)
	}
}

func TestCreateTunnelPassthroughWithAuthNoRequest(t *testing.T) {
	var hits atomic.Int32
	ts, c := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
	})
	defer ts.Close()

	_, err := c.CreateTunnelWithOptions(TunnelOptions{Port: 8443, Mode: ModeTLSPassthrough, AuthUser: "u", AuthPass: "p"})
	if err == nil {
		t.Fatal("expected local validation error")
	}
	if hits.Load() != 0 {
		t.Errorf("server should not be contacted, got %d requests", hits.Load())
	}
}

func TestCreateTunnelPassthroughUnsupportedServer(t *testing.T) {
	var deleted atomic.Int32
	ts, c := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "POST":
			// Old server: ignores the unknown field and creates a relay tunnel.
			json.NewEncoder(w).Encode(Tunnel{ID: "old-1", Slug: "s", Mode: "relay"})
		case "DELETE":
			if r.URL.Path == "/v1/tunnels/old-1" {
				deleted.Add(1)
			}
			w.WriteHeader(http.StatusOK)
		}
	})
	defer ts.Close()

	_, err := c.CreateTunnelWithOptions(TunnelOptions{Port: 8443, Mode: ModeTLSPassthrough})
	if err == nil || !strings.Contains(err.Error(), "does not support TLS passthrough") {
		t.Fatalf("expected unsupported-server error, got %v", err)
	}
	if deleted.Load() != 1 {
		t.Errorf("mismatched tunnel should be closed, deletes = %d", deleted.Load())
	}
}

func TestCreateTunnelPassthroughServerRejections(t *testing.T) {
	tests := []struct {
		status int
		body   string
		want   string
	}{
		{http.StatusForbidden, `{"error":"tls passthrough requires a paid plan"}`, "not available on your plan"},
		{http.StatusBadRequest, `{"error":"tls passthrough cannot be combined with basic auth"}`, "rejected the TLS passthrough tunnel"},
	}
	for _, tt := range tests {
		ts, c := newTestServer(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tt.status)
			w.Write([]byte(tt.body))
		})

		_, err := c.CreateTunnelWithOptions(TunnelOptions{Port: 8443, Mode: ModeTLSPassthrough})
		ts.Close()
		if err == nil {
			t.Fatalf("%d: expected error", tt.status)
		}
		if !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%d: error = %q, want it to contain %q", tt.status, err, tt.want)
		}
		// The server's own message is surfaced (not the raw JSON).
		if strings.Contains(err.Error(), `{"error"`) {
			t.Errorf("%d: error should not include raw JSON: %q", tt.status, err)
		}
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.StatusCode != tt.status {
			t.Errorf("%d: errors.As(*APIError) failed: %v", tt.status, err)
		}
	}
}

func TestRelayServerErrorUnchanged(t *testing.T) {
	ts, c := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":"persistent tunnels require a paid plan"}`))
	})
	defer ts.Close()

	_, err := c.CreateTunnel(3000, "", "")
	want := `server error (403): {"error":"persistent tunnels require a paid plan"}`
	if err == nil || err.Error() != want {
		t.Errorf("relay error = %v, want %q", err, want)
	}
}

func TestAPIErrorMessage(t *testing.T) {
	if got := (&APIError{Body: `{"error":"nope"}`}).Message(); got != "nope" {
		t.Errorf("JSON message = %q", got)
	}
	if got := (&APIError{Body: "plain text\n"}).Message(); got != "plain text" {
		t.Errorf("plain message = %q", got)
	}
}
