package cli

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
	"text/tabwriter"

	"github.com/nullbore/nullbore-client/internal/client"
	"github.com/nullbore/nullbore-client/internal/config"
	"github.com/nullbore/nullbore-client/internal/daemon"
	"github.com/nullbore/nullbore-client/internal/tunnel"
	"github.com/nullbore/nullbore-client/internal/update"
)

// version is set at build time via -ldflags for releases.
var version = "0.1.0-dev"

func Run(args []string) error {
	if len(args) == 0 {
		return printUsage()
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	switch args[0] {
	case "open":
		return cmdOpen(cfg, args[1:])
	case "list":
		return cmdList(cfg)
	case "close":
		return cmdClose(cfg, args[1:])
	case "requests":
		return cmdRequests(cfg, args[1:])
	case "status":
		return cmdStatus(cfg)
	case "daemon":
		return cmdDaemon(cfg)
	case "update":
		return cmdUpdate(args[1:])
	case "device":
		return cmdDevice(cfg, args[1:])
	case "version":
		fmt.Printf("nullbore %s\n", version)
		checkUpdateQuiet()
		return nil
	case "_generate-docs":
		fmt.Print(GenerateDocs())
		return nil
	case "help", "--help", "-h":
		return printUsage()
	default:
		return fmt.Errorf("unknown command: %s (try 'nullbore help')", args[0])
	}
}

// portList implements flag.Value for repeatable --port flags.
// sanitizeHostname converts a hostname into a valid tunnel name component.
// Lowercases, replaces non-alphanumeric with hyphens, trims, truncates to 30 chars.
func sanitizeHostname(h string) string {
	h = strings.ToLower(h)
	var b strings.Builder
	for _, c := range h {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			b.WriteRune(c)
		} else {
			b.WriteByte('-')
		}
	}
	result := strings.Trim(b.String(), "-")
	// Collapse consecutive hyphens
	for strings.Contains(result, "--") {
		result = strings.ReplaceAll(result, "--", "-")
	}
	if len(result) > 30 {
		result = result[:30]
	}
	return strings.Trim(result, "-")
}

type portList []tunnel.TunnelSpec

func (p *portList) String() string { return fmt.Sprint(*p) }
func (p *portList) Set(val string) error {
	// Parse "port" or "port:name"
	parts := strings.SplitN(val, ":", 2)
	port, err := strconv.Atoi(parts[0])
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("invalid port: %s", parts[0])
	}
	spec := tunnel.TunnelSpec{Port: port}
	if len(parts) == 2 {
		spec.Name = parts[1]
	}
	*p = append(*p, spec)
	return nil
}

// requireKey checks that an API key is configured and returns a clear error if not.
func requireKey(cfg *config.Config) error {
	if cfg.Token() == "" {
		return fmt.Errorf("no API key configured\n\n  Set your key in one of:\n    1. ~/.nullbore/config.toml → api_key = \"nbk_...\"\n    2. Environment variable   → export NULLBORE_API_KEY=\"nbk_...\"\n\n  Get a key at https://nullbore.com/dashboard")
	}
	return nil
}

