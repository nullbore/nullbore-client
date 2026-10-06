package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nullbore/nullbore-client/internal/client"
	"github.com/nullbore/nullbore-client/internal/config"
)

func TestParseACMEArgs(t *testing.T) {
	a, err := parseACMEArgs([]string{"present", "_ACME-challenge.Acct.e2e.nullbore.com.", "tok"})
	if err != nil {
		t.Fatal(err)
	}
	if a.Action != "present" || a.FQDN != "_acme-challenge.acct.e2e.nullbore.com" || a.Value != "tok" {
		t.Errorf("parsed = %+v", a)
	}
	if !a.Wait || a.WaitTimeout != defaultACMEWaitTimeout {
		t.Errorf("wait defaults = %v %v", a.Wait, a.WaitTimeout)
	}

	// Flags anywhere; a value starting with "-" (base64url) stays positional.
	a, err = parseACMEArgs([]string{"present", "--no-wait", "f.example.", "-abc_DEF", "--wait-timeout=30s"})
	if err != nil {
		t.Fatal(err)
	}
	if a.Wait || a.WaitTimeout != 30*time.Second || a.Value != "-abc_DEF" || a.FQDN != "f.example" {
		t.Errorf("parsed = %+v", a)
	}
	a, err = parseACMEArgs([]string{"cleanup", "--wait-timeout", "1m", "--", "f", "--no-wait"})
	if err != nil {
		t.Fatal(err)
	}
	if a.Action != "cleanup" || a.Value != "--no-wait" || a.WaitTimeout != time.Minute {
		t.Errorf("parsed = %+v", a)
	}

	for _, h := range [][]string{{"help"}, {"-h"}, {"present", "--help"}} {
		if a, err := parseACMEArgs(h); err != nil || !a.Help {
			t.Errorf("%v: help=%v err=%v", h, a.Help, err)
		}
	}

	bad := map[string][]string{
		"missing action":  {},
		"unknown action":  {"renew", "f", "v"},
		"no args":         {"present"},
		"one arg":         {"cleanup", "f"},
		"three args":      {"present", "f", "v", "x"},
		"empty fqdn":      {"present", ".", "v"},
		"empty value":     {"present", "f", " "},
		"bad timeout":     {"present", "--wait-timeout", "soon", "f", "v"},
		"missing timeout": {"present", "f", "v", "--wait-timeout"},
		"zero timeout":    {"present", "--wait-timeout=0s", "f", "v"},
	}
	for name, args := range bad {
		if _, err := parseACMEArgs(args); err == nil {
			t.Errorf("%s: expected error for %v", name, args)
		}
	}
	if _, err := parseACMEArgs([]string{"renew"}); err == nil || !strings.Contains(err.Error(), "unknown acme action") {
		t.Errorf("unknown action err = %v", err)
	}
}

func staticLookup(records ...string) txtLookup {
	return func(context.Context, string) ([]string, error) { return records, nil }
}

func TestWaitForTXTFound(t *testing.T) {
	var calls int32
	// Appears on the third poll; errors (NXDOMAIN) before that.
	delayed := func(_ context.Context, name string) ([]string, error) {
		if name != "f.example" {
			t.Errorf("lookup name = %q", name)
		}
		if atomic.AddInt32(&calls, 1) < 3 {
			return nil, errors.New("no such host")
		}
		return []string{"other", "want"}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := waitForTXT(ctx, []txtLookup{staticLookup("want"), delayed}, "f.example", "want", time.Millisecond)
	if err != nil {
		t.Fatalf("waitForTXT: %v", err)
	}
	if calls != 3 {
		t.Errorf("delayed lookup called %d times, want 3", calls)
	}
}

func TestWaitForTXTTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	err := waitForTXT(ctx, []txtLookup{staticLookup("want"), staticLookup("stale")}, "f.example", "want", 5*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "1 of 2") {
		t.Fatalf("err = %v, want timeout naming 1 of 2 resolvers", err)
	}
}

func newACMETestClient(t *testing.T, h http.HandlerFunc) *client.Client {
	t.Helper()
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return client.New(&config.Config{Server: ts.URL, APIKey: "nbk_test"})
}

