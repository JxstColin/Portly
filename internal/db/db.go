// Package db provides Portly's SQLite-backed data layer: admin users,
// clients, tunnels, and traffic samples.
package db

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

const setupCodeSettingKey = "setup_code"

type DB struct {
	sql *sql.DB
}

func Open(path string) (*DB, error) {
	sqlDB, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	sqlDB.SetMaxOpenConns(1) // modernc sqlite driver: single writer, avoid SQLITE_BUSY

	if _, err := sqlDB.Exec(schemaSQL); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	if err := migrateSchema(sqlDB); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("migrate schema: %w", err)
	}

	return &DB{sql: sqlDB}, nil
}

// migrateSchema adds columns to tables that already existed before those
// columns were added to schema.sql — CREATE TABLE IF NOT EXISTS only
// creates missing tables, it never alters ones that already exist, so an
// upgrade needs this to pick up new columns on an already-deployed
// database. Each entry is idempotent: skipped if the column is already
// there (a brand-new database gets it straight from schema.sql instead).
func migrateSchema(sqlDB *sql.DB) error {
	migrations := []struct{ table, column, ddl string }{
		{"tunnels", "traffic_limit_bytes", `ALTER TABLE tunnels ADD COLUMN traffic_limit_bytes INTEGER`},
		{"tunnels", "public_hostname", `ALTER TABLE tunnels ADD COLUMN public_hostname TEXT NOT NULL DEFAULT ''`},
		{"tunnels", "proxy_protocol", `ALTER TABLE tunnels ADD COLUMN proxy_protocol INTEGER NOT NULL DEFAULT 0`},
		{"clients", "traffic_limit_bytes", `ALTER TABLE clients ADD COLUMN traffic_limit_bytes INTEGER`},
	}
	for _, m := range migrations {
		has, err := hasColumn(sqlDB, m.table, m.column)
		if err != nil {
			return fmt.Errorf("check %s.%s: %w", m.table, m.column, err)
		}
		if has {
			continue
		}
		if _, err := sqlDB.Exec(m.ddl); err != nil {
			return fmt.Errorf("add column %s.%s: %w", m.table, m.column, err)
		}
	}

	if err := migrateTunnelsPublicPortNullable(sqlDB); err != nil {
		return fmt.Errorf("migrate tunnels.public_port nullable: %w", err)
	}

	return nil
}

