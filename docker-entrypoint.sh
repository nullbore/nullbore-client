#!/bin/sh
set -e

# NullBore Docker entrypoint
#
# Modes:
#   1. Dashboard-driven: set NULLBORE_API_KEY + NULLBORE_DASHBOARD
#      → runs "nullbore daemon", manages tunnels from dashboard UI
#
#   2. Static tunnels: set NULLBORE_API_KEY + NULLBORE_TUNNELS
#      → opens fixed tunnels: "host:port:slug,host:port:slug,..."
#      → e.g. NULLBORE_TUNNELS=gramps:5000:gramps-web,openclaw:8080:claw
#
#   3. Direct command: pass any nullbore args
#      → e.g. docker run nullbore/tunnel open gramps:5000 --name gramps

# If arguments are passed, run them directly
if [ $# -gt 0 ]; then
    exec nullbore "$@"
fi

# Validate
if [ -z "$NULLBORE_API_KEY" ]; then
    echo "Error: NULLBORE_API_KEY is required"
    echo ""
    echo "Usage:"
    echo "  Dashboard mode: set NULLBORE_API_KEY + NULLBORE_DASHBOARD"
    echo "  Static mode:    set NULLBORE_API_KEY + NULLBORE_TUNNELS=host:port:slug,..."
    echo ""
    echo "Example:"
    echo "  docker run -e NULLBORE_API_KEY=nbk_... -e NULLBORE_TUNNELS=myapp:3000:demo nullbore/tunnel"
    exit 1
fi

# Default server if not set
if [ -z "$NULLBORE_SERVER" ]; then
    export NULLBORE_SERVER="https://tunnel.nullbore.com"
fi

echo "🕳️  NullBore Tunnel Container"
echo "  Server: ${NULLBORE_SERVER}"

if [ -n "$NULLBORE_TUNNELS" ]; then
    echo "  Mode: static tunnels"
    echo "  Tunnels: ${NULLBORE_TUNNELS}"
elif [ -n "$NULLBORE_DASHBOARD" ]; then
    echo "  Mode: dashboard-driven"
    echo "  Dashboard: ${NULLBORE_DASHBOARD}"
else
    export NULLBORE_DASHBOARD="https://nullbore.com"
    echo "  Mode: dashboard-driven"
    echo "  Dashboard: ${NULLBORE_DASHBOARD}"
fi

echo ""
exec nullbore daemon
