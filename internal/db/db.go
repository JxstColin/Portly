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

	return &DB{sql: sqlDB}, nil
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
	ID        string
	Name      string
	TokenHash string
	CreatedAt time.Time
	LastSeen  *time.Time
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

func (d *DB) GetClientByTokenHash(hash string) (Client, error) {
	return d.scanClient(d.sql.QueryRow(
		`SELECT id, name, token_hash, created_at, last_seen FROM clients WHERE token_hash = ?`, hash,
	))
}

func (d *DB) GetClientByID(id string) (Client, error) {
	return d.scanClient(d.sql.QueryRow(
		`SELECT id, name, token_hash, created_at, last_seen FROM clients WHERE id = ?`, id,
	))
}

func (d *DB) scanClient(row *sql.Row) (Client, error) {
	var c Client
	var createdAt int64
	var lastSeen sql.NullInt64
	if err := row.Scan(&c.ID, &c.Name, &c.TokenHash, &createdAt, &lastSeen); err != nil {
		return Client{}, err
	}
	c.CreatedAt = time.Unix(createdAt, 0)
	if lastSeen.Valid {
		t := time.Unix(lastSeen.Int64, 0)
		c.LastSeen = &t
	}
	return c, nil
}

func (d *DB) ListClients() ([]Client, error) {
	rows, err := d.sql.Query(`SELECT id, name, token_hash, created_at, last_seen FROM clients ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Client
	for rows.Next() {
		var c Client
		var createdAt int64
		var lastSeen sql.NullInt64
		if err := rows.Scan(&c.ID, &c.Name, &c.TokenHash, &createdAt, &lastSeen); err != nil {
			return nil, err
		}
		c.CreatedAt = time.Unix(createdAt, 0)
		if lastSeen.Valid {
			t := time.Unix(lastSeen.Int64, 0)
			c.LastSeen = &t
		}
		out = append(out, c)
	}
	return out, rows.Err()
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
	ID                string
	ClientID          string
	Name              string
	LocalHost         string
	LocalPort         int
	PublicPort        int
	Protocol          string
	Enabled           bool
	TrafficLimitBytes *int64
	BytesInTotal      int64
	BytesOutTotal     int64
	CreatedAt         time.Time
}

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
	_, err := d.sql.Exec(
		`INSERT INTO tunnels (id, client_id, name, local_host, local_port, public_port, protocol, enabled, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 1, ?)`,
		t.ID, t.ClientID, t.Name, t.LocalHost, t.LocalPort, t.PublicPort, t.Protocol, t.CreatedAt.Unix(),
	)
	if err != nil {
		return Tunnel{}, fmt.Errorf("insert tunnel: %w", err)
	}
	return t, nil
}

func (d *DB) ListTunnelsByClient(clientID string) ([]Tunnel, error) {
	return d.queryTunnels(`SELECT id, client_id, name, local_host, local_port, public_port, protocol, enabled,
		traffic_limit_bytes, bytes_in_total, bytes_out_total, created_at FROM tunnels WHERE client_id = ? ORDER BY created_at`, clientID)
}

func (d *DB) ListAllTunnels() ([]Tunnel, error) {
	return d.queryTunnels(`SELECT id, client_id, name, local_host, local_port, public_port, protocol, enabled,
		traffic_limit_bytes, bytes_in_total, bytes_out_total, created_at FROM tunnels ORDER BY created_at`)
}

func (d *DB) ListEnabledTunnels() ([]Tunnel, error) {
	return d.queryTunnels(`SELECT id, client_id, name, local_host, local_port, public_port, protocol, enabled,
		traffic_limit_bytes, bytes_in_total, bytes_out_total, created_at FROM tunnels WHERE enabled = 1 ORDER BY created_at`)
}

func (d *DB) GetTunnelByID(id string) (Tunnel, error) {
	tunnels, err := d.queryTunnels(`SELECT id, client_id, name, local_host, local_port, public_port, protocol, enabled,
		traffic_limit_bytes, bytes_in_total, bytes_out_total, created_at FROM tunnels WHERE id = ?`, id)
	if err != nil {
		return Tunnel{}, err
	}
	if len(tunnels) == 0 {
		return Tunnel{}, sql.ErrNoRows
	}
	return tunnels[0], nil
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
		var limit sql.NullInt64
		var enabled int
		if err := rows.Scan(&t.ID, &t.ClientID, &t.Name, &t.LocalHost, &t.LocalPort, &t.PublicPort,
			&t.Protocol, &enabled, &limit, &t.BytesInTotal, &t.BytesOutTotal, &createdAt); err != nil {
			return nil, err
		}
		t.Enabled = enabled == 1
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

// AddTunnelTraffic accumulates byte counters for a tunnel and records a
// point-in-time sample for historical graphing.
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
