package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestRunNoArgs(t *testing.T) {
	// Should print usage, not error
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
	// open without --port should error
	err := Run([]string{"open"})
	if err == nil {
		t.Error("Run([open]) without --port should error")
	}
}

func TestRunStatus(t *testing.T) {
	// Start a mock server
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
	defer os.Unsetenv("NULLBORE_SERVER")

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
	defer os.Unsetenv("NULLBORE_SERVER")

	err := Run([]string{"close", "test-id"})
	if err != nil {
		t.Errorf("Run([close, test-id]) error: %v", err)
	}
}

func TestRunCloseNoArg(t *testing.T) {
	err := Run([]string{"close"})
	if err == nil {
		t.Error("Run([close]) without arg should error")
	}
}
