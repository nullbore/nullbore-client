package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestRunNoArgs(t *testing.T) {
	err := Run([]string{})
	if err != nil {
		t.Errorf("Run([]) should not error: %v", err)
	}
}

func TestRunHelp(t *testing.T) {
	for _, cmd := range []string{"help", "--help", "-h"} {
		err := Run([]string{cmd})
		if err != nil {
			t.Errorf("Run([%q]) should not error: %v", cmd, err)
		}
	}
}

func TestRunVersion(t *testing.T) {
	err := Run([]string{"version"})
	if err != nil {
		t.Errorf("Run([version]) error: %v", err)
	}
}

func TestRunUnknownCommand(t *testing.T) {
	err := Run([]string{"bogus"})
	if err == nil {
		t.Error("Run([bogus]) should error")
	}
}

func TestRunOpenNoPort(t *testing.T) {
	err := Run([]string{"open"})
	if err == nil {
		t.Error("Run([open]) without --port should error")
	}
}

func TestRunStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status":  "ok",
			"version": "0.1.0",
		})
	}))
	defer ts.Close()

	os.Setenv("NULLBORE_SERVER", ts.URL)
	os.Setenv("NULLBORE_API_KEY", "test_key")
	defer os.Unsetenv("NULLBORE_SERVER")
	defer os.Unsetenv("NULLBORE_API_KEY")

	err := Run([]string{"status"})
	if err != nil {
		t.Errorf("Run([status]) error: %v", err)
	}
}

func TestRunList(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]map[string]interface{}{})
	}))
	defer ts.Close()

	os.Setenv("NULLBORE_SERVER", ts.URL)
	os.Setenv("NULLBORE_API_KEY", "test_key")
	defer os.Unsetenv("NULLBORE_SERVER")
	defer os.Unsetenv("NULLBORE_API_KEY")

	err := Run([]string{"list"})
	if err != nil {
		t.Errorf("Run([list]) error: %v", err)
	}
}

func TestRunListWithTunnels(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]map[string]interface{}{
			{
				"id":         "abcdef1234567890",
				"slug":       "test-tunnel",
				"local_port": 3000,
				"mode":       "relay",
				"expires_at": "2026-03-30T01:00:00Z",
			},
		})
	}))
	defer ts.Close()

	os.Setenv("NULLBORE_SERVER", ts.URL)
	os.Setenv("NULLBORE_API_KEY", "test_key")
	defer os.Unsetenv("NULLBORE_SERVER")
	defer os.Unsetenv("NULLBORE_API_KEY")

	err := Run([]string{"list"})
	if err != nil {
		t.Errorf("Run([list]) error: %v", err)
	}
}

func TestRunClose(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	os.Setenv("NULLBORE_SERVER", ts.URL)
	os.Setenv("NULLBORE_API_KEY", "test_key")
	defer os.Unsetenv("NULLBORE_SERVER")
	defer os.Unsetenv("NULLBORE_API_KEY")

	err := Run([]string{"close", "test-id"})
	if err != nil {
		t.Errorf("Run([close, test-id]) error: %v", err)
	}
}

func TestRunCloseNoArg(t *testing.T) {
	os.Setenv("NULLBORE_API_KEY", "test_key")
	defer os.Unsetenv("NULLBORE_API_KEY")
	err := Run([]string{"close"})
	if err == nil {
		t.Error("Run([close]) without arg should error")
	}
}

func TestNoAPIKeyError(t *testing.T) {
	// Isolate from any real config files
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	origXDG := os.Getenv("XDG_CONFIG_HOME")
	os.Setenv("HOME", tmpDir)
	os.Unsetenv("XDG_CONFIG_HOME")
	os.Unsetenv("NULLBORE_API_KEY")
	defer func() {
		os.Setenv("HOME", origHome)
		if origXDG != "" {
			os.Setenv("XDG_CONFIG_HOME", origXDG)
		} else {
			os.Unsetenv("XDG_CONFIG_HOME")
		}
	}()

	for _, cmd := range [][]string{{"list"}, {"open", "3000"}, {"close", "x"}} {
		err := Run(cmd)
		if err == nil {
			t.Errorf("Run(%v) should error without API key", cmd)
		}
		if !strings.Contains(err.Error(), "no API key configured") {
			t.Errorf("Run(%v) error should mention missing key, got: %v", cmd, err)
		}
	}
}

func TestPortListParsing(t *testing.T) {
	var pl portList

	// Simple port
	if err := pl.Set("3000"); err != nil {
		t.Errorf("Set(3000) error: %v", err)
	}
	if len(pl) != 1 || pl[0].Port != 3000 || pl[0].Name != "" {
		t.Errorf("Set(3000) = %+v, want port=3000 name=''", pl[0])
	}

	// Port with name
	if err := pl.Set("8080:web"); err != nil {
		t.Errorf("Set(8080:web) error: %v", err)
	}
	if len(pl) != 2 || pl[1].Port != 8080 || pl[1].Name != "web" {
		t.Errorf("Set(8080:web) = %+v, want port=8080 name='web'", pl[1])
	}

	// Invalid port
	if err := pl.Set("abc"); err == nil {
		t.Error("Set(abc) should error")
	}

	// Port out of range
	if err := pl.Set("99999"); err == nil {
		t.Error("Set(99999) should error")
	}

	// Zero port
	if err := pl.Set("0"); err == nil {
		t.Error("Set(0) should error")
	}
}

func TestSanitizeHostname(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"pjs-macbook", "pjs-macbook"},
		{"PJs-MacBook-Pro.local", "pjs-macbook-pro-local"},
		{"MY_WORK_PC", "my-work-pc"},
		{"server.example.com", "server-example-com"},
		{"simple", "simple"},
		{"ALLCAPS", "allcaps"},
		{"with spaces", "with-spaces"},
		{"--leading-trailing--", "leading-trailing"},
		{"a", "a"},
		{"", ""},
		{"host--name", "host-name"},
		{"this-is-a-really-long-hostname-that-exceeds-thirty-characters", "this-is-a-really-long-hostname"},
		{"café-résumé", "caf-r-sum"},
		{"192.168.1.1", "192-168-1-1"},
	}

	for _, tt := range tests {
		got := sanitizeHostname(tt.input)
		if got != tt.expected {
			t.Errorf("sanitizeHostname(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}
