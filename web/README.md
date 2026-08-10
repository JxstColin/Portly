# Portly web UI

Next.js + Tailwind management UI for Portly. Runs as its own process,
separate from `portly-server`, and talks to it over the `NEXT_PUBLIC_API_BASE`
REST/WebSocket API (see the repo root [README](../README.md) for the full
picture).

## Development

```bash
cp .env.local.example .env.local   # point NEXT_PUBLIC_API_BASE at your portly-server --api-addr
npm install
npm run dev
```

Requires a running `portly-server` with `--allowed-origin http://localhost:3000`
(the default) so the browser's cookie-authenticated requests are accepted.

## Production

```bash
npm install
npm run build
npm start
```
