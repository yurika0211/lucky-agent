// Package credentials stores local credentials encrypted at rest.
package credentials

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

const (
	defaultScope = "default"
	keyID        = "local-file-v1"
	aadVersion   = 1
	dekSize      = 32
)

// Metadata is the non-secret information shown by credential list commands.
type Metadata struct {
	ID        string
	Kind      string
	Scope     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Store is a local, file-backed credential vault. The KEK is deliberately
// kept outside SQLite so a database copy does not contain everything needed to
// decrypt its records.
type Store struct {
	db  *sql.DB
	kek []byte
}

// NewStore opens the credential vault below the LuckyAgent home directory.
// It creates the runtime directory with owner-only permissions and generates
// a local KEK on first use.
func NewStore(homeDir string) (*Store, error) {
	homeDir = strings.TrimSpace(homeDir)
	if homeDir == "" {
		return nil, fmt.Errorf("credential home directory is required")
	}
	runtimeDir := filepath.Join(homeDir, "runtime")
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		return nil, fmt.Errorf("create credential runtime directory: %w", err)
	}
	if err := os.Chmod(runtimeDir, 0o700); err != nil {
		return nil, fmt.Errorf("secure credential runtime directory: %w", err)
	}

	kek, err := loadOrCreateKEK(filepath.Join(runtimeDir, "credential.kek"))
	if err != nil {
		return nil, err
	}

	dbPath := filepath.Join(runtimeDir, "credentials.db")
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on")
	if err != nil {
		zero(kek)
		return nil, fmt.Errorf("open credential database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		zero(kek)
		return nil, fmt.Errorf("ping credential database: %w", err)
	}
	if err := os.Chmod(dbPath, 0o600); err != nil {
		db.Close()
		zero(kek)
		return nil, fmt.Errorf("secure credential database: %w", err)
	}
	if err := initSchema(db); err != nil {
		db.Close()
		zero(kek)
		return nil, err
	}
	return &Store{db: db, kek: kek}, nil
}

// Close closes the database and clears the in-memory KEK as a best effort.
func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	zero(s.kek)
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Put inserts or replaces a credential. The value is never written as
// plaintext; each record receives a new random DEK and nonces.
func (s *Store) Put(ctx context.Context, id, kind, scope, value string) error {
	return s.put(ctx, id, kind, scope, []byte(value))
}

// PutBytes is the byte-oriented form used by hidden-input frontends so they
// can clear their input buffer after the encrypted write completes.
func (s *Store) PutBytes(ctx context.Context, id, kind, scope string, value []byte) error {
	return s.put(ctx, id, kind, scope, value)
}

func (s *Store) put(ctx context.Context, id, kind, scope string, value []byte) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("credential store is closed")
	}
	ctx = ensureContext(ctx)
	id, err := validatePart("credential id", id)
	if err != nil {
		return err
	}
	kind, err = validatePart("credential kind", kind)
	if err != nil {
		return err
	}
	scope, err = normalizeScope(scope)
	if err != nil {
		return err
	}
	if len(value) == 0 {
		return fmt.Errorf("credential value is required")
	}
	if err := contextErr(ctx); err != nil {
		return err
	}

	dek := make([]byte, dekSize)
	if _, err := io.ReadFull(rand.Reader, dek); err != nil {
		return fmt.Errorf("generate credential key: %w", err)
	}
	defer zero(dek)

	aad := associatedData(id, kind, scope)
	ciphertext, nonce, err := seal(dek, value, aad)
	if err != nil {
		return fmt.Errorf("encrypt credential: %w", err)
	}
	wrappedDEK, wrapNonce, err := seal(s.kek, dek, wrapAssociatedData(aad))
	if err != nil {
		return fmt.Errorf("wrap credential key: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO credentials
			(id, kind, scope, ciphertext, nonce, wrapped_dek, wrap_nonce, key_id, aad_version, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			kind = excluded.kind,
			scope = excluded.scope,
			ciphertext = excluded.ciphertext,
			nonce = excluded.nonce,
			wrapped_dek = excluded.wrapped_dek,
			wrap_nonce = excluded.wrap_nonce,
			key_id = excluded.key_id,
			aad_version = excluded.aad_version,
			updated_at = excluded.updated_at`,
		id, kind, scope, ciphertext, nonce, wrappedDEK, wrapNonce, keyID, aadVersion, now, now)
	if err != nil {
		return fmt.Errorf("save credential: %w", err)
	}
	return nil
}

// WithCredential decrypts one credential for the duration of fn. The value is
// wiped from the temporary byte buffer after fn returns. Callers should avoid
// copying it outside the callback.
func (s *Store) WithCredential(ctx context.Context, id string, fn func([]byte) error) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("credential store is closed")
	}
	if fn == nil {
		return fmt.Errorf("credential callback is required")
	}
	ctx = ensureContext(ctx)
	id, err := validatePart("credential id", id)
	if err != nil {
		return err
	}
	if err := contextErr(ctx); err != nil {
		return err
	}

	var kind, scope, keyID string
	var ciphertext, nonce, wrappedDEK, wrapNonce []byte
	var version int
	err = s.db.QueryRowContext(ctx, `
		SELECT kind, scope, ciphertext, nonce, wrapped_dek, wrap_nonce, key_id, aad_version
		FROM credentials WHERE id = ?`, id).Scan(
		&kind, &scope, &ciphertext, &nonce, &wrappedDEK, &wrapNonce, &keyID, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("credential %q not found", id)
	}
	if err != nil {
		return fmt.Errorf("load credential: %w", err)
	}
	if keyID != keyIDForStore() {
		return fmt.Errorf("credential %q uses unsupported key %q", id, keyID)
	}
	if version != aadVersion {
		return fmt.Errorf("credential %q uses unsupported AAD version %d", id, version)
	}

	aad := associatedData(id, kind, scope)
	dek, err := open(s.kek, wrappedDEK, wrapNonce, wrapAssociatedData(aad))
	if err != nil {
		return fmt.Errorf("unwrap credential key: %w", err)
	}
	defer zero(dek)
	plaintext, err := open(dek, ciphertext, nonce, aad)
	if err != nil {
		return fmt.Errorf("decrypt credential: %w", err)
	}
	defer zero(plaintext)
	if err := contextErr(ctx); err != nil {
		return err
	}
	return fn(plaintext)
}

// List returns metadata only; credential values are never returned.
func (s *Store) List(ctx context.Context) ([]Metadata, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("credential store is closed")
	}
	ctx = ensureContext(ctx)
	rows, err := s.db.QueryContext(ctx, `SELECT id, kind, scope, created_at, updated_at FROM credentials`)
	if err != nil {
		return nil, fmt.Errorf("list credentials: %w", err)
	}
	defer rows.Close()

	var result []Metadata
	for rows.Next() {
		var item Metadata
		var created, updated string
		if err := rows.Scan(&item.ID, &item.Kind, &item.Scope, &created, &updated); err != nil {
			return nil, fmt.Errorf("read credential metadata: %w", err)
		}
		item.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return nil, fmt.Errorf("parse credential creation time: %w", err)
		}
		item.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
		if err != nil {
			return nil, fmt.Errorf("parse credential update time: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate credentials: %w", err)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

// Delete removes a credential by reference.
func (s *Store) Delete(ctx context.Context, id string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("credential store is closed")
	}
	ctx = ensureContext(ctx)
	id, err := validatePart("credential id", id)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM credentials WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete credential: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check credential deletion: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("credential %q not found", id)
	}
	return nil
}

func initSchema(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS credentials (
		id TEXT PRIMARY KEY,
		kind TEXT NOT NULL,
		scope TEXT NOT NULL,
		ciphertext BLOB NOT NULL,
		nonce BLOB NOT NULL,
		wrapped_dek BLOB NOT NULL,
		wrap_nonce BLOB NOT NULL,
		key_id TEXT NOT NULL,
		aad_version INTEGER NOT NULL,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`)
	if err != nil {
		return fmt.Errorf("initialize credential database: %w", err)
	}
	return nil
}

func loadOrCreateKEK(path string) ([]byte, error) {
	file, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err == nil {
		defer file.Close()
		key, readErr := io.ReadAll(file)
		if readErr != nil {
			return nil, fmt.Errorf("read credential KEK: %w", readErr)
		}
		if len(key) != dekSize {
			zero(key)
			return nil, fmt.Errorf("credential KEK must be %d bytes", dekSize)
		}
		if chmodErr := os.Chmod(path, 0o600); chmodErr != nil {
			zero(key)
			return nil, fmt.Errorf("secure credential KEK: %w", chmodErr)
		}
		return key, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("open credential KEK: %w", err)
	}

	key := make([]byte, dekSize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		zero(key)
		return nil, fmt.Errorf("generate credential KEK: %w", err)
	}
	file, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		zero(key)
		if os.IsExist(err) {
			return loadOrCreateKEK(path)
		}
		return nil, fmt.Errorf("create credential KEK: %w", err)
	}
	if _, err := file.Write(key); err != nil {
		file.Close()
		_ = os.Remove(path)
		zero(key)
		return nil, fmt.Errorf("write credential KEK: %w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		zero(key)
		return nil, fmt.Errorf("sync credential KEK: %w", err)
	}
	if err := file.Close(); err != nil {
		zero(key)
		return nil, fmt.Errorf("close credential KEK: %w", err)
	}
	return key, nil
}

func seal(key, plaintext, aad []byte) ([]byte, []byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, err
	}
	return gcm.Seal(nil, nonce, plaintext, aad), nonce, nil
}

func open(key, ciphertext, nonce, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(nonce) != gcm.NonceSize() {
		return nil, fmt.Errorf("invalid nonce length")
	}
	return gcm.Open(nil, nonce, ciphertext, aad)
}

type aadEnvelope struct {
	CredentialID string `json:"credential_id"`
	Kind         string `json:"kind"`
	Scope        string `json:"scope"`
	Version      int    `json:"version"`
}

func associatedData(id, kind, scope string) []byte {
	data, _ := json.Marshal(aadEnvelope{CredentialID: id, Kind: kind, Scope: scope, Version: aadVersion})
	return data
}

func wrapAssociatedData(aad []byte) []byte {
	return append([]byte("luckyagent:dek-wrap:v1\x00"), aad...)
}

func validatePart(name, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	if strings.ContainsAny(value, "\x00\r\n") {
		return "", fmt.Errorf("%s contains an invalid control character", name)
	}
	return value, nil
}

func normalizeScope(scope string) (string, error) {
	if strings.TrimSpace(scope) == "" {
		scope = defaultScope
	}
	return validatePart("credential scope", scope)
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func ensureContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func keyIDForStore() string { return keyID }

func zero(value []byte) {
	for i := range value {
		value[i] = 0
	}
}
