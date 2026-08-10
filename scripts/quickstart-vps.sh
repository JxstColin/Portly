#!/usr/bin/env bash
# One-command VPS setup: installs Go if needed, builds portly-server (with
# embedded client binaries for the web UI's 'Add machine' installer),
# installs it as a systemd service, and opens the relevant firewall ports.
#
# Usage (from a fresh VPS, as root):
#   curl -fsSL https://raw.githubusercontent.com/JxstColin/Portly/main/scripts/quickstart-vps.sh | sudo bash -s -- --host YOUR_VPS_IP_OR_DOMAIN
#
# Or, if you've already cloned the repo:
#   sudo ./scripts/quickstart-vps.sh --host YOUR_VPS_IP_OR_DOMAIN
set -euo pipefail

REPO_URL="https://github.com/JxstColin/Portly.git"
DEFAULT_SRC_DIR="/opt/portly-src"
CONTROL_PORT=7000
API_PORT=8080
HOST=""

log() { echo "[quickstart] $*"; }
die() { echo "[quickstart] error: $*" >&2; exit 1; }

while [ $# -gt 0 ]; do
	case "$1" in
	--host) HOST="$2"; shift 2 ;;
	--control-port) CONTROL_PORT="$2"; shift 2 ;;
	--api-port) API_PORT="$2"; shift 2 ;;
	*) die "unknown argument: $1" ;;
	esac
done

[ "$(id -u)" -eq 0 ] || die "must run as root (use sudo)"
[ -n "$HOST" ] || die "missing required --host YOUR_VPS_IP_OR_DOMAIN (used for the TLS cert and install links)"

command -v curl >/dev/null 2>&1 || die "curl is required but not installed"

MISSING_PKGS=""
command -v git >/dev/null 2>&1 || MISSING_PKGS="$MISSING_PKGS git"
command -v make >/dev/null 2>&1 || MISSING_PKGS="$MISSING_PKGS make"
if [ -n "$MISSING_PKGS" ]; then
	if command -v apt-get >/dev/null 2>&1; then
		log "installing missing packages:$MISSING_PKGS..."
		apt-get update -qq && apt-get install -y -qq $MISSING_PKGS
	else
		die "missing required command(s):$MISSING_PKGS (install manually — this script only automates apt-based distros)"
	fi
fi

# Reuse an existing checkout if we're already inside one (e.g. the user
# cloned the repo themselves and ran this script from there); otherwise
# clone fresh into DEFAULT_SRC_DIR.
if [ -f "go.mod" ] && grep -q "module github.com/jxstcolin/portly" go.mod 2>/dev/null; then
	SRC_DIR="$(pwd)"
	log "using existing checkout at $SRC_DIR"
else
	SRC_DIR="$DEFAULT_SRC_DIR"
	if [ -d "$SRC_DIR/.git" ]; then
		log "updating existing checkout at $SRC_DIR..."
		git -C "$SRC_DIR" pull --ff-only
	else
		log "cloning $REPO_URL into $SRC_DIR..."
		git clone --depth 1 "$REPO_URL" "$SRC_DIR"
	fi
fi

log "ensuring Go toolchain..."
bash "$SRC_DIR/scripts/install-go.sh"
export PATH="$PATH:/usr/local/go/bin"

log "building portly-server (with embedded client binaries) and portly-client..."
make -C "$SRC_DIR" build

install -Dm755 "$SRC_DIR/bin/portly-server" /usr/local/bin/portly-server
install -Dm755 "$SRC_DIR/bin/portly-client" /usr/local/bin/portly-client

id -u portly >/dev/null 2>&1 || useradd --system --no-create-home --shell /usr/sbin/nologin portly
mkdir -p /var/lib/portly
chown portly:portly /var/lib/portly

log "installing systemd service..."
cat >/etc/systemd/system/portly-server.service <<EOF
[Unit]
Description=Portly reverse-tunnel server (control-plane + API)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=portly
Group=portly
ExecStart=/usr/local/bin/portly-server --data-dir /var/lib/portly --control-addr :${CONTROL_PORT} --api-addr :${API_PORT} --advertise-host ${HOST} --allowed-origin http://${HOST}:3000 --allowed-origin https://${HOST} run
Restart=on-failure
RestartSec=2
AmbientCapabilities=CAP_NET_BIND_SERVICE
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/portly
PrivateTmp=true

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable --now portly-server

if command -v ufw >/dev/null 2>&1 && ufw status | grep -q "Status: active"; then
	log "opening firewall ports ${CONTROL_PORT} and ${API_PORT} via ufw..."
	ufw allow "${CONTROL_PORT}/tcp" >/dev/null
	ufw allow "${API_PORT}/tcp" >/dev/null
fi

sleep 2
log "done."
echo ""
echo "Portly API:      http://${HOST}:${API_PORT}"
echo "Control-plane:   ${HOST}:${CONTROL_PORT}"
echo "Default login:   admin / portly  (you MUST change this on first login)"
echo ""
echo "Remember: any cloud firewall / security group in front of this VPS needs"
echo "${CONTROL_PORT}/tcp and ${API_PORT}/tcp opened too (ufw alone isn't enough behind e.g."
echo "Hetzner/AWS/DigitalOcean firewalls). Each tunnel's public port needs the"
echo "same treatment once you create it."
echo ""
echo "Logs: journalctl -u portly-server -f"