// migrateTunnelsPublicPortNullable relaxes tunnels.public_port from
// NOT NULL to nullable, so a tunnel can opt out of a dedicated public port
// and be reached only via the shared Minecraft hostname router instead.
// SQLite can't ALTER a column's NOT NULL constraint directly, so this
// rebuilds the table using SQLite's documented safe procedure:
// https://www.sqlite.org/lang_altertable.html#otheralter
// It's idempotent (skipped once the column is already nullable) and only
// ever runs once per database, on upgrade from an older version.
func migrateTunnelsPublicPortNullable(sqlDB *sql.DB) error {
	notNull, err := columnNotNull(sqlDB, "tunnels", "public_port")
	if err != nil {
		return fmt.Errorf("check public_port constraint: %w", err)
	}
	if !notNull {
		return nil // already migrated, or a fresh DB whose schema.sql already has it nullable
	}

	// foreign_keys is a no-op if toggled inside a transaction, so it must be
	// set here, before Begin() opens one.
	if _, err := sqlDB.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
		return fmt.Errorf("disable foreign keys: %w", err)
	}
	defer sqlDB.Exec(`PRAGMA foreign_keys=ON`)

	tx, err := sqlDB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		CREATE TABLE tunnels_new (
			id                  TEXT PRIMARY KEY,
			client_id           TEXT NOT NULL REFERENCES clients(id) ON DELETE CASCADE,
			name                TEXT NOT NULL,
			local_host          TEXT NOT NULL,
			local_port          INTEGER NOT NULL,
			public_port         INTEGER UNIQUE,
			protocol            TEXT NOT NULL DEFAULT 'tcp',
			enabled             INTEGER NOT NULL DEFAULT 1,
			traffic_limit_bytes INTEGER,
			bytes_in_total      INTEGER NOT NULL DEFAULT 0,
			bytes_out_total     INTEGER NOT NULL DEFAULT 0,
			created_at          INTEGER NOT NULL,
			public_hostname     TEXT NOT NULL DEFAULT '',
			proxy_protocol      INTEGER NOT NULL DEFAULT 0
		)
	`); err != nil {
		return fmt.Errorf("create tunnels_new: %w", err)
	}

	if _, err := tx.Exec(`
		INSERT INTO tunnels_new (id, client_id, name, local_host, local_port, public_port, protocol,
			enabled, traffic_limit_bytes, bytes_in_total, bytes_out_total, created_at, public_hostname, proxy_protocol)
		SELECT id, client_id, name, local_host, local_port, public_port, protocol,
			enabled, traffic_limit_bytes, bytes_in_total, bytes_out_total, created_at, public_hostname, proxy_protocol
		FROM tunnels
	`); err != nil {
		return fmt.Errorf("copy tunnels rows: %w", err)
	}

	if _, err := tx.Exec(`DROP TABLE tunnels`); err != nil {
		return fmt.Errorf("drop old tunnels: %w", err)
	}
	if _, err := tx.Exec(`ALTER TABLE tunnels_new RENAME TO tunnels`); err != nil {
		return fmt.Errorf("rename tunnels_new: %w", err)
	}
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_tunnels_client_id ON tunnels(client_id)`); err != nil {
		return fmt.Errorf("recreate index: %w", err)
	}

	rows, err := tx.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		return fmt.Errorf("foreign_key_check: %w", err)
	}
	violated := rows.Next()
	rows.Close()
	if violated {
		return fmt.Errorf("foreign key violation detected after tunnels table rebuild, aborting")
	}

	return tx.Commit()
}

// columnNotNull reports whether table.column currently has a NOT NULL
// constraint, via SQLite's PRAGMA table_info introspection.
func columnNotNull(sqlDB *sql.DB, table, column string) (bool, error) {
	rows, err := sqlDB.Query(fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == column {
			return notnull == 1, nil
		}
	}
	return false, rows.Err()
}

