package cli

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"github.com/nullbore/nullbore-client/internal/client"
	"github.com/nullbore/nullbore-client/internal/config"
)

// acmeUsage is the help text for `nullbore acme`.
const acmeUsage = `nullbore acme — ACME DNS-01 hook for trusted end-to-end tunnel certificates

Usage:
  nullbore acme present [--no-wait] [--wait-timeout 120s] <fqdn> <value>
  nullbore acme cleanup <fqdn> <value>

Publishes (present) or removes (cleanup) the _acme-challenge TXT record for a
name under your account's end-to-end zone (<tunnel>.<account>.e2e.nullbore.com
or *.<account>.e2e.nullbore.com), so any ACME client can get a publicly
trusted certificate for a --tls-passthrough tunnel. Only the challenge value
is sent to NullBore: the private key is generated and stays on your machine.
Paid plans only.

The argument order matches lego's exec provider ($EXEC_PATH present|cleanup
<fqdn> <value>). Point EXEC_PATH at a 2-line wrapper script:

  #!/bin/sh
  exec nullbore acme "$@"

  EXEC_PATH=/usr/local/bin/nullbore-acme-hook \
    lego run --accept-tos --path ~/.lego --dns exec --domains '*.ACCOUNT.e2e.nullbore.com'

Flags (present):
  --no-wait            Return as soon as the server accepts the record
  --wait               Wait until 1.1.1.1 and 8.8.8.8 serve the record (default)
  --wait-timeout DUR   Give up waiting after DUR (default 120s); the record
                       stays in place and the command still succeeds

cleanup succeeds even if the record is already gone.
`

const defaultACMEWaitTimeout = 120 * time.Second

// acmeArgs is a parsed `nullbore acme` invocation.
type acmeArgs struct {
	Action      string // "present" or "cleanup"
	FQDN        string // normalized (no trailing dot, lowercase)
	Value       string
	Wait        bool
	WaitTimeout time.Duration
	Help        bool
}

// parseACMEArgs parses `nullbore acme <action> [flags] <fqdn> <value>`.
// Flags are matched by exact name only: ACME challenge values are base64url
// and may begin with "-", so any other dash-prefixed arg is positional. "--"
// ends flag parsing.
func parseACMEArgs(args []string) (acmeArgs, error) {
	a := acmeArgs{Wait: true, WaitTimeout: defaultACMEWaitTimeout}
	if len(args) == 0 {
		return a, fmt.Errorf("missing action (present or cleanup)\n\n%s", acmeUsage)
	}
	switch args[0] {
	case "help", "-h", "--help":
		a.Help = true
		return a, nil
	case "present", "cleanup":
		a.Action = args[0]
	default:
		return a, fmt.Errorf("unknown acme action %q (want present or cleanup; see 'nullbore acme help')", args[0])
	}

	var pos []string
	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		arg := rest[i]
		switch {
		case arg == "--":
			pos = append(pos, rest[i+1:]...)
			i = len(rest)
		case arg == "-h" || arg == "--help":
			a.Help = true
			return a, nil
		case arg == "--wait":
			a.Wait = true
		case arg == "--no-wait":
			a.Wait = false
		case arg == "--wait-timeout" || strings.HasPrefix(arg, "--wait-timeout="):
			v := strings.TrimPrefix(arg, "--wait-timeout=")
			if arg == "--wait-timeout" {
				if i+1 >= len(rest) {
					return a, fmt.Errorf("--wait-timeout needs a duration (e.g. 120s)")
				}
				i++
				v = rest[i]
			}
			d, err := time.ParseDuration(v)
			if err != nil || d <= 0 {
				return a, fmt.Errorf("invalid --wait-timeout %q (e.g. 90s, 3m)", v)
			}
			a.WaitTimeout = d
		default:
			pos = append(pos, arg)
		}
	}

	if len(pos) != 2 {
		return a, fmt.Errorf("acme %s needs exactly 2 arguments: <fqdn> <value> (got %d)", a.Action, len(pos))
	}
	a.FQDN = client.NormalizeFQDN(pos[0])
	a.Value = strings.TrimSpace(pos[1])
	if a.FQDN == "" {
		return a, fmt.Errorf("fqdn must not be empty")
	}
	if a.Value == "" {
		return a, fmt.Errorf("value must not be empty")
	}
	return a, nil
}

// txtLookup returns the TXT records for a name from one resolver.
type txtLookup func(ctx context.Context, name string) ([]string, error)