func cmdOpen(cfg *config.Config, args []string) error {
	if err := requireKey(cfg); err != nil {
		return err
	}
	fs := flag.NewFlagSet("open", flag.ExitOnError)

	// Support both old-style --port N and new repeatable -p PORT:NAME
	singlePort := fs.Int("port", 0, "Local port to expose (single tunnel)")
	name := fs.String("name", "", "Tunnel name (single tunnel mode)")
	ttl := fs.String("ttl", cfg.DefaultTTL, "Time-to-live (e.g. 30m, 2h)")

	var ports portList
	fs.Var(&ports, "p", "Port to expose, repeatable. Format: PORT or PORT:NAME (e.g. -p 3000:api -p 8080:web)")

	fs.Parse(args)

	// Also accept positional args as ports (e.g. `nullbore open 3000 8080`)
	for _, arg := range fs.Args() {
		if err := ports.Set(arg); err == nil {
			continue
		}
	}

	// Merge single --port into the list
	if *singlePort > 0 {
		spec := tunnel.TunnelSpec{Port: *singlePort, Name: *name}
		ports = append([]tunnel.TunnelSpec{spec}, ports...)
	}

	if len(ports) == 0 {
		return fmt.Errorf("at least one port is required\n\nUsage:\n  nullbore open --port 3000\n  nullbore open -p 3000:api -p 8080:web\n  nullbore open 3000 8080")
	}

	// Set TTL on all specs, auto-generate name from hostname if not set
	hostname, _ := os.Hostname()
	for i := range ports {
		ports[i].TTL = *ttl
		if ports[i].Name == "" && hostname != "" {
			// Auto-name: sanitize hostname to valid tunnel name chars
			autoName := sanitizeHostname(hostname)
			if autoName != "" {
				ports[i].Name = fmt.Sprintf("%s-%d", autoName, ports[i].Port)
			}
		}
	}

	c := client.New(cfg)
	mgr := tunnel.NewManager(cfg, c)

	// Open all tunnels
	fmt.Printf("opening %d tunnel(s)...\n\n", len(ports))

	for _, spec := range ports {
		at, err := mgr.OpenTunnel(spec)
		if err != nil {
			// Close any already-opened tunnels
			mgr.Close()
			return err
		}

		label := at.Slug
		if spec.Name != "" {
			label = spec.Name
		}
		fmt.Printf("  ✓ %s → localhost:%d\n", at.PublicURL, spec.Port)
		_ = label
	}

	fmt.Printf("\n  %d tunnel(s) active — press Ctrl+C to close\n\n", len(ports))

	// Handle graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	errCh := make(chan error, 1)
	go func() {
		errCh <- mgr.Run()
	}()

	select {
	case <-sigCh:
		fmt.Println("\nclosing tunnels...")
		mgr.Close()
		return nil
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("tunnel error: %w", err)
		}
		return nil
	}
}

func cmdList(cfg *config.Config) error {
	if err := requireKey(cfg); err != nil {
		return err
	}
	c := client.New(cfg)
	tunnels, err := c.ListTunnels()
	if err != nil {
		return err
	}

	if len(tunnels) == 0 {
		fmt.Println("no active tunnels")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSLUG\tPORT\tMODE\tEXPIRES")
	for _, t := range tunnels {
		id := t.ID
		if len(id) > 8 {
			id = id[:8]
		}
		fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\n",
			id, t.Slug, t.LocalPort, t.Mode, t.ExpiresAt)
	}
	w.Flush()
	return nil
}

func cmdClose(cfg *config.Config, args []string) error {
	if err := requireKey(cfg); err != nil {
		return err
	}
	if len(args) == 0 {
		return fmt.Errorf("usage: nullbore close <tunnel-id-or-name>")
	}

	target := args[0]
	c := client.New(cfg)

	// Try direct ID first, then search by slug/name
	if err := c.CloseTunnel(target); err != nil {
		// Try listing and matching by slug
		tunnels, listErr := c.ListTunnels()
		if listErr != nil {
			return err // return original error
		}
		for _, t := range tunnels {
			if t.Slug == target || t.Name == target {
				if closeErr := c.CloseTunnel(t.ID); closeErr != nil {
					return closeErr
				}
				fmt.Printf("tunnel %s closed\n", target)
				return nil
			}
		}
		return fmt.Errorf("tunnel %q not found", target)
	}

	fmt.Printf("tunnel %s closed\n", target)
	return nil
}

