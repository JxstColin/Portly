# Portly

Self-hosted reverse tunnel: expose a local service (e.g. a Minecraft server on
port 25565) through a VPS you control, without opening any ports on your home
router.

A lightweight client runs on a machine in your local network and opens an
outbound connection to `portly-server` on your VPS. All tunneled traffic is
multiplexed over that single connection (like ngrok, frp, or rathole). A web
UI for managing clients, tunnels and traffic is planned (see
[Roadmap](#roadmap)) — the tunnel core works standalone via the CLI today.

## How it works

```
 Home network                                VPS
┌───────────────────┐                 ┌──────────────────────────┐
│ Minecraft server   │                 │  portly-server            │
│ 127.0.0.1:25565    │◄──┐         ┌──►│  :7000  control-plane     │
└───────────────────┘   │         │   │  :25565 public listener   │
         ▲               │  yamux  │   └──────────────────────────┘
         │        ┌──────┴────/────┴──────┐              ▲
         └────────┤ portly-client          │              │
                   │ (outbound TLS only)    │◄─────────────┘
                   └────────────────────────┘      players connect here
```

- The client authenticates with a per-client token and never needs inbound
  ports open on your home router/firewall.
- Tunnel definitions (which local port maps to which public VPS port) are
  configured **server-side** via the CLI (later: the web UI) and pushed to
  the client automatically after it connects — the client config file only
  needs the server address, token, and a pinned certificate fingerprint.
- New/removed tunnels take effect on the running server **without a
  restart**: a background reconciliation loop opens and closes public
  listeners dynamically.
- The server generates its own self-signed CA on first run; the client pins
  its SHA-256 fingerprint instead of relying on a public CA, so there's no
  extra TLS setup required.

## Building

Requires Go 1.25+. If your VPS doesn't have Go installed (or has an older
version), `scripts/install-go.sh` installs the latest release from go.dev
(Linux amd64/arm64/armv6l):

```bash
sudo ./scripts/install-go.sh
source /etc/profile.d/go.sh   # or start a new shell
```

Then build:

```bash
go build -o bin/portly-server ./cmd/portly-server
go build -o bin/portly-client ./cmd/portly-client
```

Cross-compile the client for other platforms as needed, e.g.:

```bash
GOOS=windows GOARCH=amd64 go build -o bin/portly-client.exe ./cmd/portly-client
GOOS=darwin  GOARCH=arm64 go build -o bin/portly-client-mac ./cmd/portly-client
```

## Quickstart

### 1. On the VPS: start the server

```bash
./portly-server --data-dir /var/lib/portly \
                 --control-addr :7000 \
                 --advertise-host YOUR_VPS_IP_OR_DOMAIN \
                 run
```

On first start this generates `/var/lib/portly/portly.db` (SQLite) plus a
self-signed CA and server certificate, and prints the certificate's
fingerprint. Keep the process running (see [systemd](#running-as-a-service)
for production use).

### 2. On the VPS: register a client

```bash
./portly-server --data-dir /var/lib/portly client add my-homelab
```

This prints a ready-to-use client config:

```
portly-client.yaml:
  server_addr: "YOUR_VPS_IP_OR_DOMAIN:7000"
  token: "ptly_..."
  ca_fingerprint: "..."
```

The token is shown once — copy it now.

### 3. On the VPS: define a tunnel

```bash
./portly-server --data-dir /var/lib/portly tunnel add \
  --client my-homelab \
  --local-host 127.0.0.1 \
  --local-port 25565 \
  --public-port 25565 \
  --name minecraft
```

`--local-host`/`--local-port` describe where the **client machine** should
dial (usually `127.0.0.1` and the service's port); `--public-port` is what
becomes reachable on the VPS.

### 4. On your local machine: configure and run the client

```bash
./portly-client init \
  --server-addr "YOUR_VPS_IP_OR_DOMAIN:7000" \
  --token "ptly_..." \
  --ca-fingerprint "..."

./portly-client run
```

The client authenticates, receives its tunnel config automatically, and
starts forwarding. Anyone connecting to `YOUR_VPS_IP_OR_DOMAIN:25565` now
reaches the Minecraft server on your local machine.

## CLI reference

```
portly-server run                       Start the server (control-plane + listeners)
portly-server client add <name>         Register a client, print its token
portly-server client list               List registered clients
portly-server client rm <id>            Delete a client (and its tunnels)
portly-server tunnel add [flags]        Create a tunnel (see --help)
portly-server tunnel list               List all tunnels
portly-server tunnel rm <id>            Delete a tunnel

portly-client init [flags]              Write portly-client.yaml
portly-client run                       Connect and service tunnels
```

Global flags on `portly-server`: `--data-dir` (default `/var/lib/portly`),
`--control-addr` (default `:7000`), `--advertise-host` (repeatable; hosts/IPs
embedded in the TLS certificate — set this to your VPS's public IP/domain).

## Running as a service

Unit files are in [`deploy/systemd`](deploy/systemd):

- `portly-server.service` — run on the VPS. Edit `--advertise-host` before
  installing.
- `portly-client.service` — run on the machine hosting your local service.
  Expects its config at `/etc/portly/portly-client.yaml`.

```bash
sudo useradd --system --no-create-home portly
sudo cp bin/portly-server /usr/local/bin/
sudo cp deploy/systemd/portly-server.service /etc/systemd/system/
sudo mkdir -p /var/lib/portly && sudo chown portly:portly /var/lib/portly
sudo systemctl daemon-reload
sudo systemctl enable --now portly-server
```

## Security notes

- Client tokens are 256-bit random values; only their SHA-256 hash is stored
  server-side.
- The control-plane connection is always TLS, authenticated by pinning the
  server certificate's fingerprint (no public CA required).
- `--advertise-host` defaults to `localhost`/`127.0.0.1` for local testing —
  set it to your VPS's real address before generating client configs, since
  it's baked into the cert's SAN list.
- Public tunnel ports currently have no range restriction; pick ports that
  don't collide with the control-plane port or other services on the VPS.

## Roadmap

Phase 1 (this repo today): Go tunnel core — server + client, TCP tunnels,
CLI-managed clients/tunnels, traffic accounting in SQLite.

Phase 2 (in progress): REST/WebSocket API on the server, plus a Next.js +
Tailwind web UI (run as its own process, so it stays up independently of the
tunnel engine) for managing clients, tunnels, live bandwidth graphs, traffic
quotas, and in-UI alerts.
