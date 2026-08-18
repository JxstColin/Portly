package db

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// TestMigrateTunnelsPublicPortNullable simulates upgrading a real,
// pre-existing production database (public_port still NOT NULL UNIQUE, as
// every install had it before this change) and verifies the rebuild
// preserves every row exactly, relaxes the constraint, and that a fresh
// hostname-routed tunnel (public_port = 0/NULL) can then be created
// alongside the untouched dedicated-port ones.
func TestMigrateTunnelsPublicPortNullable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "portly.db")

	// Build the pre-migration schema by hand, exactly as a real upgrading
	// install would have it: all prior ADD COLUMN migrations already
	// applied, but public_port still NOT NULL UNIQUE.
	raw, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	if _, err := raw.Exec(`
		CREATE TABLE clients (
			id          TEXT PRIMARY KEY,
			name        TEXT NOT NULL UNIQUE,
			token_hash  TEXT NOT NULL UNIQUE,
			created_at  INTEGER NOT NULL,
			last_seen   INTEGER,
			traffic_limit_bytes INTEGER
		);
		CREATE TABLE tunnels (
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
			created_at          INTEGER NOT NULL,
			public_hostname     TEXT NOT NULL DEFAULT '',
			proxy_protocol      INTEGER NOT NULL DEFAULT 0
		);
		CREATE INDEX idx_tunnels_client_id ON tunnels(client_id);
		CREATE TABLE traffic_samples (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			tunnel_id  TEXT NOT NULL REFERENCES tunnels(id) ON DELETE CASCADE,
			ts         INTEGER NOT NULL,
			bytes_in   INTEGER NOT NULL,
			bytes_out  INTEGER NOT NULL
		);
	`); err != nil {
		t.Fatalf("create pre-migration schema: %v", err)
	}

	if _, err := raw.Exec(`INSERT INTO clients (id, name, token_hash, created_at) VALUES ('c1', 'test-client', 'hash1', 1000)`); err != nil {
		t.Fatalf("insert client: %v", err)
	}
	if _, err := raw.Exec(`
		INSERT INTO tunnels (id, client_id, name, local_host, local_port, public_port, protocol, enabled,
			bytes_in_total, bytes_out_total, created_at, public_hostname, proxy_protocol)
		VALUES
			('t1', 'c1', 'survival', '127.0.0.1', 25565, 30001, 'tcp', 1, 111, 222, 1000, 'survival.mc.example.com', 0),
			('t2', 'c1', 'creative', '127.0.0.1', 25566, 30002, 'tcp', 1, 333, 444, 1001, '', 0)
	`); err != nil {
		t.Fatalf("insert tunnels: %v", err)
	}
	if _, err := raw.Exec(`INSERT INTO traffic_samples (tunnel_id, ts, bytes_in, bytes_out) VALUES ('t1', 1000, 50, 60)`); err != nil {
		t.Fatalf("insert traffic sample: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw handle: %v", err)
	}

	// This is the real upgrade path: Open() applies schema.sql (no-op,
	// tables already exist) then runs migrateSchema, which should detect
	// public_port is still NOT NULL and perform the rebuild.
	database, err := Open(path)
	if err != nil {
		t.Fatalf("Open (migration): %v", err)
	}
	defer database.Close()

	notNull, err := columnNotNull(database.sql, "tunnels", "public_port")
	if err != nil {
		t.Fatalf("columnNotNull: %v", err)
	}
	if notNull {
		t.Fatal("public_port is still NOT NULL after migration")
	}

	tunnels, err := database.ListAllTunnels()
	if err != nil {
		t.Fatalf("ListAllTunnels: %v", err)
	}
	if len(tunnels) != 2 {
		t.Fatalf("expected 2 tunnels preserved, got %d", len(tunnels))
	}

	byID := map[string]Tunnel{}
	for _, tn := range tunnels {
		byID[tn.ID] = tn
	}

	t1, ok := byID["t1"]
	if !ok {
		t.Fatal("tunnel t1 missing after migration")
	}
	if t1.PublicPort != 30001 || t1.PublicHostname != "survival.mc.example.com" || t1.BytesInTotal != 111 || t1.BytesOutTotal != 222 {
		t.Fatalf("t1 data not preserved exactly: %+v", t1)
	}

	t2, ok := byID["t2"]
	if !ok {
		t.Fatal("tunnel t2 missing after migration")
	}
	if t2.PublicPort != 30002 {
		t.Fatalf("t2 public_port not preserved: %+v", t2)
	}

	// Foreign key relationship (traffic_samples -> tunnels) must have
	// survived the rebuild intact.
	var sampleCount int
	if err := database.sql.QueryRow(`SELECT COUNT(*) FROM traffic_samples WHERE tunnel_id = 't1'`).Scan(&sampleCount); err != nil {
		t.Fatalf("query traffic_samples: %v", err)
	}
	if sampleCount != 1 {
		t.Fatalf("expected 1 traffic sample preserved, got %d", sampleCount)
	}

	// Hostname lookup must still resolve the pre-existing tunnel correctly.
	found, err := database.GetTunnelByHostname("survival.mc.example.com")
	if err != nil {
		t.Fatalf("GetTunnelByHostname: %v", err)
	}
	if found.ID != "t1" {
		t.Fatalf("GetTunnelByHostname resolved wrong tunnel: %+v", found)
	}

	// The whole point: a new hostname-routed tunnel (no dedicated port)
	// must now be createable without colliding on the UNIQUE constraint,
	// and a second one right after it must work too (multiple NULLs).
	hr1, err := database.CreateTunnel("c1", "lobby", "127.0.0.1", 25567, 0, "tcp")
	if err != nil {
		t.Fatalf("CreateTunnel with public_port=0 (first): %v", err)
	}
	if hr1.PublicPort != 0 {
		t.Fatalf("expected PublicPort 0 for hostname-routed tunnel, got %d", hr1.PublicPort)
	}
	hr2, err := database.CreateTunnel("c1", "skyblock", "127.0.0.1", 25568, 0, "tcp")
	if err != nil {
		t.Fatalf("CreateTunnel with public_port=0 (second): %v", err)
	}
	if hr2.PublicPort != 0 {
		t.Fatalf("expected PublicPort 0 for second hostname-routed tunnel, got %d", hr2.PublicPort)
	}

	// Re-running Open() (as happens on every server restart) must be a
	// pure no-op now that the migration already applied.
	database.Close()
	database2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open (idempotency check): %v", err)
	}
	defer database2.Close()
	tunnels2, err := database2.ListAllTunnels()
	if err != nil {
		t.Fatalf("ListAllTunnels after second Open: %v", err)
	}
	if len(tunnels2) != 4 {
		t.Fatalf("expected 4 tunnels after second Open, got %d", len(tunnels2))
	}
}