func hasColumn(sqlDB *sql.DB, table, column string) (bool, error) {
	// table is always one of our own hardcoded migration entries above,
	// never external input, so string-formatting it into PRAGMA (which
	// doesn't support query parameters for identifiers) is safe here.
	rows, err := sqlDB.Query(fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func (d *DB) Close() error {
	return d.sql.Close()
}

// --- Admin user (single-admin) ---

type AdminUser struct {
	ID                 string
	Username           string
	PasswordHash       string
	MustChangePassword bool
}

// HasAdminUser reports whether the first admin account has been created
// yet. Fresh installs start with none — the web UI's bootstrap flow (gated
// by the one-time setup code EnsureSetupCode prints) creates it, rather
// than seeding a default admin/portly account someone might forget to
// change.
func (d *DB) HasAdminUser() (bool, error) {
	var count int
	if err := d.sql.QueryRow(`SELECT COUNT(*) FROM admin_users`).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func (d *DB) GetAdminUser() (AdminUser, error) {
	var a AdminUser
	var mustChange int
	err := d.sql.QueryRow(`SELECT id, username, password_hash, must_change_password FROM admin_users LIMIT 1`).
		Scan(&a.ID, &a.Username, &a.PasswordHash, &mustChange)
	a.MustChangePassword = mustChange == 1
	return a, err
}

// CreateAdminUser inserts the very first (and only) admin account, e.g.
// from the bootstrap claim flow. must_change_password starts false since
// the admin just chose this password themselves, unlike the old seeded
// default.
func (d *DB) CreateAdminUser(username, passwordHash string) (AdminUser, error) {
	a := AdminUser{ID: uuid.NewString(), Username: username, PasswordHash: passwordHash}
	_, err := d.sql.Exec(
		`INSERT INTO admin_users (id, username, password_hash, must_change_password, created_at) VALUES (?, ?, ?, 0, ?)`,
		a.ID, a.Username, a.PasswordHash, time.Now().Unix(),
	)
	if err != nil {
		return AdminUser{}, err
	}
	return a, nil
}

func (d *DB) UpdateAdminCredentials(id, username, passwordHash string, mustChangePassword bool) error {
	_, err := d.sql.Exec(
		`UPDATE admin_users SET username = ?, password_hash = ?, must_change_password = ? WHERE id = ?`,
		username, passwordHash, boolToInt(mustChangePassword), id,
	)
	return err
}

// EnsureSetupCode returns the one-time code needed to claim the first admin
// account (via POST /api/bootstrap/claim), generating and persisting one on
// a fresh install if none exists yet. Returns ("", true, nil) once an admin
// already exists — there's nothing left to bootstrap.
func (d *DB) EnsureSetupCode() (code string, hasAdmin bool, err error) {
	hasAdmin, err = d.HasAdminUser()
	if err != nil {
		return "", false, err
	}
	if hasAdmin {
		return "", true, nil
	}
	if existing, ok, err := d.GetSetting(setupCodeSettingKey); err != nil {
		return "", false, err
	} else if ok {
		return existing, false, nil
	}
	code, err = randomCode(12)
	if err != nil {
		return "", false, err
	}
	if err := d.SetSetting(setupCodeSettingKey, code); err != nil {
		return "", false, err
	}
	return code, false, nil
}

// ClaimSetupCode validates code against the stored setup code and, on
// success, consumes it (so it can never be reused) — the caller still has
// to actually create the admin account afterwards.
func (d *DB) ClaimSetupCode(code string) error {
	stored, ok, err := d.GetSetting(setupCodeSettingKey)
	if err != nil {
		return err
	}
	if !ok || code == "" || stored != code {
		return fmt.Errorf("invalid setup code")
	}
	return d.DeleteSetting(setupCodeSettingKey)
}

// --- Clients ---

type Client struct {
	ID                string
	Name              string
	TokenHash         string
	CreatedAt         time.Time
	LastSeen          *time.Time
	TrafficLimitBytes *int64
}

// GenerateToken creates a new random client token and its SHA-256 hash
// (the hash is what's persisted; the plaintext token is shown once).
func GenerateToken() (token, hash string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	token = "ptly_" + hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(token))
	hash = hex.EncodeToString(sum[:])
	return token, hash, nil
}

func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (d *DB) CreateClient(name string) (Client, string, error) {
	token, hash, err := GenerateToken()
	if err != nil {
		return Client{}, "", err
	}
	c := Client{
		ID:        uuid.NewString(),
		Name:      name,
		TokenHash: hash,
		CreatedAt: time.Now(),
	}
	_, err = d.sql.Exec(
		`INSERT INTO clients (id, name, token_hash, created_at) VALUES (?, ?, ?, ?)`,
		c.ID, c.Name, c.TokenHash, c.CreatedAt.Unix(),
	)
	if err != nil {
		return Client{}, "", fmt.Errorf("insert client: %w", err)
	}
	return c, token, nil
}

// RotateClientToken replaces clientID's long-lived credential and returns
// the new plaintext token — used to re-issue an install command for a
// machine that never successfully connected with its original one (e.g.
// its enrollment code expired unused), without touching anything else.
func (d *DB) RotateClientToken(clientID string) (string, error) {
	token, hash, err := GenerateToken()
	if err != nil {
		return "", err
	}
	res, err := d.sql.Exec(`UPDATE clients SET token_hash = ? WHERE id = ?`, hash, clientID)
	if err != nil {
		return "", err
	}
	if n, err := res.RowsAffected(); err != nil {
		return "", err
	} else if n == 0 {
		return "", sql.ErrNoRows
	}
	return token, nil
}

const clientColumns = `id, name, token_hash, created_at, last_seen, traffic_limit_bytes`

func (d *DB) GetClientByTokenHash(hash string) (Client, error) {
	return d.scanClient(d.sql.QueryRow(`SELECT `+clientColumns+` FROM clients WHERE token_hash = ?`, hash))
}

func (d *DB) GetClientByID(id string) (Client, error) {
	return d.scanClient(d.sql.QueryRow(`SELECT `+clientColumns+` FROM clients WHERE id = ?`, id))
}

func (d *DB) scanClient(row *sql.Row) (Client, error) {
	var c Client
	var createdAt int64
	var lastSeen, limit sql.NullInt64
	if err := row.Scan(&c.ID, &c.Name, &c.TokenHash, &createdAt, &lastSeen, &limit); err != nil {
		return Client{}, err
	}
	c.CreatedAt = time.Unix(createdAt, 0)
	if lastSeen.Valid {
		t := time.Unix(lastSeen.Int64, 0)
		c.LastSeen = &t
	}
	if limit.Valid {
		v := limit.Int64
		c.TrafficLimitBytes = &v
	}
	return c, nil
}

func (d *DB) ListClients() ([]Client, error) {
	rows, err := d.sql.Query(`SELECT ` + clientColumns + ` FROM clients ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Client
	for rows.Next() {
		var c Client
		var createdAt int64
		var lastSeen, limit sql.NullInt64
		if err := rows.Scan(&c.ID, &c.Name, &c.TokenHash, &createdAt, &lastSeen, &limit); err != nil {
			return nil, err
		}
		c.CreatedAt = time.Unix(createdAt, 0)
		if lastSeen.Valid {
			t := time.Unix(lastSeen.Int64, 0)
			c.LastSeen = &t
		}
		if limit.Valid {
			v := limit.Int64
			c.TrafficLimitBytes = &v
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// UpdateClientTrafficLimit sets (or clears, with nil) a machine-wide
// traffic limit — the combined total across all of its tunnels. Enforced
// alongside each tunnel's own limit inside AddTunnelTraffic.
func (d *DB) UpdateClientTrafficLimit(id string, limitBytes *int64) error {
	res, err := d.sql.Exec(`UPDATE clients SET traffic_limit_bytes = ? WHERE id = ?`, limitBytes, id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// DeleteClient removes a client (cascading its tunnels) and revokes its
// token, so that if the machine is offline right now and reconnects later,
// the server can tell it to uninstall itself instead of just rejecting it.
func (d *DB) DeleteClient(id string) error {
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var tokenHash string
	err = tx.QueryRow(`SELECT token_hash FROM clients WHERE id = ?`, id).Scan(&tokenHash)
	if err != nil {
		return fmt.Errorf("find client: %w", err)
	}

	if _, err := tx.Exec(`DELETE FROM clients WHERE id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`INSERT OR REPLACE INTO revoked_tokens (token_hash, revoked_at) VALUES (?, ?)`,
		tokenHash, time.Now().Unix(),
	); err != nil {
		return err
	}
	return tx.Commit()
}

// FactoryReset wipes every client, tunnel, traffic sample, and the admin
// account, reverting the server to the same blank state as a fresh
// install — the next request to EnsureSetupCode generates a new setup
// code. Every current client's token is revoked first (same as
// DeleteClient), so any machine still out there gets told to uninstall
// itself the next time it tries to reconnect, rather than just failing
// silently. The control-plane TLS identity (CA/cert) is untouched.
func (d *DB) FactoryReset() error {
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	rows, err := tx.Query(`SELECT token_hash FROM clients`)
	if err != nil {
		return err
	}
	var hashes []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			rows.Close()
			return err
		}
		hashes = append(hashes, h)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()

	now := time.Now().Unix()
	for _, h := range hashes {
		if _, err := tx.Exec(
			`INSERT OR REPLACE INTO revoked_tokens (token_hash, revoked_at) VALUES (?, ?)`,
			h, now,
		); err != nil {
			return err
		}
	}

	// clients cascades to tunnels/enrollment_codes/traffic_samples via their
	// ON DELETE CASCADE foreign keys.
	for _, table := range []string{"clients", "admin_users", "server_settings"} {
		if _, err := tx.Exec(`DELETE FROM ` + table); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// IsTokenRevoked reports whether a token belonged to a client that has
// since been deleted.
func (d *DB) IsTokenRevoked(tokenHash string) bool {
	var count int
	err := d.sql.QueryRow(`SELECT COUNT(*) FROM revoked_tokens WHERE token_hash = ?`, tokenHash).Scan(&count)
	return err == nil && count > 0
}

func (d *DB) UpdateClientLastSeen(id string) error {
	_, err := d.sql.Exec(`UPDATE clients SET last_seen = ? WHERE id = ?`, time.Now().Unix(), id)
	return err
}

// --- Tunnels ---

type Tunnel struct {
	ID        string
	ClientID  string
	Name      string
	LocalHost string
	LocalPort int
	// PublicPort is 0 when the tunnel has no dedicated public port and is
	// only reachable through the shared Minecraft hostname router at
	// PublicHostname (stored as SQL NULL, not a real port).
	PublicPort        int
	Protocol          string
	Enabled           bool
	TrafficLimitBytes *int64
	PublicHostname    string
	ProxyProtocol     bool
	BytesInTotal      int64
	BytesOutTotal     int64
	CreatedAt         time.Time
}

// CreateTunnel creates a tunnel. publicPort of 0 means the tunnel has no
// dedicated public port and is only reachable via the shared Minecraft
// hostname router (see GetTunnelByHostname) — stored as SQL NULL so
// multiple such tunnels can coexist under the public_port UNIQUE
// constraint.
func (d *DB) CreateTunnel(clientID, name, localHost string, localPort, publicPort int, proto string) (Tunnel, error) {
	if proto == "" {
		proto = "tcp"
	}
	t := Tunnel{
		ID:         uuid.NewString(),
		ClientID:   clientID,
		Name:       name,
		LocalHost:  localHost,
		LocalPort:  localPort,
		PublicPort: publicPort,
		Protocol:   proto,
		Enabled:    true,
		CreatedAt:  time.Now(),
	}
	var publicPortArg any
	if publicPort > 0 {
		publicPortArg = publicPort
	}
	_, err := d.sql.Exec(
		`INSERT INTO tunnels (id, client_id, name, local_host, local_port, public_port, protocol, enabled, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 1, ?)`,
		t.ID, t.ClientID, t.Name, t.LocalHost, t.LocalPort, publicPortArg, t.Protocol, t.CreatedAt.Unix(),
	)
	if err != nil {
		return Tunnel{}, fmt.Errorf("insert tunnel: %w", err)
	}
	return t, nil
}

const tunnelColumns = `id, client_id, name, local_host, local_port, public_port, protocol, enabled,
	traffic_limit_bytes, public_hostname, proxy_protocol, bytes_in_total, bytes_out_total, created_at`

func (d *DB) ListTunnelsByClient(clientID string) ([]Tunnel, error) {
	return d.queryTunnels(`SELECT `+tunnelColumns+` FROM tunnels WHERE client_id = ? ORDER BY created_at`, clientID)
}

func (d *DB) ListAllTunnels() ([]Tunnel, error) {
	return d.queryTunnels(`SELECT ` + tunnelColumns + ` FROM tunnels ORDER BY created_at`)
}

func (d *DB) ListEnabledTunnels() ([]Tunnel, error) {
	return d.queryTunnels(`SELECT ` + tunnelColumns + ` FROM tunnels WHERE enabled = 1 ORDER BY created_at`)
}

func (d *DB) GetTunnelByID(id string) (Tunnel, error) {
	tunnels, err := d.queryTunnels(`SELECT `+tunnelColumns+` FROM tunnels WHERE id = ?`, id)
	if err != nil {
		return Tunnel{}, err
	}
	if len(tunnels) == 0 {
		return Tunnel{}, sql.ErrNoRows
	}
	return tunnels[0], nil
}

// GetTunnelByHostname resolves an enabled tunnel by its public_hostname,
// for the Minecraft router to look up where to forward a connection based
// on the hostname the client handshaked with — independent of whether that
// tunnel also has its own dedicated public_port.
func (d *DB) GetTunnelByHostname(hostname string) (Tunnel, error) {
	tunnels, err := d.queryTunnels(`SELECT `+tunnelColumns+` FROM tunnels WHERE public_hostname = ? AND enabled = 1`, hostname)
	if err != nil {
		return Tunnel{}, err
	}
	if len(tunnels) == 0 {
		return Tunnel{}, sql.ErrNoRows
	}
	return tunnels[0], nil
}

// HostnameTaken reports whether hostname is already set on some other
// tunnel (any tunnel, not just enabled ones — a disabled tunnel holding a
// hostname would otherwise let a duplicate through only to collide the
// moment it's re-enabled).
func (d *DB) HostnameTaken(hostname, excludeTunnelID string) (bool, error) {
	var count int
	err := d.sql.QueryRow(
		`SELECT COUNT(*) FROM tunnels WHERE public_hostname = ? AND id != ?`,
		hostname, excludeTunnelID,
	).Scan(&count)
	return count > 0, err
}

func (d *DB) queryTunnels(query string, args ...any) ([]Tunnel, error) {
	rows, err := d.sql.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Tunnel
	for rows.Next() {
		var t Tunnel
		var createdAt int64
		var limit, publicPort sql.NullInt64
		var enabled, proxyProtocol int
		if err := rows.Scan(&t.ID, &t.ClientID, &t.Name, &t.LocalHost, &t.LocalPort, &publicPort,
			&t.Protocol, &enabled, &limit, &t.PublicHostname, &proxyProtocol, &t.BytesInTotal, &t.BytesOutTotal, &createdAt); err != nil {
			return nil, err
		}
		if publicPort.Valid {
			t.PublicPort = int(publicPort.Int64)
		}
		t.Enabled = enabled == 1
		t.ProxyProtocol = proxyProtocol == 1
		if limit.Valid {
			v := limit.Int64
			t.TrafficLimitBytes = &v
		}
		t.CreatedAt = time.Unix(createdAt, 0)
		out = append(out, t)
	}
	return out, rows.Err()
}

func (d *DB) DeleteTunnel(id string) error {
	_, err := d.sql.Exec(`DELETE FROM tunnels WHERE id = ?`, id)
	return err
}

func (d *DB) SetTunnelEnabled(id string, enabled bool) error {
	_, err := d.sql.Exec(`UPDATE tunnels SET enabled = ? WHERE id = ?`, boolToInt(enabled), id)
	return err
}

// UpdateTunnelSettings sets a tunnel's traffic limit (nil = unlimited),
// public hostname (informational only — Portly doesn't manage DNS, this
// just lets the panel show the DNS records to create for it), and whether
// to prefix the local connection with a PROXY protocol v1 header carrying
// the real public client's address.
func (d *DB) UpdateTunnelSettings(id string, trafficLimitBytes *int64, publicHostname string, proxyProtocol bool) error {
	res, err := d.sql.Exec(
		`UPDATE tunnels SET traffic_limit_bytes = ?, public_hostname = ?, proxy_protocol = ? WHERE id = ?`,
		trafficLimitBytes, publicHostname, boolToInt(proxyProtocol), id,
	)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// AddTunnelTraffic accumulates byte counters for a tunnel and records a
// point-in-time sample for historical graphing. If this brings the tunnel's
// own total, or its owning client's combined total across all its tunnels,
// to its configured traffic limit, the affected tunnel(s) are disabled in
// the same transaction — reconcileListeners picks up the change and tears
// down the actual public listener(s) within one reconcile tick.
func (d *DB) AddTunnelTraffic(tunnelID string, bytesIn, bytesOut int64) error {
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(
		`UPDATE tunnels SET bytes_in_total = bytes_in_total + ?, bytes_out_total = bytes_out_total + ? WHERE id = ?`,
		bytesIn, bytesOut, tunnelID,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`INSERT INTO traffic_samples (tunnel_id, ts, bytes_in, bytes_out) VALUES (?, ?, ?, ?)`,
		tunnelID, time.Now().Unix(), bytesIn, bytesOut,
	); err != nil {
		return err
	}

	var newIn, newOut int64
	var tunnelLimit sql.NullInt64
	var clientID string
	if err := tx.QueryRow(
		`SELECT bytes_in_total, bytes_out_total, traffic_limit_bytes, client_id FROM tunnels WHERE id = ?`, tunnelID,
	).Scan(&newIn, &newOut, &tunnelLimit, &clientID); err != nil {
		return err
	}
	if tunnelLimit.Valid && newIn+newOut >= tunnelLimit.Int64 {
		if _, err := tx.Exec(`UPDATE tunnels SET enabled = 0 WHERE id = ?`, tunnelID); err != nil {
			return err
		}
	}

	var clientLimit sql.NullInt64
	if err := tx.QueryRow(`SELECT traffic_limit_bytes FROM clients WHERE id = ?`, clientID).Scan(&clientLimit); err != nil {
		return err
	}
	if clientLimit.Valid {
		var clientTotal int64
		if err := tx.QueryRow(
			`SELECT COALESCE(SUM(bytes_in_total + bytes_out_total), 0) FROM tunnels WHERE client_id = ?`, clientID,
		).Scan(&clientTotal); err != nil {
			return err
		}
		if clientTotal >= clientLimit.Int64 {
			if _, err := tx.Exec(`UPDATE tunnels SET enabled = 0 WHERE client_id = ?`, clientID); err != nil {
				return err
			}
		}
	}

	return tx.Commit()
}

// --- Enrollment codes ---

const enrollCodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // no ambiguous chars

// CreateEnrollmentCode generates a short, human-typeable one-time code for
// the given client that the 'portly-client enroll' command can exchange for
// its real token. Only the code's hash is persisted; the plaintext token is
// held in this row only until exchange (or expiry), then deleted.
func (d *DB) CreateEnrollmentCode(clientID, token string, ttl time.Duration) (string, error) {
	code, err := randomCode(10)
	if err != nil {
		return "", err
	}
	hash := HashToken(code)
	now := time.Now()
	_, err = d.sql.Exec(
		`INSERT INTO enrollment_codes (code_hash, client_id, token, created_at, expires_at) VALUES (?, ?, ?, ?, ?)`,
		hash, clientID, token, now.Unix(), now.Add(ttl).Unix(),
	)
	if err != nil {
		return "", fmt.Errorf("insert enrollment code: %w", err)
	}
	return code, nil
}

// ExchangeEnrollmentCode atomically validates and consumes a one-time
// enrollment code, returning the client it was issued for and its plaintext
// token. Returns an error if the code is unknown, expired, or already used
// (the row is deleted on first successful exchange, making reuse impossible).
func (d *DB) ExchangeEnrollmentCode(code string) (Client, string, error) {
	hash := HashToken(code)
	tx, err := d.sql.Begin()
	if err != nil {
		return Client{}, "", err
	}
	defer tx.Rollback()

	var clientID, token string
	var expiresAt int64
	err = tx.QueryRow(
		`SELECT client_id, token, expires_at FROM enrollment_codes WHERE code_hash = ?`, hash,
	).Scan(&clientID, &token, &expiresAt)
	if err != nil {
		return Client{}, "", fmt.Errorf("invalid or already-used enrollment code")
	}
	if time.Now().Unix() > expiresAt {
		return Client{}, "", fmt.Errorf("enrollment code expired")
	}

	if _, err := tx.Exec(`DELETE FROM enrollment_codes WHERE code_hash = ?`, hash); err != nil {
		return Client{}, "", err
	}
	if err := tx.Commit(); err != nil {
		return Client{}, "", err
	}

	client, err := d.GetClientByID(clientID)
	return client, token, err
}

func randomCode(n int) (string, error) {
	b := make([]byte, n)
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	for i, v := range raw {
		b[i] = enrollCodeAlphabet[int(v)%len(enrollCodeAlphabet)]
	}
	return string(b), nil
}

// --- API keys ---

type ApiKey struct {
	ID        string
	Name      string
	TokenHash string
	CreatedAt time.Time
	LastUsed  *time.Time
}

// GenerateAPIKey mirrors GenerateToken's shape (random 32 bytes, SHA-256
// hashed at rest) but with a distinct "ptly_api_" prefix, so an API key
// meant for an external service calling in is visually distinguishable
// from a client token meant for a portly-client install.
func GenerateAPIKey() (key, hash string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	key = "ptly_api_" + hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(key))
	hash = hex.EncodeToString(sum[:])
	return key, hash, nil
}

func (d *DB) CreateAPIKey(name string) (ApiKey, string, error) {
	key, hash, err := GenerateAPIKey()
	if err != nil {
		return ApiKey{}, "", err
	}
	k := ApiKey{
		ID:        uuid.NewString(),
		Name:      name,
		TokenHash: hash,
		CreatedAt: time.Now(),
	}
	_, err = d.sql.Exec(
		`INSERT INTO api_keys (id, name, token_hash, created_at) VALUES (?, ?, ?, ?)`,
		k.ID, k.Name, k.TokenHash, k.CreatedAt.Unix(),
	)
	if err != nil {
		return ApiKey{}, "", fmt.Errorf("insert api key: %w", err)
	}
	return k, key, nil
}

const apiKeyColumns = `id, name, token_hash, created_at, last_used`

func (d *DB) ListAPIKeys() ([]ApiKey, error) {
	rows, err := d.sql.Query(`SELECT ` + apiKeyColumns + ` FROM api_keys ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ApiKey
	for rows.Next() {
		var k ApiKey
		var createdAt int64
		var lastUsed sql.NullInt64
		if err := rows.Scan(&k.ID, &k.Name, &k.TokenHash, &createdAt, &lastUsed); err != nil {
			return nil, err
		}
		k.CreatedAt = time.Unix(createdAt, 0)
		if lastUsed.Valid {
			t := time.Unix(lastUsed.Int64, 0)
			k.LastUsed = &t
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// GetAPIKeyByHash is on the request hot path for every API-key-authenticated
// call, same as GetClientByTokenHash is for client connections.
func (d *DB) GetAPIKeyByHash(hash string) (ApiKey, error) {
	row := d.sql.QueryRow(`SELECT `+apiKeyColumns+` FROM api_keys WHERE token_hash = ?`, hash)
	var k ApiKey
	var createdAt int64
	var lastUsed sql.NullInt64
	if err := row.Scan(&k.ID, &k.Name, &k.TokenHash, &createdAt, &lastUsed); err != nil {
		return ApiKey{}, err
	}
	k.CreatedAt = time.Unix(createdAt, 0)
	if lastUsed.Valid {
		t := time.Unix(lastUsed.Int64, 0)
		k.LastUsed = &t
	}
	return k, nil
}

func (d *DB) UpdateAPIKeyLastUsed(id string) error {
	_, err := d.sql.Exec(`UPDATE api_keys SET last_used = ? WHERE id = ?`, time.Now().Unix(), id)
	return err
}

func (d *DB) DeleteAPIKey(id string) error {
	_, err := d.sql.Exec(`DELETE FROM api_keys WHERE id = ?`, id)
	return err
}

// --- Server settings (key-value) ---

func (d *DB) GetSetting(key string) (value string, ok bool, err error) {
	err = d.sql.QueryRow(`SELECT value FROM server_settings WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

func (d *DB) SetSetting(key, value string) error {
	_, err := d.sql.Exec(
		`INSERT INTO server_settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value,
	)
	return err
}

func (d *DB) DeleteSetting(key string) error {
	_, err := d.sql.Exec(`DELETE FROM server_settings WHERE key = ?`, key)
	return err
}

// --- Traffic samples ---

type TrafficSample struct {
	TS       int64 `json:"ts"`
	BytesIn  int64 `json:"bytes_in"`
	BytesOut int64 `json:"bytes_out"`
}

func (d *DB) ListTrafficSamples(tunnelID string, since int64) ([]TrafficSample, error) {
	rows, err := d.sql.Query(
		`SELECT ts, bytes_in, bytes_out FROM traffic_samples WHERE tunnel_id = ? AND ts >= ? ORDER BY ts`,
		tunnelID, since,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []TrafficSample
	for rows.Next() {
		var s TrafficSample
		if err := rows.Scan(&s.TS, &s.BytesIn, &s.BytesOut); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
