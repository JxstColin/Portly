CREATE TABLE IF NOT EXISTS admin_users (
    id                    TEXT PRIMARY KEY,
    username              TEXT NOT NULL UNIQUE,
    password_hash         TEXT NOT NULL,
    must_change_password  INTEGER NOT NULL DEFAULT 1,
    created_at            INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS clients (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,
    token_hash  TEXT NOT NULL UNIQUE,
    created_at  INTEGER NOT NULL,
    last_seen   INTEGER
);

CREATE TABLE IF NOT EXISTS tunnels (
    id                  TEXT PRIMARY KEY,
    client_id           TEXT NOT NULL REFERENCES clients(id) ON DELETE CASCADE,
    name                TEXT NOT NULL,
    local_host          TEXT NOT NULL,
    local_port          INTEGER NOT NULL,
    public_port         INTEGER NOT NULL UNIQUE,
    protocol            TEXT NOT NULL DEFAULT 'tcp',
    enabled             INTEGER NOT NULL DEFAULT 1,
    traffic_limit_bytes INTEGER,
    bytes_in_total      INTEGER NOT NULL DEFAULT 0,
    bytes_out_total     INTEGER NOT NULL DEFAULT 0,
    created_at          INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_tunnels_client_id ON tunnels(client_id);

CREATE TABLE IF NOT EXISTS traffic_samples (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    tunnel_id  TEXT NOT NULL REFERENCES tunnels(id) ON DELETE CASCADE,
    ts         INTEGER NOT NULL,
    bytes_in   INTEGER NOT NULL,
    bytes_out  INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_traffic_samples_tunnel_ts ON traffic_samples(tunnel_id, ts);

-- Tokens of deleted clients, so a machine that's still out there (offline
-- when it was removed in the UI) gets told to uninstall itself the next
-- time it tries to reconnect, instead of just being rejected forever.
CREATE TABLE IF NOT EXISTS revoked_tokens (
    token_hash  TEXT PRIMARY KEY,
    revoked_at  INTEGER NOT NULL
);

-- Short-lived, single-use codes shown as part of the 'Add machine' install
-- command. portly-client's 'enroll' subcommand exchanges one of these for
-- the client's real long-lived token, so the token itself never has to be
-- copy-pasted by hand.
-- token is the plaintext client token, held here only until the code is
-- exchanged (or it expires); the row is deleted immediately after exchange.
CREATE TABLE IF NOT EXISTS enrollment_codes (
    code_hash   TEXT PRIMARY KEY,
    client_id   TEXT NOT NULL REFERENCES clients(id) ON DELETE CASCADE,
    token       TEXT NOT NULL,
    created_at  INTEGER NOT NULL,
    expires_at  INTEGER NOT NULL
);
