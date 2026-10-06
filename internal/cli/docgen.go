package cli

import (
	"flag"
	"fmt"
	"strings"
)

// cmdDoc holds the metadata for a CLI command's documentation.
type cmdDoc struct {
	Name        string
	Summary     string
	Usage       []string // usage lines
	Description string   // longer description
	Flags       *flag.FlagSet
	CustomFlags string // extra flag docs not captured by flag.FlagSet (e.g. -p)
	Examples    []string
	RequiresKey bool
	Args        string // description of positional args
}

// allCommands returns documentation for every CLI command, derived from the
// same flag.FlagSet definitions used in the actual handlers.
// This is the single source of truth for CLI docs.
func allCommands() []cmdDoc {
	// --- open ---
	openFlags := flag.NewFlagSet("open", flag.ContinueOnError)
	openFlags.Int("port", 0, "Local port to expose (single tunnel)")
	openFlags.String("name", "", "Tunnel name / custom subdomain (Dev+ plans)")
	openFlags.String("ttl", "1h", "Time-to-live (e.g. 30m, 2h, 24h)")
	openFlags.String("host", "localhost", "Target host (for Docker/remote services)")
	openFlags.String("auth", "", "Basic auth for tunnel access (user:pass)")
	openFlags.Bool("tls-passthrough", false, tlsPassthroughHelp)

	// --- requests ---
	reqFlags := flag.NewFlagSet("requests", flag.ContinueOnError)
	reqFlags.Int("limit", 20, "Number of requests to show")

	// --- update ---
	updateFlags := flag.NewFlagSet("update", flag.ContinueOnError)
	updateFlags.Bool("check", false, "Only check for updates, don't install")

	return []cmdDoc{
		{
			Name:    "open",
			Summary: "Open one or more tunnels to expose local ports",
			Usage: []string{
				"nullbore open <port>",
				"nullbore open --port <port> [--name <name>] [--ttl <duration>]",
				"nullbore open -p <port>[:<name>] [-p <port>[:<name>] ...]",
				"nullbore open <port> [<port> ...]",
			},
			Description: "Creates a tunnel on the server and relays traffic from the public URL to your local port. " +
				"Stays open until the TTL expires or you press Ctrl+C.\n\n" +
				"With `--tls-passthrough` the relay forwards raw TLS bytes without decrypting them, so traffic is " +
				"encrypted end-to-end. Your local service must serve TLS itself and visitors see its certificate. " +
				"The relay cannot inspect requests or add basic auth, so " +
				"`--auth` cannot be combined with it. Available on paid plans only.",
			Flags: openFlags,
			CustomFlags: "  -p <port> or <port>:<name>    Repeatable. Open multiple tunnels.\n" +
				"                                Format: PORT or PORT:NAME\n" +
				"                                Example: -p 3000:api -p 8080:web",
			Examples: []string{
				"nullbore open 3000                          # expose localhost:3000",
				"nullbore open --port 3000 --name myapp      # with custom subdomain",
				"nullbore open --port 3000 --ttl 30m         # 30-minute TTL",
				"nullbore open -p 3000:api -p 8080:web       # multiple named tunnels",
				"nullbore open 3000 8080 5432                # multiple tunnels (positional)",
				"nullbore open --port 3000 --auth admin:s3cret  # with basic auth",
				"nullbore open --port 8443 --tls-passthrough    # end-to-end TLS (local service serves TLS)",
			},
			RequiresKey: true,
		},
		{
			Name:        "list",
			Summary:     "List active tunnels",
			Usage:       []string{"nullbore list"},
			Description: "Shows all tunnels currently open for your API key, with their IDs, slugs, ports, and expiry times.",
			RequiresKey: true,
		},
		{
			Name:    "close",
			Summary: "Close a tunnel",
			Usage:   []string{"nullbore close <tunnel-id-or-name>"},
			Args:    "The tunnel ID (or first 8 chars), slug, or name.",
			Description: "Closes the specified tunnel. You can use the full tunnel ID, " +
				"the short ID prefix from `nullbore list`, or the tunnel's slug/name.",
			RequiresKey: true,
		},
		{
			Name:        "requests",
			Summary:     "Inspect recent HTTP requests to a tunnel",
			Usage:       []string{"nullbore requests <tunnel-id-or-slug> [--limit N]"},
			Args:        "The tunnel ID or slug to inspect.",
			Description: "Shows recent HTTP requests that hit your tunnel — method, path, body size, and source IP. Useful for debugging webhooks.",
			Flags:       reqFlags,
			RequiresKey: true,
		},
		{
			Name:        "status",
			Summary:     "Check server connection and auth status",
			Usage:       []string{"nullbore status"},
			Description: "Pings the tunnel server and reports its version. Shows whether an API key is configured.",
		},
		{
			Name:    "daemon",
			Summary: "Run in dashboard-driven persistent mode",
			Usage:   []string{"nullbore daemon"},
			Description: "Connects to the NullBore dashboard and manages tunnels based on your dashboard configuration. " +
				"Tunnels activate/deactivate remotely without restarting the daemon.\n\n" +
				"For static/headless mode (Docker), set `NULLBORE_TUNNELS` instead:\n\n" +
				"    NULLBORE_TUNNELS=host:port:slug,host:port:slug,...\n\n" +
				"Example: `NULLBORE_TUNNELS=webapp:3000:my-app,db:5432:my-db`\n\n" +
				"Append `+tls-passthrough` to an entry for end-to-end TLS passthrough (the service must serve TLS; paid plans): " +
				"`NULLBORE_TUNNELS=caddy:443:secure+tls-passthrough,webapp:3000:my-app`",
			RequiresKey: true,
		},
		{
			Name:    "acme",
			Summary: "ACME DNS-01 hook for publicly trusted end-to-end tunnel certificates",
			Usage: []string{
				"nullbore acme present [--no-wait] [--wait-timeout 120s] <fqdn> <value>",
				"nullbore acme cleanup <fqdn> <value>",
			},
			Args: "`<fqdn>` is the challenge name (e.g. `_acme-challenge.ACCOUNT.e2e.nullbore.com`; a trailing dot is accepted) " +
				"and `<value>` the TXT value supplied by your ACME client.",
			Description: "End-to-end (`--tls-passthrough`) tunnels are served at `<tunnel>.<account>.e2e.nullbore.com`, " +
				"and your local service presents its own certificate. `nullbore acme` lets any ACME client obtain a " +
				"publicly trusted certificate for that name, or the wildcard `*.<account>.e2e.nullbore.com`, " +
				"using the DNS-01 challenge: `present` asks the NullBore server to publish the `_acme-challenge` TXT record, " +
				"`cleanup` removes it. Only the challenge value is sent to NullBore; **the private key is generated and stays on your machine.** " +
				"Paid plans only.\n\n" +
				"By default `present` then waits (up to `--wait-timeout`) until 1.1.1.1 and 8.8.8.8 serve the record, " +
				"so ACME clients that don't check propagation still work. `cleanup` succeeds if the record is already gone.\n\n" +
				"The argument order matches lego's exec provider, which runs `$EXEC_PATH present <fqdn> <value>` and " +
				"`$EXEC_PATH cleanup <fqdn> <value>`. Point `EXEC_PATH` at a two-line wrapper script:\n\n" +
				"    #!/bin/sh\n" +
				"    exec nullbore acme \"$@\"\n\n" +
				"Then serve the issued certificate and key (lego writes them to `./.lego/certificates/`) from your local TLS server.",
			CustomFlags: "  --no-wait               Return as soon as the server accepts the record\n" +
				"  --wait                  Wait for 1.1.1.1 and 8.8.8.8 to serve the record (default)\n" +
				"  --wait-timeout <dur>    Stop waiting after this long; the command still succeeds (default: 120s)",
			Examples: []string{
				"printf '#!/bin/sh\\nexec nullbore acme \"$@\"\\n' > ~/bin/nullbore-acme-hook && chmod +x ~/bin/nullbore-acme-hook",
				"EXEC_PATH=~/bin/nullbore-acme-hook lego --dns exec --domains '*.ACCOUNT.e2e.nullbore.com' --email you@example.com run",
				"nullbore acme present _acme-challenge.ACCOUNT.e2e.nullbore.com. TOKEN   # manual",
				"nullbore acme cleanup _acme-challenge.ACCOUNT.e2e.nullbore.com. TOKEN",
			},
			RequiresKey: true,
		},
		{
			Name:        "update",
			Summary:     "Check for updates and self-update",
			Usage:       []string{"nullbore update", "nullbore update --check"},
			Description: "Checks GitHub for a newer release. Without `--check`, downloads and replaces the binary.",
			Flags:       updateFlags,
		},
		{
			Name:    "version",
			Summary: "Show client version",
			Usage:   []string{"nullbore version"},
		},
		{
			Name:    "help",
			Summary: "Show help",
			Usage:   []string{"nullbore help"},
		},
	}
}

