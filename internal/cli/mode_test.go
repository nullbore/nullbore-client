package cli

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/nullbore/nullbore-client/internal/client"
	"github.com/nullbore/nullbore-client/internal/config"
	"github.com/nullbore/nullbore-client/internal/tunnel"
)

func TestParseStaticTunnels(t *testing.T) {
	const pt = client.ModeTLSPassthrough
	tests := []struct {
		in   string
		want []tunnel.TunnelSpec
	}{
		// Existing forms — unchanged.
		{"3000", []tunnel.TunnelSpec{{Port: 3000, TTL: "1h"}}},
		{"3000:api", []tunnel.TunnelSpec{{Port: 3000, Name: "api", TTL: "1h"}}},
		{"web:8080", []tunnel.TunnelSpec{{Port: 8080, Host: "web", TTL: "1h"}}},
		{"gramps:5000:gramps-web", []tunnel.TunnelSpec{{Port: 5000, Host: "gramps", Name: "gramps-web", TTL: "1h"}}},
		{"gramps:5000:gramps-web, openclaw:8080:claw ,", []tunnel.TunnelSpec{
			{Port: 5000, Host: "gramps", Name: "gramps-web", TTL: "1h"},
			{Port: 8080, Host: "openclaw", Name: "claw", TTL: "1h"},
		}},
		// New +option suffix on every form.
		{"8443+tls-passthrough", []tunnel.TunnelSpec{{Port: 8443, TTL: "1h", Mode: pt}}},
		{"8443:secure+tls-passthrough", []tunnel.TunnelSpec{{Port: 8443, Name: "secure", TTL: "1h", Mode: pt}}},
		{"caddy:443+tls-passthrough", []tunnel.TunnelSpec{{Port: 443, Host: "caddy", TTL: "1h", Mode: pt}}},
		{"caddy:443:secure+tls-passthrough,web:3000:app", []tunnel.TunnelSpec{
			{Port: 443, Host: "caddy", Name: "secure", TTL: "1h", Mode: pt},
			{Port: 3000, Host: "web", Name: "app", TTL: "1h"},
		}},
		// Explicit relay is the default (no mode sent).
		{"3000:api+relay", []tunnel.TunnelSpec{{Port: 3000, Name: "api", TTL: "1h"}}},
	}
	for _, tt := range tests {
		got, err := parseStaticTunnels(tt.in, "1h")
		if err != nil {
			t.Errorf("parseStaticTunnels(%q) error: %v", tt.in, err)
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("parseStaticTunnels(%q)\n got  %+v\n want %+v", tt.in, got, tt.want)
		}
	}
}

func TestParseStaticTunnelsErrors(t *testing.T) {
	for _, in := range []string{
		"abc",
		"web:abc",
		"web:abc:slug",
		"3000:api+bogus",
		"3000:api+",
		"3000+tls",
	} {
		if _, err := parseStaticTunnels(in, "1h"); err == nil {
			t.Errorf("parseStaticTunnels(%q) should error", in)
		}
	}
}

func TestValidateOpenModeFlags(t *testing.T) {
	if err := validateOpenModeFlags(false, ""); err != nil {
		t.Errorf("no flags: %v", err)
	}
	if err := validateOpenModeFlags(true, ""); err != nil {
		t.Errorf("passthrough alone: %v", err)
	}
	if err := validateOpenModeFlags(false, "u:p"); err != nil {
		t.Errorf("auth alone: %v", err)
	}
	err := validateOpenModeFlags(true, "u:p")
	if err == nil || !strings.Contains(err.Error(), "--tls-passthrough cannot be combined with --auth") {
		t.Errorf("passthrough + auth should be rejected, got %v", err)
	}
}