// publicResolvers are queried after present so the record is known to be
// visible from outside before the ACME client asks the CA to validate.
var publicResolvers = []string{"1.1.1.1:53", "8.8.8.8:53"}

// resolverLookup returns a txtLookup that queries one DNS server directly,
// bypassing the system resolver and its caches.
func resolverLookup(server string) txtLookup {
	r := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: 5 * time.Second}
			return d.DialContext(ctx, network, server)
		},
	}
	return func(ctx context.Context, name string) ([]string, error) {
		// Trailing dot: absolute name, so no search-domain expansion.
		return r.LookupTXT(ctx, name+".")
	}
}

// acmeDeps holds the side-effecting pieces of runACME, injectable for tests.
type acmeDeps struct {
	lookups  []txtLookup
	interval time.Duration
	out      io.Writer
}

func defaultACMEDeps() acmeDeps {
	d := acmeDeps{interval: 3 * time.Second, out: os.Stderr}
	for _, s := range publicResolvers {
		d.lookups = append(d.lookups, resolverLookup(s))
	}
	return d
}

func cmdACME(cfg *config.Config, args []string) error {
	a, err := parseACMEArgs(args)
	if err != nil {
		return err
	}
	if a.Help {
		fmt.Print(acmeUsage)
		return nil
	}
	if err := requireKey(cfg); err != nil {
		return err
	}
	return runACME(client.New(cfg), a, defaultACMEDeps())
}

// runACME performs a parsed acme action. Progress goes to deps.out (stderr in
// the CLI) so stdout stays clean for the ACME client.
func runACME(c *client.Client, a acmeArgs, deps acmeDeps) error {
	switch a.Action {
	case "cleanup":
		deleted, err := c.CleanupDNS01(a.FQDN, a.Value)
		if err != nil {
			return fmt.Errorf("acme cleanup %s: %w", a.FQDN, err)
		}
		if deleted {
			fmt.Fprintf(deps.out, "nullbore acme: removed TXT %s\n", a.FQDN)
		} else {
			fmt.Fprintf(deps.out, "nullbore acme: TXT %s already absent (nothing to remove)\n", a.FQDN)
		}
		return nil
	case "present":
		rec, err := c.PresentDNS01(a.FQDN, a.Value)
		if err != nil {
			return fmt.Errorf("acme present %s: %w", a.FQDN, err)
		}
		msg := fmt.Sprintf("nullbore acme: published TXT %s", a.FQDN)
		if rec.ExpiresAt != "" {
			msg += " (expires " + rec.ExpiresAt + ")"
		}
		fmt.Fprintln(deps.out, msg)
		if !a.Wait {
			return nil
		}
		fmt.Fprintf(deps.out, "nullbore acme: waiting up to %s for public DNS to serve it...\n", a.WaitTimeout)
		ctx, cancel := context.WithTimeout(context.Background(), a.WaitTimeout)
		defer cancel()
		if err := waitForTXT(ctx, deps.lookups, a.FQDN, a.Value, deps.interval); err != nil {
			// The record exists; the CA queries authoritative DNS and may
			// still see it, so don't fail the ACME run here.
			fmt.Fprintf(deps.out, "nullbore acme: warning: %v — continuing anyway\n", err)
			return nil
		}
		fmt.Fprintln(deps.out, "nullbore acme: record is visible on public DNS")
		return nil
	default:
		return fmt.Errorf("unknown acme action %q", a.Action)
	}
}

// waitForTXT polls every lookup until each one returns value among the TXT
// records for fqdn, or ctx ends. Lookup errors (e.g. NXDOMAIN before the
// record propagates) count as "not yet".
func waitForTXT(ctx context.Context, lookups []txtLookup, fqdn, value string, interval time.Duration) error {
	if len(lookups) == 0 {
		return nil
	}
	pending := make([]bool, len(lookups))
	for i := range pending {
		pending[i] = true
	}
	for {
		remaining := 0
		for i, lookup := range lookups {
			if !pending[i] {
				continue
			}
			if txtContains(ctx, lookup, fqdn, value) {
				pending[i] = false
			} else {
				remaining++
			}
		}
		if remaining == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("TXT %s not visible on %d of %d public resolvers before timeout", fqdn, remaining, len(lookups))
		case <-time.After(interval):
		}
	}
}

func txtContains(ctx context.Context, lookup txtLookup, fqdn, value string) bool {
	records, err := lookup(ctx, fqdn)
	if err != nil {
		return false
	}
	for _, r := range records {
		if r == value {
			return true
		}
	}
	return false
}
