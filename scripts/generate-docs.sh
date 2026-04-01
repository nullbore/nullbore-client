#!/bin/sh
# Generate CLI reference docs from the actual code.
# Run this after changing CLI flags/commands.
#
# Usage: ./scripts/generate-docs.sh [output-path]
#
# Default output: stdout
# With path:      writes to the given file (for mdBook integration)

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# Build the client
echo "Building nullbore..." >&2
cd "$ROOT"
CGO_ENABLED=0 go build -o /tmp/nullbore-docgen ./cmd/nullbore/

# Generate docs
echo "Generating CLI reference..." >&2
DOCS=$(/tmp/nullbore-docgen _generate-docs)

rm -f /tmp/nullbore-docgen

if [ -n "$1" ]; then
    echo "$DOCS" > "$1"
    echo "Written to $1" >&2
else
    echo "$DOCS"
fi
