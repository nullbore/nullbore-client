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

## License

MIT
