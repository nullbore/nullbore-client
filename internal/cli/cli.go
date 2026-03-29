package cli

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"text/tabwriter"

	"github.com/nullbore/nullbore-client/internal/client"
	"github.com/nullbore/nullbore-client/internal/config"
	"github.com/nullbore/nullbore-client/internal/tunnel"
)

const version = "0.1.0"

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
	case "version":
		fmt.Printf("nullbore %s\n", version)
		return nil
	case "help", "--help", "-h":
		return printUsage()
	default:
		return fmt.Errorf("unknown command: %s (try 'nullbore help')", args[0])
	}
}

func cmdOpen(cfg *config.Config, args []string) error {
	fs := flag.NewFlagSet("open", flag.ExitOnError)
	port := fs.Int("port", 0, "Local port to expose (required)")
	name := fs.String("name", "", "Tunnel name (optional, for stable slug)")
	ttl := fs.String("ttl", cfg.DefaultTTL, "Time-to-live (e.g. 30m, 2h)")
	fs.Parse(args)

	if *port == 0 {
		return fmt.Errorf("--port is required")
	}

	c := client.New(cfg)

	// Step 1: Create tunnel via REST API
	fmt.Printf("creating tunnel for localhost:%d...\n", *port)
	t, err := c.CreateTunnel(*port, *name, *ttl)
	if err != nil {
		return fmt.Errorf("creating tunnel: %w", err)
	}

	publicURL := fmt.Sprintf("%s/t/%s", cfg.ServerURL(), t.Slug)
	fmt.Printf("\n  ✓ tunnel created\n")
	fmt.Printf("  id:   %s\n", t.ID)
	fmt.Printf("  slug: %s\n", t.Slug)
	fmt.Printf("  url:  %s\n", publicURL)
	fmt.Printf("  ttl:  %s\n", t.TTL)
	fmt.Printf("  mode: %s\n\n", t.Mode)

	// Step 2: Connect WebSocket tunnel
	conn := tunnel.NewConnector(cfg, t.ID, *port)
	if err := conn.Connect(); err != nil {
		return fmt.Errorf("connecting tunnel: %w", err)
	}

	fmt.Printf("  ✓ tunnel active — forwarding %s → localhost:%d\n", publicURL, *port)
	fmt.Printf("  press Ctrl+C to close\n\n")

	// Handle graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	errCh := make(chan error, 1)
	go func() {
		// Reconnect loop with full tunnel re-registration
		errCh <- tunnel.RunWithFullReconnect(cfg, c, *port, *name, *ttl, conn)
	}()

	select {
	case <-sigCh:
		fmt.Println("\nclosing tunnel...")
		conn.Close()
		c.CloseTunnel(t.ID)
		return nil
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("tunnel error: %w", err)
		}
		fmt.Println("tunnel closed (expired or server shutdown)")
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
		fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\n",
			t.ID[:8], t.Slug, t.LocalPort, t.Mode, t.ExpiresAt)
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

func printUsage() error {
	fmt.Print(`nullbore — on-demand SSH tunnel client

Usage:
  nullbore open --port <port> [--name <name>] [--ttl <duration>]
  nullbore list
  nullbore close <tunnel-id-or-name>
  nullbore status
  nullbore version

Configuration:
  ~/.nullbore/config.toml
  
  server = "https://api.nullbore.com"
  api_key = "nbk_..."
  default_ttl = "1h"

Environment:
  NULLBORE_SERVER    Override server URL
  NULLBORE_API_KEY   Override API key
`)
	return nil
}
