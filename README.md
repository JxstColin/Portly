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
└───────────────────┘   │         │   │  :80/:443  API + web UI   │
         ▲               │  yamux  │   │  :25565 public listener   │
         │        ┌──────┴────/────┴──────┐              ▲       │
         └────────┤ portly-client          │              │       │
                   │ (outbound TLS only)    │◄─────────────┘       │
                   └────────────────────────┘      players connect here
```

- **No IP/host to configure.** `portly-server` auto-detects this machine's
  public IP on its own; the panel is reachable at `http://<that IP>` right
  after setup, no flags needed.
- **Optional domain + automatic HTTPS.** Point a domain's A/AAAA record at
  the VPS and set it in the web UI's Setup page — Portly requests and
  renews a free Let's Encrypt certificate for it automatically, and the
  panel becomes reachable at `https://your-domain`.
- **Adding a machine is one command.** The web UI's "Add machine" button
  gives you a single `curl | sudo bash` line, valid for 15 minutes and
  single-use. It downloads the right prebuilt `portly-client` binary,
  authenticates, writes its config, and starts it as a systemd service —
  nothing to copy by hand.
- The client never needs inbound ports open on your home router/firewall —
  only an outbound connection to the VPS.
- Tunnel definitions (which local port maps to which public VPS port,
  TCP or UDP) are configured **server-side** (via the web UI or the CLI)
  and pushed to the client automatically after it connects.
- New/removed tunnels take effect on the running server **without a
  restart**: a background reconciliation loop opens and closes public
  listeners dynamically.
- Deleting a machine tells it to uninstall itself — live, if it's currently
  connected; otherwise the next time it tries to reconnect, since its
  token is revoked immediately. Either way it removes its own systemd
  service, config, and binary rather than being left behind as an orphan.
- The control-plane (tunnel clients) generates its own self-signed CA on
  first run; the client pins its SHA-256 fingerprint instead of relying on
  a public CA, so there's no extra TLS setup required there either.
- The web UI (Next.js) runs as its **own process**; `portly-server`
  reverse-proxies it onto the same public origin as the API, so the
  browser only ever talks to one address/port — no CORS, and nothing to
  rebuild if you add a domain later.

## Quickstart

### 1. Set up the VPS — one command

```bash
curl -fsSL "https://raw.githubusercontent.com/JxstColin/Portly/main/scripts/quickstart-vps.sh?$(date +%s)" | sudo bash
```

No host, IP, or domain to pass — `portly-server` auto-detects this
machine's public IP on its own. (The `?$(date +%s)` busts GitHub's
raw-content CDN cache, which otherwise can serve a stale copy of the
script for a few minutes after a fix is pushed — always include it rather
than a bare URL.)

This installs Go and Node.js if needed, builds `portly-server` (with
prebuilt client binaries embedded for the installer below) and the web UI,
installs both as systemd services, opens the relevant `ufw` ports if `ufw`
is active, and prints the detected IP and default login. **Any cloud
firewall/security group in front of the VPS (Hetzner, AWS, DigitalOcean,
…) needs the same ports opened separately** — `ufw` alone doesn't cover
that.

Already have the repo cloned? Run `sudo ./scripts/quickstart-vps.sh` from
its root instead — no CDN involved either way.

### 2. Open the web UI

Visit `http://<the detected IP>` (printed at the end of step 1), log in
with `admin` / `portly`, and set a new username/password when prompted
(required on first login).

### 3. (Optional) Point a domain at it

Open **Setup** in the web UI. It shows the server's detected public IP —
create an A record (and AAAA for IPv6) for your domain pointing at it,
e.g. `panel.example.com` → that IP, then enter the domain and click
**Activate**. Portly requests a free Let's Encrypt certificate for it
automatically; once issued (usually a few seconds after DNS resolves),
the panel is reachable at `https://panel.example.com` — no reverse proxy
or manual TLS setup needed. Skip this step entirely and the panel just
keeps working over plain HTTP on the IP address.

### 4. Add a machine

Click **Add machine**, name it, and run the single command it gives you on
the machine that hosts your local service:

```bash
curl -fsSL 'http://<ip-or-domain>/install.sh?code=XXXXXXXXXX' | sudo bash
```

The dialog shows "Connected!" once the client comes online — no manual
token, config file, or service setup needed.

### 5. Add a tunnel

Open the machine's detail page, click **Add tunnel**, and map a local
port (on that machine) to a public port (on the VPS) — e.g. local `25565` →
public `25565` for Minecraft. It goes live immediately; the running client
picks it up automatically.

## Updating

**VPS (`portly-server` + web UI):** re-run the exact same one-liner from
step 1 — it's also the update command:

```bash
curl -fsSL "https://raw.githubusercontent.com/JxstColin/Portly/main/scripts/quickstart-vps.sh?$(date +%s)" | sudo bash
```

It pulls the latest code, rebuilds `portly-server` and the web UI, and
restarts both services in place. Already-cloned the repo? `git pull &&
sudo ./scripts/quickstart-vps.sh` from its root does the same thing
without going through GitHub's raw-content CDN.