// envVarDocs returns documentation for all environment variables.
// These are defined in config.Load() — kept here as the doc source of truth.
func envVarDocs() []struct {
	Name    string
	Desc    string
	Default string
} {
	return []struct {
		Name    string
		Desc    string
		Default string
	}{
		{"NULLBORE_SERVER", "Tunnel server URL (must include https://)", "https://tunnel.nullbore.com"},
		{"NULLBORE_API_KEY", "API key for authentication", ""},
		{"NULLBORE_DASHBOARD", "Dashboard URL (for daemon mode)", "https://nullbore.com"},
		{"NULLBORE_TLS_SKIP_VERIFY", "Skip TLS certificate verification (set to 1 or true)", ""},
		{"NULLBORE_TUNNELS", "Static tunnel list for Docker/headless mode (format: host:port:slug,...; append +tls-passthrough to an entry for end-to-end TLS)", ""},
		{"NULLBORE_INSTALL_DIR", "Override install directory for install.sh", "~/.local/bin"},
		{"NULLBORE_VERSION", "Pin a specific version for install.sh", ""},
	}
}

// GenerateDocs outputs a complete CLI reference in markdown, derived from
// the actual command definitions. This is called by `nullbore _generate-docs`.
func GenerateDocs() string {
	var b strings.Builder

	b.WriteString("# CLI Reference\n\n")
	b.WriteString("> **Auto-generated from the client source code.** Do not edit manually.\n")
	b.WriteString(fmt.Sprintf("> Client version: `%s`\n\n", version))

	// Commands
	for _, cmd := range allCommands() {
		b.WriteString(fmt.Sprintf("## `nullbore %s`\n\n", cmd.Name))

		if cmd.Summary != "" {
			b.WriteString(cmd.Summary + "\n\n")
		}

		// Usage
		if len(cmd.Usage) > 0 {
			b.WriteString("```\n")
			for _, u := range cmd.Usage {
				b.WriteString(u + "\n")
			}
			b.WriteString("```\n\n")
		}

		if cmd.Description != "" {
			b.WriteString(cmd.Description + "\n\n")
		}

		if cmd.Args != "" {
			b.WriteString("**Arguments:** " + cmd.Args + "\n\n")
		}

		if cmd.RequiresKey {
			b.WriteString("*Requires an API key.*\n\n")
		}

		// Flags from FlagSet
		if cmd.Flags != nil || cmd.CustomFlags != "" {
			b.WriteString("**Flags:**\n\n")
			b.WriteString("```\n")
			if cmd.Flags != nil {
				cmd.Flags.VisitAll(func(f *flag.Flag) {
					def := ""
					if f.DefValue != "" && f.DefValue != "0" && f.DefValue != "false" {
						def = fmt.Sprintf(" (default: %s)", f.DefValue)
					}
					b.WriteString(fmt.Sprintf("  --%s    %s%s\n", f.Name, f.Usage, def))
				})
			}
			if cmd.CustomFlags != "" {
				b.WriteString(cmd.CustomFlags + "\n")
			}
			b.WriteString("```\n\n")
		}

		// Examples
		if len(cmd.Examples) > 0 {
			b.WriteString("**Examples:**\n\n")
			b.WriteString("```bash\n")
			for _, ex := range cmd.Examples {
				b.WriteString(ex + "\n")
			}
			b.WriteString("```\n\n")
		}

		b.WriteString("---\n\n")
	}

	// Environment variables
	b.WriteString("## Environment Variables\n\n")
	b.WriteString("Environment variables override config file values.\n\n")
	b.WriteString("| Variable | Description | Default |\n")
	b.WriteString("|----------|-------------|----------|\n")
	for _, ev := range envVarDocs() {
		def := ev.Default
		if def == "" {
			def = "—"
		}
		b.WriteString(fmt.Sprintf("| `%s` | %s | %s |\n", ev.Name, ev.Desc, def))
	}
	b.WriteString("\n")

	b.WriteString("> **Important:** Use `export` when setting environment variables in your shell.\n")
	b.WriteString("> Without `export`, the variable is only a shell variable and won't be passed to `nullbore`.\n")
	b.WriteString(">\n")
	b.WriteString("> ```bash\n")
	b.WriteString("> # Wrong:\n")
	b.WriteString("> NULLBORE_API_KEY=\"nbk_...\"    # shell variable only\n")
	b.WriteString("> nullbore open 3000             # won't see the key\n")
	b.WriteString(">\n")
	b.WriteString("> # Right:\n")
	b.WriteString("> export NULLBORE_API_KEY=\"nbk_...\"\n")
	b.WriteString("> nullbore open 3000\n")
	b.WriteString("> ```\n\n")

	// Config file
	b.WriteString("## Config File\n\n")
	b.WriteString("The client reads `~/.config/nullbore/config.toml` on startup.\n\n")
	b.WriteString("> Legacy path `~/.nullbore/config.toml` is still supported.\n\n")
	b.WriteString("```toml\n")
	b.WriteString("# ~/.config/nullbore/config.toml\n\n")
	b.WriteString("server = \"https://tunnel.nullbore.com\"\n")
	b.WriteString("api_key = \"nbk_your_key_here\"\n")
	b.WriteString("default_ttl = \"1h\"\n")
	b.WriteString("debug = false\n\n")
	b.WriteString("# Persistent tunnels (managed by daemon)\n")
	b.WriteString("[[tunnels]]\n")
	b.WriteString("name = \"api\"\n")
	b.WriteString("port = 3000\n")
	b.WriteString("# auth = \"user:pass\"  # optional: require basic auth on this tunnel\n")
	b.WriteString("# mode = \"tls-passthrough\"  # optional: end-to-end TLS (local service must serve TLS; not with auth)\n")
	b.WriteString("```\n\n")
	b.WriteString("Edit the file directly — there is no `config set` command.\n\n")

	// Precedence
	b.WriteString("## Precedence\n\n")
	b.WriteString("Environment variables > Config file > Defaults\n")

	return b.String()
}
