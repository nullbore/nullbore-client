#!/bin/sh
# NullBore installer — https://nullbore.com
# Usage: curl -fsSL https://nullbore.com/install.sh | sh
#
# Detects OS/arch, downloads the latest release from GitHub,
# installs to /usr/local/bin (or ~/.local/bin if no root).

set -e

REPO="nullbore/nullbore-client"
BINARY="nullbore"

# --- Detect platform ---

detect_os() {
  case "$(uname -s)" in
    Linux*)  echo "linux" ;;
    Darwin*) echo "darwin" ;;
    MINGW*|MSYS*|CYGWIN*) echo "windows" ;;
    *) echo "unsupported"; return 1 ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64)  echo "amd64" ;;
    aarch64|arm64)  echo "arm64" ;;
    *) echo "unsupported"; return 1 ;;
  esac
}

# --- Fetch latest version ---

get_latest_version() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null | \
      grep '"tag_name"' | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/'
  elif command -v wget >/dev/null 2>&1; then
    wget -qO- "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null | \
      grep '"tag_name"' | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/'
  else
    echo ""
  fi
}

# --- Download ---

download() {
  url="$1"
  dest="$2"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$url" -o "$dest"
  elif command -v wget >/dev/null 2>&1; then
    wget -q "$url" -O "$dest"
  else
    echo "Error: curl or wget required"
    exit 1
  fi
}

# --- Main ---

main() {
  echo ""
  echo "  🕳️  NullBore Installer"
  echo ""

  OS=$(detect_os)
  ARCH=$(detect_arch)

  if [ "$OS" = "unsupported" ] || [ "$ARCH" = "unsupported" ]; then
    echo "  Error: unsupported platform $(uname -s)/$(uname -m)"
    echo "  Build from source: https://github.com/${REPO}"
    exit 1
  fi

  echo "  Platform: ${OS}/${ARCH}"

  # Get version
  VERSION="${NULLBORE_VERSION:-}"
  if [ -z "$VERSION" ]; then
    echo "  Fetching latest version..."
    VERSION=$(get_latest_version)
  fi

  if [ -z "$VERSION" ]; then
    echo "  Error: could not determine latest version."
    echo "  Set NULLBORE_VERSION=v0.1.0 or download manually:"
    echo "  https://github.com/${REPO}/releases"
    exit 1
  fi

  echo "  Version: ${VERSION}"

  # Build download URL
  EXT=""
  if [ "$OS" = "windows" ]; then EXT=".exe"; fi
  FILENAME="${BINARY}-${OS}-${ARCH}${EXT}"
  URL="https://github.com/${REPO}/releases/download/${VERSION}/${FILENAME}"

  echo "  Downloading ${FILENAME}..."

  TMPDIR=$(mktemp -d)
  TMPFILE="${TMPDIR}/${BINARY}${EXT}"

  download "$URL" "$TMPFILE"
  chmod +x "$TMPFILE"

  # Verify it runs
  if ! "$TMPFILE" version >/dev/null 2>&1; then
    echo "  Error: downloaded binary doesn't execute on this platform"
    rm -rf "$TMPDIR"
    exit 1
  fi

  INSTALLED_VERSION=$("$TMPFILE" version 2>/dev/null || echo "$VERSION")

  # Install
  INSTALL_DIR="/usr/local/bin"
  NEED_SUDO=""

  if [ ! -w "$INSTALL_DIR" ]; then
    if command -v sudo >/dev/null 2>&1; then
      NEED_SUDO="sudo"
    else
      # Fall back to ~/.local/bin
      INSTALL_DIR="${HOME}/.local/bin"
      mkdir -p "$INSTALL_DIR"
    fi
  fi

  DEST="${INSTALL_DIR}/${BINARY}${EXT}"
  echo "  Installing to ${DEST}..."

  if [ -n "$NEED_SUDO" ]; then
    sudo mv "$TMPFILE" "$DEST"
    sudo chmod +x "$DEST"
  else
    mv "$TMPFILE" "$DEST"
    chmod +x "$DEST"
  fi

  rm -rf "$TMPDIR"

  # Create config dir
  CONFIG_DIR="${HOME}/.nullbore"
  if [ ! -d "$CONFIG_DIR" ]; then
    mkdir -p "$CONFIG_DIR"
    echo "  Created config directory: ${CONFIG_DIR}"
  fi

  # Create default config if missing
  CONFIG_FILE="${CONFIG_DIR}/config.toml"
  if [ ! -f "$CONFIG_FILE" ]; then
    cat > "$CONFIG_FILE" << 'CONF'
# NullBore client configuration
# Docs: https://nullbore.com/docs

server = "https://tunnel.nullbore.com"
# api_key = "nbk_..."
# default_ttl = "1h"
CONF
    echo "  Created default config: ${CONFIG_FILE}"
  fi

  # Check PATH
  case ":$PATH:" in
    *":${INSTALL_DIR}:"*) ;;
    *)
      echo ""
      echo "  ⚠  ${INSTALL_DIR} is not in your PATH."
      echo "  Add this to your shell profile:"
      echo ""
      echo "    export PATH=\"\$PATH:${INSTALL_DIR}\""
      echo ""
      ;;
  esac

  echo ""
  echo "  ✅ NullBore ${INSTALLED_VERSION} installed successfully!"
  echo ""
  echo "  Get started:"
  echo "    nullbore status          # check connection to server"
  echo "    nullbore open 3000       # expose localhost:3000"
  echo "    nullbore open -p 3000:api -p 8080:web  # multiple tunnels"
  echo ""
  echo "  Set your API key:"
  echo "    Edit ~/.nullbore/config.toml or export NULLBORE_API_KEY=nbk_..."
  echo ""
  echo "  Docs: https://nullbore.com/docs"
  echo ""
}

main "$@"
