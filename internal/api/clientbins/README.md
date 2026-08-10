# clientbins

Prebuilt `portly-client` binaries get cross-compiled into this directory
(named `portly-client-<os>-<arch>`, e.g. `portly-client-linux-amd64`) by
`make build-clientbins` before `portly-server` is built, so it can embed and
serve them at `/downloads/portly-client-<os>-<arch>` for the one-command
"Add machine" installer (see `/install.sh`).

This README is the only file checked into git here — the actual binaries
are build artifacts (see `.gitignore`). Building `portly-server` without
running `make build-clientbins` first still works, it just won't have
anything to serve, so `/install.sh`-based installs will 404 until you do.
