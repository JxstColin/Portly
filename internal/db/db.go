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
	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

const defaultAdminUsername = "admin"
const defaultAdminPassword = "portly"

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

	d := &DB{sql: sqlDB}
	if err := d.ensureAdminUser(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("seed admin user: %w", err)
	}
	return d, nil
}

func (d *DB) Close() error {
	return d.sql.Close()
}

func (d *DB) ensureAdminUser() error {
	var count int
	if err := d.sql.QueryRow(`SELECT COUNT(*) FROM admin_users`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(defaultAdminPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = d.sql.Exec(
		`INSERT INTO admin_users (id, username, password_hash, must_change_password, created_at) VALUES (?, ?, ?, 1, ?)`,
		uuid.NewString(), defaultAdminUsername, string(hash), time.Now().Unix(),
	)
	return err
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

func (d *DB) DeleteClient(id string) error {
	_, err := d.sql.Exec(`DELETE FROM clients WHERE id = ?`, id)
	return err
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

func (d *DB) CreateTunnel(clientID, name, localHost string, localPort, publicPort int) (Tunnel, error) {
	t := Tunnel{
		ID:         uuid.NewString(),
		ClientID:   clientID,
		Name:       name,
		LocalHost:  localHost,
		LocalPort:  localPort,
		PublicPort: publicPort,
		Protocol:   "tcp",
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

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
