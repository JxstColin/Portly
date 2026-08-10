# Portly

Self-hosted reverse tunnel: expose a local service (e.g. a Minecraft server on
port 25565) through a VPS you control, without opening any ports on your home
router — managed through a web UI where adding a machine is a single
copy-pasted command.

A lightweight client runs on a machine in your local network and opens an
outbound connection to `portly-server` on your VPS. All tunneled traffic is
multiplexed over that single connection (like ngrok, frp, or rathole).

## How it works

```
 Home network                                VPS
┌───────────────────┐                 ┌──────────────────────────┐
│ Minecraft server   │                 │  portly-server            │
│ 127.0.0.1:25565    │◄──┐         ┌──►│  :7000  control-plane     │
└───────────────────┘   │         │   │  :8080  API (+ web UI)    │
         ▲               │  yamux  │   │  :25565 public listener   │
         │        ┌──────┴────/────┴──────┐              ▲       │
         └────────┤ portly-client          │              │       │
                   │ (outbound TLS only)    │◄─────────────┘       │
                   └────────────────────────┘      players connect here
```

- **Adding a machine is one command.** The web UI's "Add machine" button
  gives you a single `curl | sudo bash` line, valid for 15 minutes and
  single-use. It downloads the right prebuilt `portly-client` binary,
  authenticates, writes its config, and starts it as a systemd service —
  nothing to copy by hand.
- The client never needs inbound ports open on your home router/firewall —
  only an outbound connection to the VPS.
- Tunnel definitions (which local port maps to which public VPS port) are
  configured **server-side** (via the web UI or the CLI) and pushed to the
  client automatically after it connects.
- New/removed tunnels take effect on the running server **without a
  restart**: a background reconciliation loop opens and closes public
  listeners dynamically.
- The server generates its own self-signed CA on first run; the client pins
  its SHA-256 fingerprint instead of relying on a public CA, so there's no
  extra TLS setup required.
- The web UI (Next.js) runs as its **own process**, separate from the tunnel
  engine (`portly-server`) — it talks to it over the network, so it stays up
  independently of tunnel/client activity.

## Quickstart

### 1. Set up the VPS — one command

```bash
curl -fsSL https://raw.githubusercontent.com/JxstColin/Portly/main/scripts/quickstart-vps.sh \
  | sudo bash -s -- --host YOUR_VPS_IP_OR_DOMAIN
```

This installs Go if needed, builds `portly-server` (with prebuilt client
binaries embedded for the installer below), installs it as a systemd
service, opens the relevant `ufw` ports if `ufw` is active, and prints the
default login. **Any cloud firewall/security group in front of the VPS
(Hetzner, AWS, DigitalOcean, …) needs the same ports opened separately** —
`ufw` alone doesn't cover that.

Already have the repo cloned? Run `sudo ./scripts/quickstart-vps.sh --host
YOUR_VPS_IP_OR_DOMAIN` from its root instead.

### 2. Open the web UI

Visit `http://YOUR_VPS_IP_OR_DOMAIN:8080`, log in with `admin` / `portly`,
and set a new username/password when prompted (required on first login).

Put a reverse proxy with real TLS in front of port 8080 if you want the UI
reachable over HTTPS — Portly's own API listens on plain HTTP by default,
suited to a private/trusted network or a proxy doing TLS termination.

### 3. Add a machine

Click **Add machine**, name it, and run the single command it gives you on
the machine that hosts your local service:

```bash
curl -fsSL 'http://YOUR_VPS_IP_OR_DOMAIN:8080/install.sh?code=XXXXXXXXXX' | sudo bash
```

The dialog shows "Connected!" once the client comes online — no manual
token, config file, or service setup needed.

### 4. Add a tunnel

Open the machine's detail page, click **Add tunnel**, and map a local
port (on that machine) to a public port (on the VPS) — e.g. local `25565` →
public `25565` for Minecraft. It goes live immediately; the running client
picks it up automatically.

## CLI-only path (no web UI)

Everything above is also available from the CLI, useful for scripting or if
you'd rather not run the web UI:

```bash
# on the VPS
portly-server --data-dir /var/lib/portly run &
portly-server --data-dir /var/lib/portly client add my-homelab
portly-server --data-dir /var/lib/portly tunnel add \
  --client my-homelab --local-host 127.0.0.1 \
  --local-port 25565 --public-port 25565 --name minecraft

# on the local machine
portly-client init --server-addr "YOUR_VPS_IP_OR_DOMAIN:7000" \
  --token "ptly_..." --ca-fingerprint "..."
portly-client run
```