// TestHostnameTaken verifies the uniqueness check used by
// handleUpdateTunnelSettings correctly ignores the tunnel being updated
// itself but catches a genuine collision with another tunnel.
func TestHostnameTaken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "portly.db")
	database, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()

	client, _, err := database.CreateClient("test-client")
	if err != nil {
		t.Fatalf("CreateClient: %v", err)
	}

	t1, err := database.CreateTunnel(client.ID, "one", "127.0.0.1", 25565, 0, "tcp")
	if err != nil {
		t.Fatalf("CreateTunnel t1: %v", err)
	}
	if err := database.UpdateTunnelSettings(t1.ID, nil, "shared.example.com", false); err != nil {
		t.Fatalf("UpdateTunnelSettings t1: %v", err)
	}

	t2, err := database.CreateTunnel(client.ID, "two", "127.0.0.1", 25566, 0, "tcp")
	if err != nil {
		t.Fatalf("CreateTunnel t2: %v", err)
	}

	taken, err := database.HostnameTaken("shared.example.com", t2.ID)
	if err != nil {
		t.Fatalf("HostnameTaken (t2 checking t1's hostname): %v", err)
	}
	if !taken {
		t.Fatal("expected shared.example.com to be reported taken by another tunnel")
	}

	notTaken, err := database.HostnameTaken("shared.example.com", t1.ID)
	if err != nil {
		t.Fatalf("HostnameTaken (t1 checking its own hostname): %v", err)
	}
	if notTaken {
		t.Fatal("a tunnel's own hostname must not be reported as taken against itself")
	}
}
