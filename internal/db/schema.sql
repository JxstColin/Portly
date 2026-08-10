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
