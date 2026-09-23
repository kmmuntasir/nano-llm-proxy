package main

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"os"
	"reflect"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

// Store is the SQLite persistence layer — source of truth for users, client
// API keys, providers (and their upstream keys), and sessions. The proxy hot
// path never touches it directly: in-memory pools and the client-key cache are
// rebuilt after CRUD writes instead.

var (
	ErrAdminEnvMissing   = errors.New("ADMIN_EMAIL/ADMIN_PASSWORD required on first boot (set them in the environment or a .env file)")
	ErrLegacyKeysMissing = errors.New("first boot requires keys.json to seed the provider pools")
	ErrBuiltinProvider   = errors.New("builtin providers cannot be deleted")
	ErrNotFound          = errors.New("not found")
)

type Store struct {
	db *sql.DB
}

type User struct {
	ID           int64
	Name         string
	Email        string
	PasswordHash string
	Role         string // "superadmin" | "user"
	Disabled     bool
	CreatedAt    int64
	UpdatedAt    int64
}

type ClientKey struct {
	ID           int64
	UserID       int64
	KeyHash      string // full sha256 hex of the plaintext key
	KeyHint      string // plaintext prefix for GUI display, e.g. "fg-5gHyfBSf"
	Alias        string
	Disabled     bool
	CreatedAt    int64
	LastUsedAt   int64
	RequestCount int64
}

type Provider struct {
	ID        int64
	Name      string // == routing prefix ("zen/model-id")
	Type      string // "openai" | "opencode"
	BaseURL   string
	Enabled   bool
	Builtin   bool
	SortOrder int64
	CreatedAt int64
}

type ProviderKey struct {
	ID         int64
	ProviderID int64
	Key        string // plaintext upstream key; the DB file is 0600
	Label      string
	SortOrder  int64
	Disabled   bool
	CreatedAt  int64
}

type Session struct {
	TokenHash string
	UserID    int64
	CreatedAt int64
	ExpiresAt int64
	LastSeen  int64
}

func OpenStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// One connection serializes writers (kills "database is locked"), and for
	// :memory: test DBs it guarantees every query sees the same database.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL", // expected to fail on :memory: — ignored
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
		"PRAGMA synchronous=NORMAL",
	} {
		db.Exec(pragma) //nolint:errcheck
	}
	if path != ":memory:" {
		os.Chmod(path, 0o600) //nolint:errcheck
	}
	s := &Store{db: db}
	if err := s.Migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Migrate() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL)`)
	if err != nil {
		return err
	}
	var version sql.NullInt64
	row := s.db.QueryRow(`SELECT MAX(version) FROM schema_migrations`)
	if err := row.Scan(&version); err != nil {
		return err
	}
	if !version.Valid || version.Int64 < 1 {
		if err := s.migrateV1(); err != nil {
			return err
		}
	}
	if !version.Valid || version.Int64 < 2 {
		if err := s.migrateV2(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) migrateV1() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmts := []string{
		`CREATE TABLE users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			email TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL CHECK (role IN ('superadmin','user')),
			disabled INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL)`,
		`CREATE TABLE api_keys (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			key_hash TEXT NOT NULL UNIQUE,
			key_hint TEXT NOT NULL DEFAULT '',
			alias TEXT NOT NULL DEFAULT '',
			disabled INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			last_used_at INTEGER NOT NULL DEFAULT 0,
			request_count INTEGER NOT NULL DEFAULT 0)`,
		`CREATE INDEX idx_api_keys_user ON api_keys(user_id)`,
		`CREATE TABLE providers (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			type TEXT NOT NULL CHECK (type IN ('openai','opencode')),
			base_url TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			builtin INTEGER NOT NULL DEFAULT 0,
			sort_order INTEGER NOT NULL DEFAULT 100,
			created_at INTEGER NOT NULL)`,
		`CREATE TABLE provider_keys (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			provider_id INTEGER NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
			key TEXT NOT NULL,
			label TEXT NOT NULL DEFAULT '',
			sort_order INTEGER NOT NULL DEFAULT 0,
			disabled INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL)`,
		`CREATE INDEX idx_provider_keys_provider ON provider_keys(provider_id)`,
		`CREATE TABLE sessions (
			token_hash TEXT PRIMARY KEY,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			created_at INTEGER NOT NULL,
			expires_at INTEGER NOT NULL,
			last_seen_at INTEGER NOT NULL)`,
		`CREATE INDEX idx_sessions_expiry ON sessions(expires_at)`,
		`CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
	}
	for _, q := range stmts {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	_, err = tx.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (1, ?)`, time.Now().Unix())
	if err != nil {
		return err
	}
	return tx.Commit()
}

// migrateV2 adds the persisted usage log (per-request token accounting for
// the Usage page and dashboard metrics).
func (s *Store) migrateV2() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmts := []string{
		`CREATE TABLE usage_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			ts INTEGER NOT NULL,
			user_id INTEGER NOT NULL,
			client_key_id INTEGER NOT NULL,
			provider TEXT NOT NULL,
			model TEXT NOT NULL,
			input_tokens INTEGER NOT NULL DEFAULT 0,
			output_tokens INTEGER NOT NULL DEFAULT 0,
			status INTEGER NOT NULL,
			duration_ms INTEGER NOT NULL)`,
		`CREATE INDEX idx_usage_ts ON usage_events(ts)`,
		`CREATE INDEX idx_usage_user_ts ON usage_events(user_id, ts)`,
		`CREATE INDEX idx_usage_key_ts ON usage_events(client_key_id, ts)`,
	}
	for _, q := range stmts {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (2, ?)`, time.Now().Unix()); err != nil {
		return err
	}
	return tx.Commit()
}