func TestParseOpenArgsTLSPassthrough(t *testing.T) {
	cfg := &config.Config{DefaultTTL: "1h"}

	tests := []struct {
		args []string
		want []tunnel.TunnelSpec
	}{
		{[]string{"--port", "8443", "--tls-passthrough"},
			[]tunnel.TunnelSpec{{Port: 8443, TTL: "1h", Mode: client.ModeTLSPassthrough}}},
		// Bool flag before a value flag and a positional port: the bool flag
		// must not swallow the next arg.
		{[]string{"8443", "--tls-passthrough", "--ttl", "2h"},
			[]tunnel.TunnelSpec{{Port: 8443, TTL: "2h", Mode: client.ModeTLSPassthrough}}},
		{[]string{"--tls-passthrough", "-p", "8443:a", "-p", "9443:b"},
			[]tunnel.TunnelSpec{
				{Port: 8443, Name: "a", TTL: "1h", Mode: client.ModeTLSPassthrough},
				{Port: 9443, Name: "b", TTL: "1h", Mode: client.ModeTLSPassthrough},
			}},
		// Without the flag: relay (no mode).
		{[]string{"3000", "--ttl", "30m"},
			[]tunnel.TunnelSpec{{Port: 3000, TTL: "30m"}}},
		{[]string{"--port", "3000", "--auth", "u:p"},
			[]tunnel.TunnelSpec{{Port: 3000, TTL: "1h", AuthUser: "u", AuthPass: "p"}}},
	}
	for _, tt := range tests {
		got, err := parseOpenArgs(cfg, tt.args)
		if err != nil {
			t.Errorf("parseOpenArgs(%v) error: %v", tt.args, err)
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("parseOpenArgs(%v)\n got  %+v\n want %+v", tt.args, got, tt.want)
		}
	}
}

func TestParseOpenArgsPassthroughAuthConflict(t *testing.T) {
	cfg := &config.Config{DefaultTTL: "1h"}
	for _, args := range [][]string{
		{"--port", "8443", "--tls-passthrough", "--auth", "u:p"},
		{"--auth", "u:p", "8443", "--tls-passthrough"},
	} {
		_, err := parseOpenArgs(cfg, args)
		if err == nil || !strings.Contains(err.Error(), "cannot be combined with --auth") {
			t.Errorf("parseOpenArgs(%v) should reject passthrough+auth, got %v", args, err)
		}
	}
}

func TestPassthroughURL(t *testing.T) {
	if got := passthroughURL("http://s.example.test", client.ModeTLSPassthrough); got != "https://s.example.test" {
		t.Errorf("passthrough http URL = %q, want https", got)
	}
	if got := passthroughURL("https://s.example.test", client.ModeTLSPassthrough); got != "https://s.example.test" {
		t.Errorf("passthrough https URL = %q", got)
	}
	if got := passthroughURL("http://s.example.test", ""); got != "http://s.example.test" {
		t.Errorf("relay URL should be unchanged, got %q", got)
	}
}

func TestWriteTunnelListPassthrough(t *testing.T) {
	var buf bytes.Buffer
	writeTunnelList(&buf, []client.Tunnel{
		{ID: "aaaaaaaa-1", Slug: "web", LocalPort: 3000, Mode: "relay"},
		{ID: "bbbbbbbb-2", Slug: "secure", LocalPort: 8443, Mode: client.ModeTLSPassthrough, PublicURL: "https://secure.example.test"},
		{ID: "cccccccc-3", Slug: "legacy", LocalPort: 80},
	})
	out := buf.String()

	for _, want := range []string{
		"tls-passthrough",
		"https://secure.example.test",
		"certificate visitors see is your local service's own",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("list output missing %q:\n%s", want, out)
		}
	}
	// Empty mode displays as relay.
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "cccccccc") && !strings.Contains(line, "relay") {
			t.Errorf("empty mode should display as relay: %q", line)
		}
	}

	// No passthrough tunnels → no note.
	buf.Reset()
	writeTunnelList(&buf, []client.Tunnel{{ID: "x", Slug: "web", LocalPort: 3000, Mode: "relay"}})
	if strings.Contains(buf.String(), "certificate") {
		t.Errorf("relay-only list should not print the passthrough note:\n%s", buf.String())
	}
}
