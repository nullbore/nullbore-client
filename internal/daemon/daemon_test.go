package daemon

import (
	"testing"

	"github.com/nullbore/nullbore-client/internal/config"
)

func TestSpecKey(t *testing.T) {
	s := config.TunnelSpec{Port: 3000, Name: "api"}
	key := specKey(s)
	if key == "" {
		t.Error("specKey should not be empty")
	}

	s2 := config.TunnelSpec{Port: 3000, Name: "api"}
	if specKey(s) != specKey(s2) {
		t.Error("same spec should produce same key")
	}

	s3 := config.TunnelSpec{Port: 8080, Name: "web"}
	if specKey(s) == specKey(s3) {
		t.Error("different specs should produce different keys")
	}
}

func TestTunnelsChanged(t *testing.T) {
	a := []config.TunnelSpec{{Port: 3000, Name: "api"}}
	b := []config.TunnelSpec{{Port: 3000, Name: "api"}}

	if tunnelsChanged(a, b) {
		t.Error("identical specs should not be changed")
	}

	c := []config.TunnelSpec{{Port: 3000, Name: "api-v2"}}
	if !tunnelsChanged(a, c) {
		t.Error("different name should be changed")
	}

	d := []config.TunnelSpec{{Port: 3000, Name: "api"}, {Port: 8080, Name: "web"}}
	if !tunnelsChanged(a, d) {
		t.Error("different length should be changed")
	}

	if !tunnelsChanged(nil, a) {
		t.Error("nil vs non-nil should be changed")
	}
}

func TestNewDaemon(t *testing.T) {
	cfg := &config.Config{
		Server: "https://tunnel.nullbore.com",
		APIKey: "nbk_test",
		Tunnels: []config.TunnelSpec{
			{Port: 3000, Name: "api"},
			{Port: 5432, Name: "db"},
		},
	}

	d := New(cfg, "0.0.0-test")
	if d == nil {
		t.Fatal("daemon should not be nil")
	}
	if d.ActiveCount() != 0 {
		t.Errorf("active = %d, want 0 (not started)", d.ActiveCount())
	}
}
