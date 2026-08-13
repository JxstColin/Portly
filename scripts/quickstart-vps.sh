#!/usr/bin/env bash
# One-command VPS setup: installs Go and Node.js if needed, builds
# portly-server (with embedded client binaries for the web UI's 'Add
# machine' installer) and the Next.js web UI, installs both as systemd
# services, and opens the relevant firewall ports.
#
# No host/IP to pass — portly-server auto-detects this machine's public IP
# on its own. Once it's running, open the panel at http://<that IP> and set
# up a domain (with automatic Let's Encrypt HTTPS) from there if you want
# one, rather than passing it on the command line.
#
# Usage (from a fresh VPS, as root):
#   curl -fsSL https://raw.githubusercontent.com/JxstColin/Portly/main/scripts/quickstart-vps.sh | sudo bash
#
# Or, if you've already cloned the repo:
#   sudo ./scripts/quickstart-vps.sh
#
# This is also the update command: safe to re-run any time (e.g. after a
# new Portly release) — it pulls the latest code, rebuilds both services,
# and restarts them in place. portly-client machines out in the field
# update themselves automatically and don't need this re-run for that.
#
# Running this script is also what wires up the panel's own "Update now"
# button: it installs a root-owned systemd .path unit that watches for an
# update request from the panel and runs this same script in response (see
# the portly-update section below for exactly how, and why it isn't sudo).
set -euo pipefail

REPO_URL="https://github.com/JxstColin/Portly.git"
DEFAULT_SRC_DIR="/opt/portly-src"
CONTROL_PORT=7000
WEB_PORT=80
HTTPS_PORT=443
LOCAL_WEB_PORT=3000
NODE_MAJOR=20
# Written by portly-server (the one path it can write to) to request an
# update; watched by portly-update.path. Must match updateTriggerFileName
# in cmd/portly-server/main.go.
UPDATE_TRIGGER_FILE=/var/lib/portly/update-requested
SUDOERS_FILE=/etc/sudoers.d/portly-update # legacy, removed below

log() { echo "[quickstart] $*"; }
die() { echo "[quickstart] error: $*" >&2; exit 1; }

while [ $# -gt 0 ]; do
	case "$1" in
	--control-port) CONTROL_PORT="$2"; shift 2 ;;
	--web-port) WEB_PORT="$2"; shift 2 ;;
	--https-port) HTTPS_PORT="$2"; shift 2 ;;
	*) die "unknown argument: $1 (host/domain are no longer passed here — set a domain in the web UI once it's running)" ;;
	esac
done

[ "$(id -u)" -eq 0 ] || die "must run as root (use sudo)"

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

if ! command -v node >/dev/null 2>&1 || [ "$(node -e 'console.log(process.versions.node.split(".")[0])')" -lt "$NODE_MAJOR" ]; then
	command -v apt-get >/dev/null 2>&1 || die "Node.js $NODE_MAJOR+ is required (install manually — this script only automates apt-based distros)"
	log "installing Node.js ${NODE_MAJOR}.x (needed to build/run the web UI)..."
	curl -fsSL "https://deb.nodesource.com/setup_${NODE_MAJOR}.x" | bash - >/dev/null
	apt-get install -y -qq nodejs
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
		# This checkout is entirely script-managed (nobody hand-edits
		# /opt/portly-src), so hard-reset to whatever origin/main currently
		# is rather than 'pull --ff-only' — that fails outright the moment
		# histories diverge for any reason (e.g. the upstream repo's history
		# was ever rewritten/force-pushed), permanently breaking the update
		# path until someone deletes the checkout by hand.
		git -C "$SRC_DIR" fetch --depth 1 origin main
		git -C "$SRC_DIR" reset --hard FETCH_HEAD
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
Description=Portly reverse-tunnel server (control-plane + API + web)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=portly
Group=portly
ExecStart=/usr/local/bin/portly-server --data-dir /var/lib/portly --control-addr :${CONTROL_PORT} --web-addr :${WEB_PORT} --https-addr :${HTTPS_PORT} --web-upstream http://127.0.0.1:${LOCAL_WEB_PORT} run
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

# The panel's "Update now" button works by dropping a marker file into
# /var/lib/portly (the one directory portly-server can write to), which
# this .path unit watches and turns into a run of this very script as
# root.
#
# It deliberately does NOT go through sudo. portly-server's unit above
# sets NoNewPrivileges=true, which makes the kernel refuse every setuid
# binary — sudo included — so a sudoers grant could never have worked
# from inside that service no matter how it was written. That same unit
# also sets ProtectSystem=strict, and child processes inherit the mount
# namespace, so even a working sudo would have been handed a read-only
# /usr and /opt to install into. This unit runs as root in its own clean
# namespace instead, sharing nothing with portly-server but the marker
# file — and portly-server never gains a privilege of its own.
log "installing the panel-triggered updater..."
rm -f "$SUDOERS_FILE" # no longer used; the sudo approach never worked (see above)