func TestRunACMEPresentWaits(t *testing.T) {
	c := newACMETestClient(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"fqdn": "f", "value": "v", "expires_at": "soon"})
	})
	var out bytes.Buffer
	a := acmeArgs{Action: "present", FQDN: "f", Value: "v", Wait: true, WaitTimeout: time.Second}

	err := runACME(c, a, acmeDeps{lookups: []txtLookup{staticLookup("v")}, interval: time.Millisecond, out: &out})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "visible on public DNS") {
		t.Errorf("out = %q", out.String())
	}

	// Timeout is a warning, not a failure: the record exists.
	out.Reset()
	a.WaitTimeout = 20 * time.Millisecond
	err = runACME(c, a, acmeDeps{lookups: []txtLookup{staticLookup()}, interval: time.Millisecond, out: &out})
	if err != nil {
		t.Fatalf("timeout should not fail: %v", err)
	}
	if !strings.Contains(out.String(), "warning") {
		t.Errorf("out = %q, want warning", out.String())
	}

	// --no-wait never queries DNS.
	out.Reset()
	a.Wait = false
	never := func(context.Context, string) ([]string, error) {
		t.Error("lookup called with --no-wait")
		return nil, nil
	}
	if err := runACME(c, a, acmeDeps{lookups: []txtLookup{never}, out: &out}); err != nil {
		t.Fatal(err)
	}
}

func TestRunACMEErrors(t *testing.T) {
	c := newACMETestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":"not your account"}`))
	})
	var out bytes.Buffer
	err := runACME(c, acmeArgs{Action: "present", FQDN: "f", Value: "v", Wait: true}, acmeDeps{out: &out})
	if err == nil || !strings.Contains(err.Error(), "not your account") || !strings.Contains(err.Error(), "paid plan") {
		t.Errorf("err = %v", err)
	}
}

func TestRunACMECleanupMissingSucceeds(t *testing.T) {
	c := newACMETestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %s", r.Method)
		}
		w.Write([]byte(`{"deleted": false}`))
	})
	var out bytes.Buffer
	if err := runACME(c, acmeArgs{Action: "cleanup", FQDN: "f", Value: "v"}, acmeDeps{out: &out}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "already absent") {
		t.Errorf("out = %q", out.String())
	}
}

// End-to-end through Run: config/env plumbing, normalization, --no-wait.
func TestRunACMECommand(t *testing.T) {
	var got client.ACMEDNS01Request
	var method string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		if r.URL.Path != "/v1/acme/dns-01" || r.Header.Get("Authorization") != "Bearer nbk_run" {
			t.Errorf("path=%s auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"fqdn":"x","value":"y","expires_at":"z","deleted":true}`))
	}))
	defer ts.Close()
	t.Setenv("NULLBORE_SERVER", ts.URL)
	t.Setenv("NULLBORE_API_KEY", "nbk_run")

	if err := Run([]string{"acme", "present", "--no-wait", "_acme-challenge.A.e2e.nullbore.com.", "tok"}); err != nil {
		t.Fatal(err)
	}
	if method != http.MethodPost || got.FQDN != "_acme-challenge.a.e2e.nullbore.com" || got.Value != "tok" {
		t.Errorf("present: %s %+v", method, got)
	}
	if err := Run([]string{"acme", "cleanup", "_acme-challenge.a.e2e.nullbore.com", "tok"}); err != nil {
		t.Fatal(err)
	}
	if method != http.MethodDelete {
		t.Errorf("cleanup method = %s", method)
	}
	if err := Run([]string{"acme", "help"}); err != nil {
		t.Errorf("acme help: %v", err)
	}
	if err := Run([]string{"acme", "bogus", "f", "v"}); err == nil {
		t.Error("unknown action should fail")
	}
}

func TestACMEDocs(t *testing.T) {
	docs := GenerateDocs()
	for _, want := range []string{"## `nullbore acme`", `exec nullbore acme "$@"`, "stays on your machine", "--no-wait"} {
		if !strings.Contains(docs, want) {
			t.Errorf("generated docs missing %q", want)
		}
	}
}