func cmdRequests(cfg *config.Config, args []string) error {
	if err := requireKey(cfg); err != nil {
		return err
	}
	fs := flag.NewFlagSet("requests", flag.ExitOnError)
	limit := fs.Int("limit", 20, "Number of requests to show")
	fs.Parse(args)

	if fs.NArg() == 0 {
		return fmt.Errorf("usage: nullbore requests <tunnel-id-or-slug>\n\nShow recent requests for a tunnel. Get tunnel IDs from 'nullbore list'.")
	}

	target := fs.Arg(0)
	c := client.New(cfg)

	// Try to find tunnel by slug if it doesn't look like a UUID
	tunnelID := target
	if !strings.Contains(target, "-") || len(target) < 20 {
		// Probably a slug — find the ID
		tunnels, err := c.ListTunnels()
		if err != nil {
			return fmt.Errorf("listing tunnels: %w", err)
		}
		found := false
		for _, t := range tunnels {
			if t.Slug == target || t.Name == target {
				tunnelID = t.ID
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("tunnel %q not found", target)
		}
	}

	logs, err := c.ListRequests(tunnelID, *limit)
	if err != nil {
		return fmt.Errorf("fetching requests: %w", err)
	}

	if len(logs) == 0 {
		fmt.Println("no requests recorded yet")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TIME\tMETHOD\tPATH\tBODY\tFROM")
	for _, r := range logs {
		ts := r.CreatedAt
		if len(ts) > 19 {
			ts = ts[:19]
		}
		bodyInfo := fmt.Sprintf("%d B", r.BodySize)
		if r.BodySize > 1024 {
			bodyInfo = fmt.Sprintf("%.1f KB", float64(r.BodySize)/1024)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", ts, r.Method, r.Path, bodyInfo, r.RemoteIP)
	}
	w.Flush()
	return nil
}

func cmdStatus(cfg *config.Config) error {
	c := client.New(cfg)
	health, err := c.Health()
	if err != nil {
		return fmt.Errorf("server unreachable: %w", err)
	}

	fmt.Printf("server:  %s\n", cfg.ServerURL())
	fmt.Printf("status:  %s\n", health["status"])
	fmt.Printf("version: %s\n", health["version"])

	if cfg.Token() != "" {
		fmt.Printf("auth:    configured\n")
	} else {
		log.Printf("auth:    not configured (set api_key in ~/.nullbore/config.toml)")
	}

	return nil
}

func cmdDaemon(cfg *config.Config) error {
	// Static tunnel mode: NULLBORE_TUNNELS=host:port:slug,host:port:slug,...
	if tunnelEnv := os.Getenv("NULLBORE_TUNNELS"); tunnelEnv != "" {
		return runStaticTunnels(cfg, tunnelEnv)
	}

	if cfg.Token() == "" {
		return fmt.Errorf("API key required for daemon mode. Set api_key in config or NULLBORE_API_KEY env")
	}

	d := daemon.New(cfg)

	// Graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		log.Printf("shutting down daemon...")
		d.Stop()
		os.Exit(0)
	}()

	return d.Run()
}

// runStaticTunnels opens tunnels from NULLBORE_TUNNELS env var.
// Format: host:port:slug,host:port:slug,...
// Examples: gramps:5000:gramps-web,openclaw:8080:my-claw
//           localhost:3000:api (equivalent to just port 3000)
func runStaticTunnels(cfg *config.Config, spec string) error {
	if cfg.Token() == "" {
		return fmt.Errorf("API key required. Set NULLBORE_API_KEY")
	}

	apiClient := client.New(cfg)
	mgr := tunnel.NewManager(cfg, apiClient)

	entries := strings.Split(spec, ",")
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}

		parts := strings.SplitN(entry, ":", 3)
		var host, slug string
		var port int

		switch len(parts) {
		case 1:
			// Just a port: "3000"
			p, err := strconv.Atoi(parts[0])
			if err != nil {
				return fmt.Errorf("invalid tunnel spec %q: %w", entry, err)
			}
			port = p
		case 2:
			// port:slug or host:port
			p, err := strconv.Atoi(parts[0])
			if err != nil {
				// host:port
				host = parts[0]
				p2, err := strconv.Atoi(parts[1])
				if err != nil {
					return fmt.Errorf("invalid tunnel spec %q: expected host:port", entry)
				}
				port = p2
			} else {
				// port:slug
				port = p
				slug = parts[1]
			}
		case 3:
			// host:port:slug
			host = parts[0]
			p, err := strconv.Atoi(parts[1])
			if err != nil {
				return fmt.Errorf("invalid tunnel spec %q: port must be a number", entry)
			}
			port = p
			slug = parts[2]
		default:
			return fmt.Errorf("invalid tunnel spec %q", entry)
		}

		s := tunnel.TunnelSpec{
			Port: port,
			Host: host,
			Name: slug,
			TTL:  cfg.DefaultTTL,
		}

		at, err := mgr.OpenTunnel(s)
		if err != nil {
			return fmt.Errorf("opening tunnel %q: %w", entry, err)
		}

		target := "localhost"
		if host != "" {
			target = host
		}
		log.Printf("tunnel open: %s → %s:%d", at.PublicURL, target, port)
	}

	// Graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		log.Printf("shutting down tunnels...")
		mgr.Close()
		os.Exit(0)
	}()

	return mgr.Run()
}