cat >/etc/systemd/system/portly-update.service <<EOF
[Unit]
Description=Portly self-update, triggered by the panel's "Update now" button

[Service]
Type=oneshot
# Removed first so the .path unit re-arms for the next request, and so a
# failed update can't wedge this into a restart loop.
ExecStartPre=/bin/rm -f ${UPDATE_TRIGGER_FILE}
ExecStart=${SRC_DIR}/scripts/quickstart-vps.sh
StandardOutput=append:/var/lib/portly/update.log
StandardError=append:/var/lib/portly/update.log
EOF

cat >/etc/systemd/system/portly-update.path <<EOF
[Unit]
Description=Watch for Portly update requests from the panel

[Path]
PathExists=${UPDATE_TRIGGER_FILE}
Unit=portly-update.service

[Install]
WantedBy=multi-user.target
EOF

UPDATE_BUTTON_STATUS="enabled"

systemctl daemon-reload
systemctl enable portly-server
systemctl enable portly-update.path
# Starting the .path unit is what actually arms the watch. Restarting it
# is safe mid-update: this script is running *from* portly-update.service,
# and a .path unit is a separate unit from the service it triggers.
systemctl restart portly-update.path
# 'enable --now' only starts a service that isn't already running — on a
# re-run (update) it would silently leave the old binary's process alive
# in memory even though we just replaced /usr/local/bin/portly-server on
# disk, so restart explicitly every time instead.
systemctl restart portly-server

log "building the web UI (this can take a minute)..."
# No NEXT_PUBLIC_API_BASE needed: portly-server reverse-proxies the UI onto
# its own origin, so the UI just calls the API relative to wherever it's
# being served from — nothing to bake in at build time, no rebuild needed
# if you set a domain later.
( cd "$SRC_DIR/web" && npm install --no-audit --no-fund --silent && npm run build )
chown -R portly:portly "$SRC_DIR/web"

log "installing web UI systemd service..."
cat >/etc/systemd/system/portly-web.service <<EOF
[Unit]
Description=Portly web UI (internal — reverse-proxied by portly-server)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=portly
Group=portly
WorkingDirectory=${SRC_DIR}/web
ExecStart=${SRC_DIR}/web/node_modules/.bin/next start -H 127.0.0.1 -p ${LOCAL_WEB_PORT}
Restart=on-failure
RestartSec=2
NoNewPrivileges=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable portly-web
systemctl restart portly-web

if command -v ufw >/dev/null 2>&1 && ufw status | grep -q "Status: active"; then
	log "opening firewall ports ${CONTROL_PORT}, ${WEB_PORT}, and ${HTTPS_PORT} via ufw..."
	ufw allow "${CONTROL_PORT}/tcp" >/dev/null
	ufw allow "${WEB_PORT}/tcp" >/dev/null
	ufw allow "${HTTPS_PORT}/tcp" >/dev/null
fi

sleep 2
PUBLIC_IP="$(curl -4 -fsS --max-time 5 https://ifconfig.me 2>/dev/null || echo "<your-vps-ip>")"

log "done."
echo ""
echo "Panel: http://${PUBLIC_IP}"
echo ""
echo "Panel's one-click Update now button: ${UPDATE_BUTTON_STATUS}"
echo ""
SETUP_CODE_FILE=/var/lib/portly/setup-code.txt
if [ -f "$SETUP_CODE_FILE" ]; then
	echo "No admin account yet. Open the panel and enter this one-time setup"
	echo "code to create one (you'll pick your own username/password there):"
	echo ""
	echo "  $(cat "$SETUP_CODE_FILE")"
	echo ""
else
	echo "An admin account already exists on this install — log in as usual."
	echo ""
fi
echo "Optional: open the panel, go to Setup, and point a domain (e.g."
echo "panel.example.com) at ${PUBLIC_IP} via an A/AAAA record — Portly gets"
echo "you a free Let's Encrypt certificate automatically and the panel then"
echo "becomes reachable at https://your-domain instead."
echo ""
echo "Remember: any cloud firewall / security group in front of this VPS needs"
echo "${CONTROL_PORT}/tcp, ${WEB_PORT}/tcp, and ${HTTPS_PORT}/tcp opened too (ufw alone"
echo "isn't enough behind e.g. Hetzner/AWS/DigitalOcean firewalls). Each tunnel's"
echo "public port needs the same treatment once you create it."
echo ""
echo "Logs: journalctl -u portly-server -f   |   journalctl -u portly-web -f"