**Machines (`portly-client`):** nothing to do — every enrolled machine
checks the VPS for a newer client binary roughly every 15 minutes (with a
random startup delay so a whole fleet doesn't hit the server at once), and
if the one embedded in your updated `portly-server` differs, it downloads
it, verifies its checksum, swaps it in atomically, and restarts itself
into it — no reinstall, no downtime beyond a brief reconnect. This means
that after you update the VPS, every machine picks up client-side fixes
on its own within about 15 minutes.

Machines enrolled before this feature shipped don't have the update
checker wired up yet (their config predates it) — re-run their install
command once (**Add machine** → same machine name is fine, it just
re-enrolls) and they'll self-update automatically from then on.

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
`--control-addr` (default `:7000`, tunnel clients), `--web-addr` (default
`:80`, the public entry point — API, install links, ACME challenges, and
the reverse-proxied UI all on one origin), `--https-addr` (default `:443`,
active once a domain is set), `--web-upstream` (default
`http://127.0.0.1:3000`, where the Next.js UI process is), `--api-addr`
(default `:8080`, a direct CORS-enabled listener — mainly for local dev),
`--advertise-host` (repeatable; hosts/IPs for the control-plane TLS cert
and install links — auto-detects this machine's public IP if left unset),
`--allowed-origin` (repeatable; browser origins allowed to call `--api-addr`
with credentials — irrelevant to `--web-addr`, which is same-origin).

`tunnel add` takes `--protocol tcp` (default) or `--protocol udp` — pick UDP
for things like game servers or WireGuard that don't use TCP. Both are
selectable the same way in the web UI's "Add tunnel" form.

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

- `portly-server.service` — works as-is (auto-detects the public IP); add
  `--advertise-host` yourself only if detection fails or you want a fixed
  value.
- `portly-web.service` — the Next.js UI, bound to `127.0.0.1` only since
  `portly-server` reverse-proxies it.
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

For the web UI, build it (`cd web && npm install && npm run build`), then
install `deploy/systemd/portly-web.service` the same way (adjust
`WorkingDirectory`/`ExecStart` if your checkout isn't at
`/opt/portly-src`) — or just use `scripts/quickstart-vps.sh`, which does
all of this in one step.

## Security notes

- Client tokens are 256-bit random values; only their SHA-256 hash is stored
  server-side. The "Add machine" flow never displays the token itself —
  only a short-lived, single-use enrollment code that `portly-client enroll`
  exchanges for it.
- The control-plane connection is always TLS, authenticated by pinning the
  server certificate's fingerprint (no public CA required).
- `--web-addr` (default `:80`) is plain HTTP until a domain is configured;
  set one in the Setup page for real HTTPS via an automatically-issued
  Let's Encrypt certificate. Without a domain, the panel and admin login
  run over plain HTTP on the IP — fine for casual/trusted use, but the
  session cookie and credentials aren't encrypted in transit until you do.
- Certificates are only ever requested for the exact domain configured in
  the Setup page (`autocert`'s `HostPolicy` rejects anything else), so
  pointing unrelated DNS at your VPS can't be used to make it issue
  certificates for other names.
- `--advertise-host` auto-detects this machine's public IP if left unset —
  pass it explicitly if detection fails (no outbound internet) or you want
  a specific value baked into the control-plane cert's SAN list.
- Public tunnel ports currently have no range restriction; pick ports that
  don't collide with the control-plane/web/HTTPS ports or other services
  on the VPS.
- `portly-client`'s self-update checks/downloads go over the same
  `api_base` URL it enrolled through (HTTPS once you've set a domain, plain
  HTTP otherwise) and verify the downloaded binary's sha256 against the
  hash the server itself reports — this catches corruption/partial
  downloads, but trusts whatever `portly-server` is currently serving,
  same as the initial install script does. There's no separate release
  signing step.

## Project layout

```
cmd/portly-server        control-plane + API + CLI entrypoint
cmd/portly-client        client entrypoint (run / init / enroll)
internal/tunnel          yamux-based control-plane and data-plane
internal/api             REST/WebSocket management API, install.sh, embedded client binaries
internal/db              SQLite schema and queries
internal/tlsutil         self-signed CA + fingerprint-pinned TLS (control-plane)
internal/netutil         public IP auto-detection
internal/config          client config file (YAML)
web/                      Next.js + Tailwind web UI (separate process)
scripts/                 install-go.sh, quickstart-vps.sh
deploy/systemd/          manual-setup unit file templates
```

## Roadmap

Done: Go tunnel core (server + client, TCP and UDP tunnels, dynamic
reconciliation, real-time traffic accounting, machines auto-uninstall
themselves when deleted), the management API, the one-command "Add
machine" installer, the Next.js/Tailwind web UI (clients, tunnels, live +
historical bandwidth, forced first-login credential change), and zero-config
setup (public IP auto-detection, optional domain + automatic Let's Encrypt
HTTPS from the Setup page).

Ideas for later: traffic quotas/limits with automatic pause, alert delivery
beyond the in-UI dashboard (e.g. webhooks), Layer-7 HTTP/HTTPS tunnels
(domain-based routing), multi-user
accounts.
