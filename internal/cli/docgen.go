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
	openFlags.String("name", "", "Tunnel name / custom subdomain (Hobby+ plans)")
	openFlags.String("ttl", "1h", "Time-to-live (e.g. 30m, 2h, 24h)")
	openFlags.String("host", "localhost", "Target host (for Docker/remote services)")

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
				"Stays open until the TTL expires or you press Ctrl+C.",
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
				"Example: `NULLBORE_TUNNELS=webapp:3000:my-app,db:5432:my-db`",
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
		{"NULLBORE_TUNNELS", "Static tunnel list for Docker/headless mode (format: host:port:slug,...)", ""},
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
	b.WriteString("The client reads `~/.nullbore/config.toml` on startup.\n\n")
	b.WriteString("```toml\n")
	b.WriteString("# ~/.nullbore/config.toml\n\n")
	b.WriteString("server = \"https://tunnel.nullbore.com\"\n")
	b.WriteString("api_key = \"nbk_your_key_here\"\n")
	b.WriteString("default_ttl = \"1h\"\n")
	b.WriteString("```\n\n")
	b.WriteString("Edit the file directly — there is no `config set` command.\n\n")

	// Precedence
	b.WriteString("## Precedence\n\n")
	b.WriteString("Environment variables > Config file > Defaults\n")

	return b.String()
}
