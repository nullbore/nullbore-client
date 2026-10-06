package client

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestNormalizeFQDN(t *testing.T) {
	cases := map[string]string{
		"_acme-challenge.foo.acct.e2e.nullbore.com.": "_acme-challenge.foo.acct.e2e.nullbore.com",
		"_ACME-Challenge.Acct.E2E.nullbore.com":      "_acme-challenge.acct.e2e.nullbore.com",
		"  _acme-challenge.acct.e2e.nullbore.com.  ": "_acme-challenge.acct.e2e.nullbore.com",
		"_acme-challenge.acct.e2e.nullbore.com..":    "_acme-challenge.acct.e2e.nullbore.com.", // only one dot stripped
		"": "",
	}
	for in, want := range cases {
		if got := NormalizeFQDN(in); got != want {
			t.Errorf("NormalizeFQDN(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPresentDNS01Request(t *testing.T) {
	ts, c := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/acme/dns-01" {
			t.Errorf("got %s %s, want POST /v1/acme/dns-01", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer nbk_test_key" {
			t.Errorf("auth = %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("content-type = %q", got)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["fqdn"] != "_acme-challenge.acct.e2e.nullbore.com" || body["value"] != "-tok_EN" || len(body) != 2 {
			t.Errorf("body = %v", body)
		}
		json.NewEncoder(w).Encode(map[string]string{
			"fqdn": body["fqdn"], "value": body["value"], "expires_at": "2026-10-06T12:00:00Z",
		})
	})
	defer ts.Close()

	rec, err := c.PresentDNS01("_ACME-challenge.acct.e2e.nullbore.com.", "-tok_EN")
	if err != nil {
		t.Fatal(err)
	}
	if rec.ExpiresAt != "2026-10-06T12:00:00Z" || rec.Value != "-tok_EN" {
		t.Errorf("rec = %+v", rec)
	}
}

func TestCleanupDNS01(t *testing.T) {
	for _, deleted := range []bool{true, false} {
		ts, c := newTestServer(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodDelete || r.URL.Path != "/v1/acme/dns-01" {
				t.Errorf("got %s %s, want DELETE /v1/acme/dns-01", r.Method, r.URL.Path)
			}
			var body ACMEDNS01Request
			json.NewDecoder(r.Body).Decode(&body)
			if body.FQDN != "_acme-challenge.acct.e2e.nullbore.com" || body.Value != "v" {
				t.Errorf("body = %+v", body)
			}
			json.NewEncoder(w).Encode(map[string]bool{"deleted": deleted})
		})
		got, err := c.CleanupDNS01("_acme-challenge.acct.e2e.nullbore.com.", "v")
		ts.Close()
		if err != nil {
			t.Fatalf("deleted=%v: %v", deleted, err)
		}
		if got != deleted {
			t.Errorf("deleted = %v, want %v", got, deleted)
		}
	}
}

func TestDNS01ErrorMapping(t *testing.T) {
	cases := []struct {
		status int
		want   string
	}{
		{400, "invalid DNS-01 request: bad fqdn"},
		{401, "API key rejected"},
		{403, "requires a paid plan"},
		{404, "does not support ACME DNS-01"},
		{429, "rate limited"},
		{501, "does not support ACME DNS-01"},
		{502, "could not update DNS"},
	}
	for _, tc := range cases {
		ts, c := newTestServer(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			json.NewEncoder(w).Encode(map[string]string{"error": "bad fqdn"})
		})
		_, err := c.PresentDNS01("x.acct.e2e.nullbore.com", "v")
		_, cerr := c.CleanupDNS01("x.acct.e2e.nullbore.com", "v")
		ts.Close()
		for _, e := range []error{err, cerr} {
			if e == nil || !strings.Contains(e.Error(), tc.want) {
				t.Errorf("status %d: err = %v, want containing %q", tc.status, e, tc.want)
				continue
			}
			var apiErr *APIError
			if !errors.As(e, &apiErr) || apiErr.StatusCode != tc.status {
				t.Errorf("status %d: APIError not reachable via errors.As", tc.status)
			}
		}
	}

	// Unmapped statuses keep the raw error.
	ts, c := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte(`{"error":"boom"}`))
	})
	defer ts.Close()
	if _, err := c.PresentDNS01("x", "v"); err == nil || !strings.Contains(err.Error(), "server error (500)") {
		t.Errorf("500: err = %v", err)
	}
}