```
portly-server run                       Start the server (control-plane + API + listeners)
portly-server client add <name>         Register a client, print its token
portly-server client list               List registered clients
portly-server client rm <id>            Delete a client (and its tunnels)
portly-server tunnel add [flags]        Create a tunnel (see --help)
portly-server tunnel list               List all tunnels
portly-server tunnel rm <id>            Delete a tunnel

portly-client init [flags]              Write portly-client.yaml manually
portly-client enroll --api <url> --code <code>   Exchange a web-UI enrollment code (used by /install.sh)
portly-client run                       Connect and service tunnels
```

Global flags on `portly-server`: `--data-dir` (default `/var/lib/portly`),
`--control-addr` (default `:7000`), `--api-addr` (default `:8080`),
`--advertise-host` (repeatable; hosts/IPs embedded in the TLS cert and used
to build install links — set this to your VPS's public IP/domain),
`--allowed-origin` (repeatable; browser origins allowed to call the API with
credentials — add your web UI's origin if it's not `localhost:3000`).

## Building from source

Requires Go 1.25+ (`scripts/install-go.sh` installs it if missing) and
Node.js 20+ for the web UI.

```bash
make build          # cross-compiles client binaries, builds portly-server (with them embedded) and portly-client
cd web && npm install && npm run build && npm start   # web UI, separate process
```

`make build-clientbins` alone cross-compiles just the embedded client
binaries (linux/amd64, linux/arm64, darwin/amd64, darwin/arm64) into
`internal/api/clientbins/` — `portly-server` needs these to exist *before*
it's built, since it embeds them via `go:embed`. Windows isn't prebuilt
(the `curl | sudo bash` installer doesn't apply there); cross-compile it
directly instead:

```bash
GOOS=windows GOARCH=amd64 go build -o portly-client.exe ./cmd/portly-client
```

## Running as a service

`scripts/quickstart-vps.sh` handles this for the server automatically. For
manual setups, unit files are in [`deploy/systemd`](deploy/systemd):

- `portly-server.service` — edit the `--advertise-host`/`--allowed-origin`
  values before installing.
- `portly-client.service` — only needed for the manual/CLI path; the
  `/install.sh` flow (`portly-client enroll`) writes its own equivalent unit
  automatically when systemd is detected.

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
  server-side. The "Add machine" flow never displays the token itself —
  only a short-lived, single-use enrollment code that `portly-client enroll`
  exchanges for it.
- The control-plane connection is always TLS, authenticated by pinning the
  server certificate's fingerprint (no public CA required).
- The management API (`--api-addr`, default `:8080`) is plain HTTP by
  design, so the enrollment exchange and admin login work without extra TLS
  setup out of the box. Put a reverse proxy with real TLS in front of it if
  you're exposing it beyond a trusted network.
- `--advertise-host` defaults to `localhost`/`127.0.0.1` for local testing —
  set it to your VPS's real address before generating install links, since
  it's baked into the cert's SAN list and every generated URL.
- Public tunnel ports currently have no range restriction; pick ports that
  don't collide with the control-plane/API ports or other services on the
  VPS.

## Project layout

```
cmd/portly-server        control-plane + API + CLI entrypoint
cmd/portly-client        client entrypoint (run / init / enroll)
internal/tunnel          yamux-based control-plane and data-plane
internal/api             REST/WebSocket management API, install.sh, embedded client binaries
internal/db              SQLite schema and queries
internal/tlsutil         self-signed CA + fingerprint-pinned TLS
internal/config          client config file (YAML)
web/                      Next.js + Tailwind web UI (separate process)
scripts/                 install-go.sh, quickstart-vps.sh
deploy/systemd/          manual-setup unit file templates
```

## Roadmap

Done: Go tunnel core (server + client, TCP tunnels, dynamic reconciliation,
traffic accounting), the management API, the one-command "Add machine"
installer, and the Next.js/Tailwind web UI (clients, tunnels, live +
historical bandwidth, forced first-login credential change).

Ideas for later: traffic quotas/limits with automatic pause, alert delivery
beyond the in-UI dashboard (e.g. webhooks), UDP/HTTP tunnels, multi-user
accounts.
