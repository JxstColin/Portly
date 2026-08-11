# Portly web UI

Next.js + Tailwind management UI for Portly. Runs as its own process,
separate from `portly-server` (see the repo root [README](../README.md)
for the full picture).

## Development

```bash
cp .env.local.example .env.local   # point NEXT_PUBLIC_API_BASE at your portly-server --api-addr
npm install
npm run dev
```

Requires a running `portly-server` with `--allowed-origin http://localhost:3000`
(the default) so the browser's cookie-authenticated requests are accepted
directly against `--api-addr` during development.

## Production

```bash
npm install
npm run build
npm start
```

No `NEXT_PUBLIC_API_BASE` needed here: `portly-server` reverse-proxies this
process onto its own public origin (`--web-addr`/`--https-addr`), so the UI
just calls the API relative to wherever it's being served from —
`scripts/quickstart-vps.sh` sets this up automatically.
