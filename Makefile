BIN_DIR := bin
CLIENTBINS_DIR := internal/api/clientbins

# OS/ARCH pairs portly-server embeds and can auto-install via /install.sh.
# Windows is deliberately excluded here — the curl|sudo bash installer
# doesn't apply there; Windows users cross-compile/build directly instead
# (see README).
CLIENT_TARGETS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64

.PHONY: build build-server build-client build-clientbins clean

## Cross-compiles the client for every target in CLIENT_TARGETS, then builds
## portly-server (which embeds them) and a portly-client for this machine.
build: build-clientbins build-server build-client

build-clientbins:
	@mkdir -p $(CLIENTBINS_DIR)
	@for target in $(CLIENT_TARGETS); do \
		os=$${target%/*}; arch=$${target#*/}; \
		echo "building portly-client for $$os/$$arch..."; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 go build -o $(CLIENTBINS_DIR)/portly-client-$$os-$$arch ./cmd/portly-client; \
	done

## Must run after build-clientbins so the embed picks up fresh binaries.
build-server:
	@mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/portly-server ./cmd/portly-server

build-client:
	@mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/portly-client ./cmd/portly-client

clean:
	rm -rf $(BIN_DIR) $(CLIENTBINS_DIR)/portly-client-*
