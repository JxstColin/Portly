#!/usr/bin/env bash
# Uninstalls portly-server + the web UI from this VPS: stops and disables
# both systemd services, removes their unit files, the installed binaries,
# the source checkout, and the system user — and, unless --keep-data is
# passed, all server data too (the SQLite DB: every client, tunnel, and the
# admin account; plus TLS certs). This is IRREVERSIBLE — back up
# /var/lib/portly first if you're not sure.
#
# Usage (as root):
#   curl -fsSL "https://raw.githubusercontent.com/JxstColin/Portly/main/scripts/uninstall-vps.sh?$(date +%s)" | sudo bash
#
# Flags:
#   --keep-data                keep /var/lib/portly (DB, certs, setup code)
#   --keep-source               keep /opt/portly-src (the git checkout)
#   --yes, -y                   skip the confirmation prompt
#   --control-port/--web-port/--https-port   only needed if you passed
#                                non-default values to quickstart-vps.sh, so
#                                the matching ufw rules get removed too
#
# This only removes the server-side install. Already-enrolled client
# machines aren't touched by this script — delete them from the panel
# first if you want them to self-uninstall, or run 'portly-client
# uninstall' directly on each one.
set -euo pipefail

SRC_DIR="/opt/portly-src"
DATA_DIR="/var/lib/portly"
CONTROL_PORT=7000
WEB_PORT=80
HTTPS_PORT=443
KEEP_DATA=0
KEEP_SOURCE=0
ASSUME_YES=0

log() { echo "[uninstall] $*"; }
die() { echo "[uninstall] error: $*" >&2; exit 1; }

while [ $# -gt 0 ]; do
	case "$1" in
	--keep-data) KEEP_DATA=1; shift ;;
	--keep-source) KEEP_SOURCE=1; shift ;;
	--yes | -y) ASSUME_YES=1; shift ;;
	--control-port) CONTROL_PORT="$2"; shift 2 ;;
	--web-port) WEB_PORT="$2"; shift 2 ;;
	--https-port) HTTPS_PORT="$2"; shift 2 ;;
	*) die "unknown argument: $1" ;;
	esac
done

[ "$(id -u)" -eq 0 ] || die "must run as root (use sudo)"

if [ "$ASSUME_YES" -ne 1 ]; then
	echo "This will stop and remove portly-server and the web UI from this machine."
	if [ "$KEEP_DATA" -ne 1 ]; then
		echo "It will also DELETE all server data in ${DATA_DIR} — every client,"
		echo "tunnel, and the admin account. This cannot be undone."
	fi
	printf "Continue? [y/N] "
	read -r answer
	case "$answer" in
	[yY]) ;;
	*)
		echo "Aborted."
		exit 0
		;;
	esac
fi

log "stopping services..."
systemctl stop portly-server portly-web 2>/dev/null || true
systemctl disable portly-server portly-web 2>/dev/null || true

log "removing systemd units..."
rm -f /etc/systemd/system/portly-server.service /etc/systemd/system/portly-web.service
systemctl daemon-reload 2>/dev/null || true

log "removing binaries..."
rm -f /usr/local/bin/portly-server /usr/local/bin/portly-client

if command -v ufw >/dev/null 2>&1 && ufw status | grep -q "Status: active"; then
	log "closing firewall ports ${CONTROL_PORT}, ${WEB_PORT}, and ${HTTPS_PORT}..."
	ufw delete allow "${CONTROL_PORT}/tcp" >/dev/null 2>&1 || true
	ufw delete allow "${WEB_PORT}/tcp" >/dev/null 2>&1 || true
	ufw delete allow "${HTTPS_PORT}/tcp" >/dev/null 2>&1 || true
fi

if [ "$KEEP_SOURCE" -ne 1 ]; then
	log "removing source checkout at ${SRC_DIR}..."
	rm -rf "$SRC_DIR"
fi

if [ "$KEEP_DATA" -ne 1 ]; then
	log "removing server data at ${DATA_DIR}..."
	rm -rf "$DATA_DIR"
else
	log "keeping server data at ${DATA_DIR} (--keep-data)"
fi

if id -u portly >/dev/null 2>&1; then
	log "removing 'portly' system user..."
	userdel portly 2>/dev/null || true
fi

log "done. portly-server and the web UI have been removed from this machine."
if [ "$KEEP_DATA" -eq 1 ]; then
	echo "Data kept at ${DATA_DIR} — reinstalling with quickstart-vps.sh will pick it back up as-is."
fi