// Bootstrap seeds a fresh database: superadmin from env, and the two builtin
// providers with keys imported from keys.json. Later boots with a populated
// DB are no-ops. Ordered so a mid-tx crash leaves nothing partial.
func (s *Store) Bootstrap(cfg *Config, kf *keyFile, adminEmail, adminPassword string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().Unix()

	// 1. superadmin (create-if-absent; env never overwrites an existing user)
	var nUsers int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&nUsers); err != nil {
		return err
	}
	var adminID int64
	if nUsers == 0 {
		if adminEmail == "" || adminPassword == "" {
			return ErrAdminEnvMissing
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(adminPassword), 10)
		if err != nil {
			return err
		}
		res, err := tx.Exec(`INSERT INTO users (name, email, password_hash, role, disabled, created_at, updated_at)
			VALUES ('admin', ?, ?, 'superadmin', 0, ?, ?)`, adminEmail, string(hash), now, now)
		if err != nil {
			return err
		}
		adminID, err = res.LastInsertId()
		if err != nil {
			return err
		}
		log.Printf("store: seeded superadmin %s", adminEmail)
	} else {
		if err := tx.QueryRow(`SELECT id FROM users WHERE role='superadmin' ORDER BY id LIMIT 1`).Scan(&adminID); err != nil {
			return fmt.Errorf("users exist but no superadmin: %w", err)
		}
	}

	// 2. builtin providers + keys.json import
	var nProviders int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM providers`).Scan(&nProviders); err != nil {
		return err
	}
	if nProviders == 0 {
		if kf == nil {
			return ErrLegacyKeysMissing
		}
		res, err := tx.Exec(`INSERT INTO providers (name, type, base_url, enabled, builtin, sort_order, created_at)
			VALUES ('zen', 'opencode', ?, 1, 1, 0, ?)`, cfg.Zen.BaseURL, now)
		if err != nil {
			return err
		}
		zenID, _ := res.LastInsertId()
		res, err = tx.Exec(`INSERT INTO providers (name, type, base_url, enabled, builtin, sort_order, created_at)
			VALUES ('kilo', 'openai', ?, 1, 1, 1, ?)`, cfg.Kilo.BaseURL, now)
		if err != nil {
			return err
		}
		kiloID, _ := res.LastInsertId()
		for i, e := range kf.Zen {
			if _, err := tx.Exec(`INSERT INTO provider_keys (provider_id, key, label, sort_order, disabled, created_at)
				VALUES (?, ?, ?, ?, 0, ?)`, zenID, e.Key, e.Label, i, now); err != nil {
				return err
			}
		}
		for i, e := range kf.Kilo {
			if _, err := tx.Exec(`INSERT INTO provider_keys (provider_id, key, label, sort_order, disabled, created_at)
				VALUES (?, ?, ?, ?, 0, ?)`, kiloID, e.Key, e.Label, i, now); err != nil {
				return err
			}
		}
		log.Printf("store: seeded providers zen=%d keys kilo=%d keys", len(kf.Zen), len(kf.Kilo))
	}

	return tx.Commit()
}

func (s *Store) getSettingTx(tx *sql.Tx, key string) (string, error) {
	var v string
	err := tx.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

func (s *Store) getSetting(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// --- runtime settings ---

const runtimeSettingsKey = "runtime_settings"

// loadSettingsDoc reads and parses the stored document; "" comes back as
// defaults. Fields absent from stored JSON keep their defaults because the
// unmarshal targets a defaulted value.
func (s *Store) loadSettingsDoc(raw string) (*RuntimeSettings, error) {
	rs := DefaultRuntimeSettings()
	if raw == "" {
		return rs, nil
	}
	if err := json.Unmarshal([]byte(raw), rs); err != nil {
		return nil, fmt.Errorf("parse %s: %w", runtimeSettingsKey, err)
	}
	return rs.applyDefaults(), nil
}

// LoadRuntimeSettings returns the effective runtime settings, applying
// defaults for anything the stored document (or the row itself) leaves out.
func (s *Store) LoadRuntimeSettings() (*RuntimeSettings, error) {
	raw, err := s.getSetting(runtimeSettingsKey)
	if err != nil {
		return nil, err
	}
	return s.loadSettingsDoc(raw)
}

// SaveRuntimeSettings replaces the stored document wholesale.
func (s *Store) SaveRuntimeSettings(rs *RuntimeSettings) error {
	raw, err := json.Marshal(rs)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, runtimeSettingsKey, string(raw))
	return err
}

// UpdateSettings runs read-modify-write against the stored document inside
// one transaction. SetMaxOpenConns(1) serializes individual statements, not
// pairs — without the tx a background sync could clobber an admin PUT that
// lands between its read and write.
func (s *Store) UpdateSettings(mutate func(*RuntimeSettings) *RuntimeSettings) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	raw, err := s.getSettingTx(tx, runtimeSettingsKey)
	if err != nil {
		return err
	}
	rs, err := s.loadSettingsDoc(raw)
	if err != nil {
		return err
	}
	updated := mutate(rs)
	if updated == nil {
		updated = rs
	}
	blob, err := json.Marshal(updated)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, runtimeSettingsKey, string(blob)); err != nil {
		return err
	}
	return tx.Commit()
}

// --- users ---

func (s *Store) InsertUser(u *User) error {
	now := time.Now().Unix()
	res, err := s.db.Exec(`INSERT INTO users (name, email, password_hash, role, disabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, u.Name, u.Email, u.PasswordHash, u.Role, boolInt(u.Disabled), now, now)
	if err != nil {
		return err
	}
	u.ID, _ = res.LastInsertId()
	u.CreatedAt, u.UpdatedAt = now, now
	return nil
}

