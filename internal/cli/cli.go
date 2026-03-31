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
	"text/tabwriter"

	"github.com/nullbore/nullbore-client/internal/client"
	"github.com/nullbore/nullbore-client/internal/config"
	"github.com/nullbore/nullbore-client/internal/daemon"
	"github.com/nullbore/nullbore-client/internal/tunnel"
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
	case "status":
		return cmdStatus(cfg)
	case "daemon":
		return cmdDaemon(cfg)
	case "version":
		fmt.Printf("nullbore %s\n", version)
		return nil
	case "help", "--help", "-h":
		return printUsage()
	default:
		return fmt.Errorf("unknown command: %s (try 'nullbore help')", args[0])
	}
}

// portList implements flag.Value for repeatable --port flags.
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

func cmdOpen(cfg *config.Config, args []string) error {
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

	// Set TTL on all specs
	for i := range ports {
		ports[i].TTL = *ttl
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

func printUsage() error {
	fmt.Print(`nullbore — on-demand tunnel client

Usage:
  nullbore open --port <port> [--name <name>] [--ttl <duration>]
  nullbore open -p <port>[:<name>] [-p <port>[:<name>] ...]
  nullbore open <port> [<port> ...]
  nullbore daemon                             # dashboard-driven persistent mode
  nullbore list
  nullbore close <tunnel-id-or-name>
  nullbore status
  nullbore version

Examples:
  nullbore open --port 3000                  # single tunnel
  nullbore open -p 3000:api -p 8080:web      # multiple named tunnels
  nullbore open 3000 8080 5432               # multiple tunnels (positional)

Configuration:
  ~/.nullbore/config.toml
  
  server = "https://api.nullbore.com"
  api_key = "nbk_..."
  default_ttl = "1h"

Environment:
  NULLBORE_SERVER             Override server URL
  NULLBORE_API_KEY            Override API key
  NULLBORE_DASHBOARD          Override dashboard URL
  NULLBORE_TLS_SKIP_VERIFY    Skip TLS verification (1/true)

Daemon mode:
  Connects to the NullBore dashboard and manages tunnels based on your
  dashboard configuration. Tunnels activate/deactivate remotely without
  restarting the daemon.
`)
	return nil
}
