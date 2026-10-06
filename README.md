# nullbore-client

The NullBore tunnel client. Exposes localhost services through a NullBore server.

## Install

```bash
curl https://get.nullbore.com | sh
```

Or build from source:

```bash
go build -o nullbore ./cmd/nullbore
```

## Usage

```bash
# Expose a local port (default 1h TTL)
nullbore open --port 8080

# With custom TTL and name
nullbore open --port 3000 --ttl 30m --name myapp

# End-to-end TLS passthrough (paid plans) — see below
nullbore open --port 8443 --tls-passthrough

# List active tunnels
nullbore list

# Close a tunnel
nullbore close myapp

# Check connection status
nullbore status
```

## Configuration

Create `~/.nullbore/config.toml`:

```toml
server = "https://api.nullbore.com"
api_key = "nbk_..."
default_ttl = "1h"
```

## TLS passthrough (end-to-end encryption)

By default the relay terminates TLS and proxies HTTP to your local port. With
`--tls-passthrough` the relay instead forwards the raw TLS bytes untouched, so
traffic stays encrypted end-to-end between the visitor and your service:

```bash
nullbore open --port 8443 --tls-passthrough
#   ✓ https://<tunnel-host> → localhost:8443 (tls-passthrough)
#       tls-passthrough: end-to-end encrypted — the certificate visitors see is your local service's own
```

- **Your local service must serve TLS itself** (e.g. Caddy, nginx, or an app
  with its own certificate). Visitors see that service's certificate, so it
  must be valid for the tunnel hostname if browsers are to trust it.
- The relay cannot inspect requests or add basic auth, so `--auth` cannot be
  combined with `--tls-passthrough` (the client refuses it).
- **Paid plans only.** The server returns 403 on the free plan; the client
  reports this as "TLS passthrough is not available on your plan".
- `nullbore list` shows these tunnels with mode `tls-passthrough`.

### Daemon / Docker sidecar

`NULLBORE_TUNNELS` entries (`port`, `port:slug`, `host:port`, `host:port:slug`)
take an optional `+tls-passthrough` suffix. Entries without a suffix behave
exactly as before:

```yaml
nullbore-tunnel:
  image: ghcr.io/nullbore/tunnel:latest
  environment:
    - NULLBORE_API_KEY=${NULLBORE_API_KEY}
    - NULLBORE_TUNNELS=caddy:443:secure+tls-passthrough,webapp:3000:my-app
```

In `config.toml` (for `nullbore daemon`), set `mode` on a tunnel:

```toml
[[tunnels]]
port = 8443
name = "secure"
mode = "tls-passthrough"
```

The mode is re-sent every time the daemon re-registers a tunnel after a
reconnect. Dashboard-managed tunnels are always relay mode; the dashboard
has no mode setting yet.

## Trusted certificates for end-to-end tunnels

End-to-end tunnels are served at `<tunnel>.<account>.e2e.nullbore.com`. Because
the relay never decrypts them, the certificate visitors see is the one your
local service serves, and browsers only trust it if it comes from a public
CA. `nullbore acme` is an ACME DNS-01 hook that gets you one without your
private key leaving your machine: your ACME client generates the key and CSR
locally, and NullBore only publishes the `_acme-challenge` TXT record the CA
checks. Paid plans only.

```bash
nullbore acme present <fqdn> <value>   # publish the TXT record, wait for public DNS
nullbore acme cleanup <fqdn> <value>   # remove it (succeeds if already gone)
```

`present` waits (up to 120s, `--wait-timeout` to change) until 1.1.1.1 and
8.8.8.8 serve the record; `--no-wait` skips that.

### With lego

lego's exec provider runs `$EXEC_PATH present <fqdn> <value>` and
`$EXEC_PATH cleanup <fqdn> <value>`, which is exactly what `nullbore acme`
takes. Create a two-line wrapper script and point `EXEC_PATH` at it:

```bash
cat > ~/bin/nullbore-acme-hook <<'EOF'
#!/bin/sh
exec nullbore acme "$@"
EOF
chmod +x ~/bin/nullbore-acme-hook

# Wildcard cert covering every tunnel on your account (replace ACCOUNT)
export NULLBORE_API_KEY="nbk_..."
EXEC_PATH=~/bin/nullbore-acme-hook \
  lego --dns exec --domains '*.ACCOUNT.e2e.nullbore.com' --email you@example.com run
```

lego writes the results to `./.lego/certificates/` (or `--path`): for the
wildcard above, `_.ACCOUNT.e2e.nullbore.com.crt` (full chain) and
`_.ACCOUNT.e2e.nullbore.com.key` (the private key, created locally). Renew
with the same command, replacing `run` with `renew`.

Then configure your local TLS server (Caddy, nginx, your app) to serve that
certificate and key, and expose it with `nullbore open --port <port>
--tls-passthrough`. The relay passes the TLS handshake straight through, so
the certificate must be loaded by your local service; NullBore never sees it.

## License

MIT
