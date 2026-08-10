#!/usr/bin/env bash
# Installs (or upgrades) Go from the official tarballs at go.dev if the
# system doesn't already have a Go toolchain new enough to build Portly
# (1.25+). Safe to re-run. Linux amd64/arm64/armv6l only.
set -euo pipefail

MIN_MAJOR=1
MIN_MINOR=25
INSTALL_DIR="/usr/local"
GO_ROOT="${INSTALL_DIR}/go"

log() { echo "[install-go] $*"; }

version_ge() {
	# usage: version_ge <major> <minor> <min_major> <min_minor>
	if [ "$1" -gt "$3" ]; then return 0; fi
	if [ "$1" -eq "$3" ] && [ "$2" -ge "$4" ]; then return 0; fi
	return 1
}

if command -v go >/dev/null 2>&1; then
	current="$(go version | awk '{print $3}' | sed 's/^go//')"
	major="$(echo "$current" | cut -d. -f1)"
	minor="$(echo "$current" | cut -d. -f2)"
	if version_ge "$major" "$minor" "$MIN_MAJOR" "$MIN_MINOR"; then
		log "Go $current already installed and >= ${MIN_MAJOR}.${MIN_MINOR}, nothing to do."
		exit 0
	fi
	log "Found Go $current, but Portly needs >= ${MIN_MAJOR}.${MIN_MINOR}. Upgrading."
fi

if [ "$(uname -s)" != "Linux" ]; then
	echo "This script only automates Linux installs. On macOS: 'brew install go'. On Windows: https://go.dev/dl/" >&2
	exit 1
fi

case "$(uname -m)" in
	x86_64) ARCH="amd64" ;;
	aarch64|arm64) ARCH="arm64" ;;
	armv6l|armv7l) ARCH="armv6l" ;;
	*) echo "Unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

if [ "$(id -u)" -ne 0 ] && [ ! -w "$INSTALL_DIR" ]; then
	echo "Need write access to ${INSTALL_DIR} (run as root or via sudo)." >&2
	exit 1
fi

log "Fetching latest Go version..."
LATEST="$(curl -fsSL https://go.dev/VERSION?m=text | head -n1)" # e.g. go1.25.0
TARBALL="${LATEST}.linux-${ARCH}.tar.gz"
URL="https://go.dev/dl/${TARBALL}"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

log "Downloading ${URL}..."
curl -fsSL -o "${TMP}/${TARBALL}" "$URL"

log "Installing to ${GO_ROOT}..."
rm -rf "$GO_ROOT"
tar -C "$INSTALL_DIR" -xzf "${TMP}/${TARBALL}"

if [ ! -f /etc/profile.d/go.sh ]; then
	echo 'export PATH=$PATH:'"${GO_ROOT}"'/bin' > /etc/profile.d/go.sh
	chmod 644 /etc/profile.d/go.sh
	log "Added ${GO_ROOT}/bin to PATH via /etc/profile.d/go.sh"
fi

log "Installed ${LATEST}. Run 'source /etc/profile.d/go.sh' (or start a new shell) to pick it up in this session."
"${GO_ROOT}/bin/go" version