const userCols = `id, name, email, password_hash, role, disabled, created_at, updated_at`

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	u := &User{}
	var disabled int
	if err := row.Scan(&u.ID, &u.Name, &u.Email, &u.PasswordHash, &u.Role, &disabled, &u.CreatedAt, &u.UpdatedAt); err != nil {
		return nil, err
	}
	u.Disabled = disabled != 0
	return u, nil
}

func (s *Store) UserByEmail(email string) (*User, error) {
	u, err := scanUser(s.db.QueryRow(`SELECT `+userCols+` FROM users WHERE email=?`, email))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return u, err
}

func (s *Store) User(id int64) (*User, error) {
	u, err := scanUser(s.db.QueryRow(`SELECT `+userCols+` FROM users WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return u, err
}

type UserWithKeyCount struct {
	User
	KeyCount int64
}

func (s *Store) ListUsers() ([]UserWithKeyCount, error) {
	rows, err := s.db.Query(`SELECT ` + userCols + `, (SELECT COUNT(*) FROM api_keys k WHERE k.user_id=users.id) AS key_count
		FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UserWithKeyCount
	for rows.Next() {
		u := &User{}
		var disabled, kc int64
		if err := rows.Scan(&u.ID, &u.Name, &u.Email, &u.PasswordHash, &u.Role, &disabled, &u.CreatedAt, &u.UpdatedAt, &kc); err != nil {
			return nil, err
		}
		u.Disabled = disabled != 0
		out = append(out, UserWithKeyCount{User: *u, KeyCount: kc})
	}
	return out, rows.Err()
}

// UpdateUser patches only the non-nil fields; passwordHash should arrive
// pre-hashed (or empty to leave unchanged).
func (s *Store) UpdateUser(id int64, name, email, passwordHash, role *string, disabled *bool) error {
	sets := []string{"updated_at=?"}
	args := []any{time.Now().Unix()}
	if name != nil {
		sets, args = append(sets, "name=?"), append(args, *name)
	}
	if email != nil {
		sets, args = append(sets, "email=?"), append(args, *email)
	}
	if passwordHash != nil {
		sets, args = append(sets, "password_hash=?"), append(args, *passwordHash)
	}
	if role != nil {
		sets, args = append(sets, "role=?"), append(args, *role)
	}
	if disabled != nil {
		sets, args = append(sets, "disabled=?"), append(args, boolInt(*disabled))
	}
	args = append(args, id)
	_, err := s.db.Exec(`UPDATE users SET `+strings.Join(sets, ", ")+` WHERE id=?`, args...)
	return err
}

func (s *Store) DeleteUser(id int64) error {
	res, err := s.db.Exec(`DELETE FROM users WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) CountSuperadmins() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM users WHERE role='superadmin' AND disabled=0`).Scan(&n)
	return n, err
}

// --- client API keys ---

// CreateClientKey generates the plaintext (returned exactly once) and stores
// only its sha256.
func (s *Store) CreateClientKey(userID int64, alias string) (string, ClientKey, error) {
	plaintext := "fg-" + newSecret(32)
	now := time.Now().Unix()
	res, err := s.db.Exec(`INSERT INTO api_keys (user_id, key_hash, key_hint, alias, disabled, created_at)
		VALUES (?, ?, ?, ?, 0, ?)`, userID, hashSecret(plaintext), keyHint(plaintext), alias, now)
	if err != nil {
		return "", ClientKey{}, err
	}
	id, _ := res.LastInsertId()
	return plaintext, ClientKey{
		ID: id, UserID: userID, KeyHash: hashSecret(plaintext),
		KeyHint: keyHint(plaintext), Alias: alias, CreatedAt: now,
	}, nil
}

const clientKeyCols = `id, user_id, key_hash, key_hint, alias, disabled, created_at, last_used_at, request_count`

func scanClientKey(row interface{ Scan(...any) error }) (*ClientKey, error) {
	k := &ClientKey{}
	var disabled int
	if err := row.Scan(&k.ID, &k.UserID, &k.KeyHash, &k.KeyHint, &k.Alias, &disabled, &k.CreatedAt, &k.LastUsedAt, &k.RequestCount); err != nil {
		return nil, err
	}
	k.Disabled = disabled != 0
	return k, nil
}

func (s *Store) ListClientKeys(userID int64) ([]ClientKey, error) {
	rows, err := s.db.Query(`SELECT `+clientKeyCols+` FROM api_keys WHERE user_id=? ORDER BY id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ClientKey
	for rows.Next() {
		k, err := scanClientKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *k)
	}
	return out, rows.Err()
}

// ClientKeyWithEmail is a client key plus its owner's email (activity ring).
type ClientKeyWithEmail struct {
	ClientKey
	UserEmail string
}

// AllClientKeys feeds the hot-path clientKeyCache reload (joins owner email).
func (s *Store) AllClientKeys() ([]ClientKeyWithEmail, error) {
	rows, err := s.db.Query(`SELECT k.id, k.user_id, k.key_hash, k.key_hint, k.alias, k.disabled,
		k.created_at, k.last_used_at, k.request_count, u.email FROM api_keys k
		JOIN users u ON u.id = k.user_id ORDER BY k.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ClientKeyWithEmail
	for rows.Next() {
		var k ClientKeyWithEmail
		var disabled int
		if err := rows.Scan(&k.ID, &k.UserID, &k.KeyHash, &k.KeyHint, &k.Alias, &disabled, &k.CreatedAt, &k.LastUsedAt, &k.RequestCount, &k.UserEmail); err != nil {
			return nil, err
		}
		k.Disabled = disabled != 0
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s *Store) ClientKey(id int64) (*ClientKey, error) {
	k, err := scanClientKey(s.db.QueryRow(`SELECT `+clientKeyCols+` FROM api_keys WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return k, err
}

func (s *Store) SetClientKeyDisabled(id int64, disabled bool) error {
	res, err := s.db.Exec(`UPDATE api_keys SET disabled=? WHERE id=?`, boolInt(disabled), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) UpdateClientKeyAlias(id int64, alias string) error {
	res, err := s.db.Exec(`UPDATE api_keys SET alias=? WHERE id=?`, alias, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteClientKey(id int64) error {
	res, err := s.db.Exec(`DELETE FROM api_keys WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// BumpClientKeyUsage applies a flushed batch of in-memory counter deltas.
func (s *Store) BumpClientKeyUsage(id int64, delta int64, lastUsed int64) error {
	_, err := s.db.Exec(`UPDATE api_keys
		SET request_count = request_count + ?,
		    last_used_at = CASE WHEN ? > last_used_at THEN ? ELSE last_used_at END
		WHERE id=?`, delta, lastUsed, lastUsed, id)
	return err
}

// --- providers ---

const providerCols = `id, name, type, base_url, enabled, builtin, sort_order, created_at`

func scanProvider(row interface{ Scan(...any) error }) (*Provider, error) {
	p := &Provider{}
	var enabled, builtin int
	if err := row.Scan(&p.ID, &p.Name, &p.Type, &p.BaseURL, &enabled, &builtin, &p.SortOrder, &p.CreatedAt); err != nil {
		return nil, err
	}
	p.Enabled, p.Builtin = enabled != 0, builtin != 0
	return p, nil
}

func (s *Store) ListProviders() ([]Provider, error) {
	rows, err := s.db.Query(`SELECT ` + providerCols + ` FROM providers ORDER BY sort_order, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Provider
	for rows.Next() {
		p, err := scanProvider(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

func (s *Store) Provider(id int64) (*Provider, error) {
	p, err := scanProvider(s.db.QueryRow(`SELECT `+providerCols+` FROM providers WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return p, err
}

func (s *Store) ListProviderKeys(providerID int64) ([]ProviderKey, error) {
	rows, err := s.db.Query(`SELECT id, provider_id, key, label, sort_order, disabled, created_at
		FROM provider_keys WHERE provider_id=? ORDER BY sort_order, id`, providerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProviderKey
	for rows.Next() {
		var k ProviderKey
		var disabled int
		if err := rows.Scan(&k.ID, &k.ProviderID, &k.Key, &k.Label, &k.SortOrder, &disabled, &k.CreatedAt); err != nil {
			return nil, err
		}
		k.Disabled = disabled != 0
		out = append(out, k)
	}
	return out, rows.Err()
}

// ListProvidersWithKeys is the rebuildPools feed: every provider with its keys
// in one pass, in priority (sort_order) order.
func (s *Store) ListProvidersWithKeys() ([]Provider, [][]ProviderKey, error) {
	provs, err := s.ListProviders()
	if err != nil {
		return nil, nil, err
	}
	all, err := func() ([]ProviderKey, error) {
		rows, err := s.db.Query(`SELECT id, provider_id, key, label, sort_order, disabled, created_at
			FROM provider_keys ORDER BY sort_order, id`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []ProviderKey
		for rows.Next() {
			var k ProviderKey
			var disabled int
			if err := rows.Scan(&k.ID, &k.ProviderID, &k.Key, &k.Label, &k.SortOrder, &disabled, &k.CreatedAt); err != nil {
				return nil, err
			}
			k.Disabled = disabled != 0
			out = append(out, k)
		}
		return out, rows.Err()
	}()
	if err != nil {
		return nil, nil, err
	}
	byProvider := map[int64][]ProviderKey{}
	for _, k := range all {
		byProvider[k.ProviderID] = append(byProvider[k.ProviderID], k)
	}
	keyLists := make([][]ProviderKey, len(provs))
	for i, p := range provs {
		keyLists[i] = byProvider[p.ID]
	}
	return provs, keyLists, nil
}

func (s *Store) CreateProvider(p *Provider, keys []ProviderKey) error {
	now := time.Now().Unix()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT INTO providers (name, type, base_url, enabled, builtin, sort_order, created_at)
		VALUES (?, ?, ?, ?, 0, ?, ?)`, p.Name, p.Type, p.BaseURL, boolInt(p.Enabled), p.SortOrder, now)
	if err != nil {
		return err
	}
	p.ID, _ = res.LastInsertId()
	p.CreatedAt = now
	for i, k := range keys {
		if _, err := tx.Exec(`INSERT INTO provider_keys (provider_id, key, label, sort_order, disabled, created_at)
			VALUES (?, ?, ?, ?, 0, ?)`, p.ID, k.Key, k.Label, i, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UpdateProvider patches only non-nil fields; callers enforce builtin rules.
func (s *Store) UpdateProvider(id int64, name, baseURL *string, enabled *bool) error {
	var sets []string
	args := []any{}
	if name != nil {
		sets, args = append(sets, "name=?"), append(args, *name)
	}
	if baseURL != nil {
		sets, args = append(sets, "base_url=?"), append(args, *baseURL)
	}
	if enabled != nil {
		sets, args = append(sets, "enabled=?"), append(args, boolInt(*enabled))
	}
	if len(sets) == 0 {
		return nil
	}
	args = append(args, id)
	res, err := s.db.Exec(`UPDATE providers SET `+strings.Join(sets, ", ")+` WHERE id=?`, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteProvider(id int64) error {
	p, err := s.Provider(id)
	if err != nil {
		return err
	}
	if p == nil {
		return ErrNotFound
	}
	if p.Builtin {
		return ErrBuiltinProvider
	}
	_, err = s.db.Exec(`DELETE FROM providers WHERE id=?`, id) // keys cascade
	return err
}

func (s *Store) AddProviderKey(providerID int64, label, key string) (ProviderKey, error) {
	now := time.Now().Unix()
	var max int64
	s.db.QueryRow(`SELECT COALESCE(MAX(sort_order), -1) FROM provider_keys WHERE provider_id=?`, providerID).Scan(&max)
	res, err := s.db.Exec(`INSERT INTO provider_keys (provider_id, key, label, sort_order, disabled, created_at)
		VALUES (?, ?, ?, ?, 0, ?)`, providerID, key, label, max+1, now)
	if err != nil {
		return ProviderKey{}, err
	}
	id, _ := res.LastInsertId()
	return ProviderKey{ID: id, ProviderID: providerID, Key: key, Label: label, SortOrder: max + 1, CreatedAt: now}, nil
}

func (s *Store) UpdateProviderKey(id int64, label *string, disabled *bool) error {
	var sets []string
	args := []any{}
	if label != nil {
		sets, args = append(sets, "label=?"), append(args, *label)
	}
	if disabled != nil {
		sets, args = append(sets, "disabled=?"), append(args, boolInt(*disabled))
	}
	if len(sets) == 0 {
		return nil
	}
	args = append(args, id)
	res, err := s.db.Exec(`UPDATE provider_keys SET `+strings.Join(sets, ", ")+` WHERE id=?`, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteProviderKey(id int64) error {
	res, err := s.db.Exec(`DELETE FROM provider_keys WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// --- sessions ---

// CreateSession mints a raw token for the cookie (returned) and stores only
// its sha256. 32 bytes of base62 ≈ 190 bits — no timing-safe lookup needed,
// but we use one anyway since the comparison is over fixed-size hashes.
func (s *Store) CreateSession(userID int64, ttl time.Duration) (string, time.Time, error) {
	raw := newSecret(43)
	now := time.Now()
	exp := now.Add(ttl)
	_, err := s.db.Exec(`INSERT INTO sessions (token_hash, user_id, created_at, expires_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?)`, hashSecret(raw), userID, now.Unix(), exp.Unix(), now.Unix())
	if err != nil {
		return "", time.Time{}, err
	}
	return raw, exp, nil
}

func (s *Store) SessionByHash(hash string) (*Session, *User, error) {
	var sess Session
	var userID int64
	err := s.db.QueryRow(`SELECT token_hash, user_id, created_at, expires_at, last_seen_at
		FROM sessions WHERE token_hash=?`, hash).Scan(&sess.TokenHash, &userID, &sess.CreatedAt, &sess.ExpiresAt, &sess.LastSeen)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if time.Now().Unix() >= sess.ExpiresAt {
		s.db.Exec(`DELETE FROM sessions WHERE token_hash=?`, hash) //nolint:errcheck
		return nil, nil, nil
	}
	u, err := s.User(userID)
	if err != nil || u == nil {
		return nil, nil, err
	}
	if u.Disabled {
		return nil, nil, nil
	}
	return &sess, u, nil
}

// TouchSession slides expiry forward when the session is still active; called
// at most once per 24h per session (throttled by the caller).
func (s *Store) TouchSession(hash string, ttl time.Duration) error {
	now := time.Now().Unix()
	_, err := s.db.Exec(`UPDATE sessions SET last_seen_at=?, expires_at=? WHERE token_hash=?`,
		now, now+int64(ttl.Seconds()), hash)
	return err
}

func (s *Store) DeleteSession(hash string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token_hash=?`, hash)
	return err
}

func (s *Store) DeleteExpiredSessions() error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE expires_at < ?`, time.Now().Unix())
	return err
}

func (s *Store) DeleteUserSessions(userID int64) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE user_id=?`, userID)
	return err
}

// --- usage events ---

// InsertUsageEvents persists one flushed batch in a single transaction.
func (s *Store) InsertUsageEvents(events []usageEvent) error {
	if len(events) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, e := range events {
		if _, err := tx.Exec(`INSERT INTO usage_events
			(ts, user_id, client_key_id, provider, model, input_tokens, output_tokens, status, duration_ms)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			e.TS, e.UserID, e.ClientKeyID, e.Provider, e.Model,
			e.InputTokens, e.OutputTokens, e.Status, e.DurationMs); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// PruneUsageEvents drops rows older than the retention window.
func (s *Store) PruneUsageEvents(before int64) error {
	_, err := s.db.Exec(`DELETE FROM usage_events WHERE ts < ?`, before)
	return err
}

type UsageTotals struct {
	Requests     int64 `json:"requests"`
	Errors       int64 `json:"errors"`
	InputTokens  int64 `json:"inputTokens"`
	OutputTokens int64 `json:"outputTokens"`
}

type UsageModelRow struct {
	Key          string `json:"key"` // model or provider name
	Requests     int64  `json:"requests"`
	InputTokens  int64  `json:"inputTokens"`
	OutputTokens int64  `json:"outputTokens"`
}

type UsageUserRow struct {
	UserID       int64  `json:"userId"`
	Email        string `json:"email"`
	Requests     int64  `json:"requests"`
	InputTokens  int64  `json:"inputTokens"`
	OutputTokens int64  `json:"outputTokens"`
}

type UsageKeyRow struct {
	KeyID        int64  `json:"keyId"`
	Alias        string `json:"alias"`
	UserID       int64  `json:"userId"`
	Email        string `json:"email"`
	Requests     int64  `json:"requests"`
	InputTokens  int64  `json:"inputTokens"`
	OutputTokens int64  `json:"outputTokens"`
}

type UsageActivityRow struct {
	TS           int64  `json:"ts"`
	Email        string `json:"email"`
	Alias        string `json:"alias"`
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	InputTokens  int64  `json:"inputTokens"`
	OutputTokens int64  `json:"outputTokens"`
	Status       int    `json:"status"`
	DurationMs   int64  `json:"durationMs"`
}

// usageScope builds the WHERE clause + args for a ts range with an optional
// per-user filter (nil = all users, superadmin views).
func usageScope(from, to int64, userID *int64) (string, []any) {
	where := "ts BETWEEN ? AND ?"
	args := []any{from, to}
	if userID != nil {
		where += " AND user_id = ?"
		args = append(args, *userID)
	}
	return where, args
}

func (s *Store) UsageTotals(from, to int64, userID *int64) (*UsageTotals, error) {
	where, args := usageScope(from, to, userID)
	t := &UsageTotals{}
	err := s.db.QueryRow(`SELECT COUNT(*),
			COALESCE(SUM(CASE WHEN status >= 400 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0)
		FROM usage_events WHERE `+where, args...).
		Scan(&t.Requests, &t.Errors, &t.InputTokens, &t.OutputTokens)
	return t, err
}

func (s *Store) UsageByModel(from, to int64, userID *int64, limit int) ([]UsageModelRow, error) {
	where, args := usageScope(from, to, userID)
	rows, err := s.db.Query(`SELECT model, COUNT(*), COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0)
		FROM usage_events WHERE `+where+` GROUP BY model
		ORDER BY SUM(input_tokens + output_tokens) DESC LIMIT ?`, append(args, limit)...)
	return scanUsageRows[UsageModelRow](rows, err)
}

func (s *Store) UsageByProvider(from, to int64, userID *int64) ([]UsageModelRow, error) {
	where, args := usageScope(from, to, userID)
	rows, err := s.db.Query(`SELECT provider, COUNT(*), COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0)
		FROM usage_events WHERE `+where+` GROUP BY provider
		ORDER BY SUM(input_tokens + output_tokens) DESC`, args...)
	return scanUsageRows[UsageModelRow](rows, err)
}

func (s *Store) UsageByUser(from, to int64) ([]UsageUserRow, error) {
	rows, err := s.db.Query(`SELECT u.id, u.email, COUNT(*), COALESCE(SUM(e.input_tokens), 0), COALESCE(SUM(e.output_tokens), 0)
		FROM usage_events e JOIN users u ON u.id = e.user_id
		WHERE e.ts BETWEEN ? AND ? GROUP BY u.id, u.email
		ORDER BY SUM(e.input_tokens + e.output_tokens) DESC`, from, to)
	return scanUsageRows[UsageUserRow](rows, err)
}

func (s *Store) UsageByKey(from, to int64, userID *int64) ([]UsageKeyRow, error) {
	where, args := usageScope(from, to, userID)
	rows, err := s.db.Query(`SELECT k.id, k.alias, u.id, u.email, COUNT(*),
			COALESCE(SUM(e.input_tokens), 0), COALESCE(SUM(e.output_tokens), 0)
		FROM usage_events e
		JOIN api_keys k ON k.id = e.client_key_id
		JOIN users u ON u.id = e.user_id
		WHERE `+where+` GROUP BY k.id, k.alias, u.id, u.email
		ORDER BY SUM(e.input_tokens + e.output_tokens) DESC`, args...)
	return scanUsageRows[UsageKeyRow](rows, err)
}

func (s *Store) UsageActivity(limit int, userID *int64) ([]UsageActivityRow, error) {
	where := ""
	args := []any{}
	if userID != nil {
		where = "WHERE e.user_id = ?"
		args = append(args, *userID)
	}
	rows, err := s.db.Query(`SELECT e.ts, u.email, k.alias, e.provider, e.model,
			e.input_tokens, e.output_tokens, e.status, e.duration_ms
		FROM usage_events e
		JOIN users u ON u.id = e.user_id
		JOIN api_keys k ON k.id = e.client_key_id
		`+where+` ORDER BY e.ts DESC, e.id DESC LIMIT ?`, append(args, limit)...)
	return scanUsageRows[UsageActivityRow](rows, err)
}

// scanUsageRows folds the Scan-rows-error dance for the usage queries.
func scanUsageRows[T any](rows *sql.Rows, err error) ([]T, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []T
	for rows.Next() {
		var r T
		v := reflect.ValueOf(&r).Elem()
		fields := make([]any, v.NumField())
		for i := range fields {
			fields[i] = v.Field(i).Addr().Interface()
		}
		if err := rows.Scan(fields...); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// --- usage time series (dashboard charts) ---

type UsagePoint struct {
	TS           int64 `json:"ts"`
	Requests     int64 `json:"requests"`
	InputTokens  int64 `json:"inputTokens"`
	OutputTokens int64 `json:"outputTokens"`
}

type UsageProviderPoint struct {
	TS       int64  `json:"ts"`
	Provider string `json:"provider"`
	Requests int64  `json:"requests"`
}

// usageBucketExpr buckets a unix-second column into fixed-size buckets
// aligned to a timezone offset (seconds east of UTC), all in SQL.
func usageBucketExpr(bucket, tz int64) string {
	return fmt.Sprintf("((ts - %d) / %d) * %d + %d", tz, bucket, bucket, tz)
}

func (s *Store) UsageTimeseries(from, to, bucket, tz int64, userID *int64) ([]UsagePoint, error) {
	where, args := usageScope(from, to, userID)
	expr := usageBucketExpr(bucket, tz)
	rows, err := s.db.Query(`SELECT `+expr+` AS b, COUNT(*),
			COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0)
		FROM usage_events WHERE `+where+` GROUP BY `+expr+` ORDER BY b`,
		args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UsagePoint
	for rows.Next() {
		var p UsagePoint
		if err := rows.Scan(&p.TS, &p.Requests, &p.InputTokens, &p.OutputTokens); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) UsageProviderTimeseries(from, to, bucket, tz int64, userID *int64) ([]UsageProviderPoint, error) {
	where, args := usageScope(from, to, userID)
	expr := usageBucketExpr(bucket, tz)
	rows, err := s.db.Query(`SELECT `+expr+` AS b, provider, COUNT(*)
		FROM usage_events WHERE `+where+` GROUP BY `+expr+`, provider ORDER BY b`,
		args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UsageProviderPoint
	for rows.Next() {
		var p UsageProviderPoint
		if err := rows.Scan(&p.TS, &p.Provider, &p.Requests); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// --- helpers ---

// hashSecret is the storage form of every bearer-worthy secret (client keys,
// session tokens): full sha256 hex.
func hashSecret(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func keyHint(plaintext string) string {
	if len(plaintext) > 10 {
		return plaintext[:10]
	}
	return plaintext
}

const secretAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// newSecret returns n characters from crypto/rand over a base62 alphabet.
func newSecret(n int) string {
	b := make([]byte, n)
	max := big.NewInt(int64(len(secretAlphabet)))
	for i := range b {
		v, err := rand.Int(rand.Reader, max)
		if err != nil {
			panic(err)
		}
		b[i] = secretAlphabet[v.Int64()]
	}
	return string(b)
}

// constantTimeEqual compares two sha256 hex digests.
func constantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