func cmdDevice(cfg *config.Config, args []string) error {
	if len(args) == 0 {
		// Show device info
		hostname, _ := os.Hostname()
		fmt.Printf("Device ID:   %s\n", cfg.DeviceID)
		fmt.Printf("Hostname:    %s\n", hostname)
		return nil
	}

	switch args[0] {
	case "info":
		hostname, _ := os.Hostname()
		fmt.Printf("Device ID:   %s\n", cfg.DeviceID)
		fmt.Printf("Hostname:    %s\n", hostname)
		return nil

	case "takeover":
		requireKey(cfg)
		c := client.New(cfg)
		hostname, _ := os.Hostname()

		fmt.Printf("Taking over API key for this device (%s)...\n", hostname)

		// Set takeover flag — client.do will send device headers,
		// we just need to also send the takeover header
		c.SetTakeover(true)
		_, err := c.ListTunnels()
		c.SetTakeover(false)
		if err != nil {
			return fmt.Errorf("takeover failed: %w", err)
		}

		fmt.Println("✅ Device takeover complete. This machine is now bound to your API key.")
		return nil

	default:
		return fmt.Errorf("unknown device command: %s (try: info, takeover)", args[0])
	}
}

func cmdUpdate(args []string) error {
	fs := flag.NewFlagSet("update", flag.ExitOnError)
	checkOnly := fs.Bool("check", false, "Only check for updates, don't install")
	fs.Parse(args)

	fmt.Printf("nullbore %s\n", version)
	fmt.Println("Checking for updates...")

	rel, err := update.CheckLatest()
	if err != nil {
		return fmt.Errorf("update check failed: %w", err)
	}

	if !update.IsNewer(version, rel.TagName) {
		fmt.Println("✓ You're on the latest version")
		return nil
	}

	fmt.Printf("  New version available: %s\n", rel.TagName)

	if *checkOnly {
		fmt.Printf("  Download: %s\n", rel.HTMLURL)
		return nil
	}

	downloadURL, err := update.FindAsset(rel)
	if err != nil {
		return err
	}

	fmt.Printf("  Downloading %s...\n", update.AssetName())
	tmpPath, err := update.Download(downloadURL)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}

	fmt.Println("  Installing...")
	if err := update.ReplaceBinary(tmpPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("install failed: %w", err)
	}

	fmt.Printf("  ✅ Updated to %s\n", rel.TagName)
	return nil
}

// checkUpdateQuiet runs a non-blocking version check and prints a hint if outdated.
func checkUpdateQuiet() {
	done := make(chan struct{})
	go func() {
		defer close(done)
		rel, err := update.CheckLatest()
		if err != nil {
			return
		}
		if update.IsNewer(version, rel.TagName) {
			fmt.Printf("  → Update available: %s (run 'nullbore update')\n", rel.TagName)
		}
	}()

	// Wait up to 2 seconds for the check
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}
}

func printUsage() error {
	fmt.Print(`nullbore — on-demand tunnel client

Usage:
  nullbore open --port <port> [--name <name>] [--ttl <duration>]
  nullbore open -p <port>[:<name>] [-p <port>[:<name>] ...]
  nullbore open <port> [<port> ...]
  nullbore daemon                             # persistent mode from config.toml
  nullbore device                             # show device info
  nullbore device takeover                    # rebind API key to this device
  nullbore update                             # check for updates and self-update
  nullbore update --check                     # check only, don't install
  nullbore list
  nullbore requests <tunnel-or-slug>          # inspect incoming HTTP requests
  nullbore close <tunnel-id-or-name>
  nullbore status
  nullbore version

Examples:
  nullbore open --port 3000                  # single tunnel
  nullbore open -p 3000:api -p 8080:web      # multiple named tunnels
  nullbore open 3000 8080 5432               # multiple tunnels (positional)

Configuration:
  ~/.nullbore/config.toml
  
  server = "https://tunnel.nullbore.com"
  api_key = "nbk_..."
  default_ttl = "1h"

Environment:
  NULLBORE_SERVER             Override server URL
  NULLBORE_API_KEY            Override API key
  NULLBORE_DASHBOARD          Override dashboard URL
  NULLBORE_TLS_SKIP_VERIFY    Skip TLS verification (1/true)

Daemon mode:
  Reads tunnel definitions from ~/.nullbore/config.toml and keeps them
  open persistently. Watches config for changes (hot-reload every 10s).
  One-off 'nullbore open' commands coexist on the same device.

  Config example:
    [[tunnels]]
    port = 3000
    name = "my-api"
    ttl = "2h"

    [[tunnels]]
    port = 5432
    name = "postgres"
    subdomain = "db"
`)
	return nil
}
