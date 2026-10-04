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

## License

MIT
