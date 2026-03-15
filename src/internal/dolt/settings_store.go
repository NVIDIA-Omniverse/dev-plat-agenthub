package dolt

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"

	"golang.org/x/crypto/argon2"
)

// saltKey is stored verbatim (unencrypted) in the settings table.
// All other keys are encrypted with AES-256-GCM.
const saltKey = "_salt"

const (
	argon2Time    = 1
	argon2Memory  = 64 * 1024
	argon2Threads = 4
	argon2KeyLen  = 32
)

// DoltPersister implements settings.Persister using the Dolt settings table.
// All values except saltKey are encrypted with AES-256-GCM.
type DoltPersister struct {
	db  *DB
	key []byte // 32-byte AES-256 master key
}

// NewDoltPersister returns a DoltPersister.
// key must be exactly 32 bytes (AES-256). Use OpenDoltPersister for the full init flow.
func NewDoltPersister(db *DB, key []byte) (*DoltPersister, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("dolt persister: key must be 32 bytes, got %d", len(key))
	}
	return &DoltPersister{db: db, key: key}, nil
}

// OpenDoltPersister opens (or creates) the DoltPersister for the given DB and password.
// It reads or generates the Argon2id salt from the settings table and derives the key.
// Returns the persister. Does NOT verify the password against any stored hash.
func OpenDoltPersister(db *DB, password string) (*DoltPersister, error) {
	salt, err := ensureSalt(db)
	if err != nil {
		return nil, fmt.Errorf("initialising salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, argon2Time, argon2Memory, argon2Threads, argon2KeyLen)
	return NewDoltPersister(db, key)
}

// IsInitialised reports whether the settings table has been populated (admin_password_hash exists).
func IsInitialised(db *DB) (bool, error) {
	var count int
	err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM settings WHERE key_name = 'admin_password_hash'`).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// Set encrypts value and upserts it into the settings table.
// The special key "_salt" is stored unencrypted.
func (p *DoltPersister) Set(key, value string) error {
	stored := value
	if key != saltKey {
		enc, err := p.encrypt(value)
		if err != nil {
			return fmt.Errorf("encrypting %q: %w", key, err)
		}
		stored = enc
	}
	ctx := context.Background()
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO settings (key_name, value)
		VALUES (?, ?)
		ON DUPLICATE KEY UPDATE value = VALUES(value), updated_at = NOW()`,
		key, stored,
	)
	if err != nil {
		return fmt.Errorf("storing setting %q: %w", key, err)
	}
	return nil
}

// Get returns the decrypted value for key, or ("", nil) if not found.
func (p *DoltPersister) Get(key string) (string, error) {
	ctx := context.Background()
	var stored string
	err := p.db.QueryRowContext(ctx,
		`SELECT value FROM settings WHERE key_name = ?`, key,
	).Scan(&stored)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("querying setting %q: %w", key, err)
	}
	if key == saltKey {
		return stored, nil
	}
	return p.decrypt(stored)
}

// Delete removes a key from the settings table.
func (p *DoltPersister) Delete(key string) error {
	ctx := context.Background()
	_, err := p.db.ExecContext(ctx, `DELETE FROM settings WHERE key_name = ?`, key)
	if err != nil {
		return fmt.Errorf("deleting setting %q: %w", key, err)
	}
	return nil
}

// Keys returns all key names in the settings table (excluding the internal salt key).
func (p *DoltPersister) Keys() []string {
	ctx := context.Background()
	rows, err := p.db.QueryContext(ctx,
		`SELECT key_name FROM settings WHERE key_name != ?`, saltKey)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err == nil {
			keys = append(keys, k)
		}
	}
	return keys
}

// MigrateFrom copies all keys from src (a settings.Persister) into this persister,
// skipping keys that already exist. Used for one-time migration from the old file store.
// src is the old store.Store (which implements settings.Persister).
func (p *DoltPersister) MigrateFrom(src interface {
	Keys() []string
	Get(key string) (string, error)
}) error {
	for _, k := range src.Keys() {
		if k == saltKey {
			continue
		}
		// Skip if already set in Dolt.
		existing, err := p.Get(k)
		if err != nil {
			return err
		}
		if existing != "" {
			continue
		}
		v, err := src.Get(k)
		if err != nil || v == "" {
			continue
		}
		if err := p.Set(k, v); err != nil {
			return fmt.Errorf("migrating key %q: %w", k, err)
		}
	}
	return nil
}

// ensureSalt reads the salt from the settings table, generating and storing
// a fresh 16-byte random salt if it does not yet exist.
func ensureSalt(db *DB) ([]byte, error) {
	raw, err := readSaltRaw(db)
	if err != nil {
		return nil, err
	}
	if raw != "" {
		return hex.DecodeString(raw)
	}
	// Generate new salt.
	salt := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, err
	}
	if err := storeSaltRaw(db, hex.EncodeToString(salt)); err != nil {
		return nil, err
	}
	return salt, nil
}

func readSaltRaw(db *DB) (string, error) {
	var salt string
	err := db.QueryRowContext(context.Background(),
		`SELECT value FROM settings WHERE key_name = ?`, saltKey).Scan(&salt)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("reading salt: %w", err)
	}
	return salt, nil
}

func storeSaltRaw(db *DB, salt string) error {
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO settings (key_name, value)
		VALUES (?, ?)
		ON DUPLICATE KEY UPDATE value = VALUES(value), updated_at = NOW()`,
		saltKey, salt,
	)
	return err
}

// encrypt encrypts plaintext with AES-256-GCM and returns base64(nonce + ciphertext).
func (p *DoltPersister) encrypt(plaintext string) (string, error) {
	block, err := aes.NewCipher(p.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// decrypt decodes base64(nonce + ciphertext) and decrypts with AES-256-GCM.
func (p *DoltPersister) decrypt(encoded string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("base64 decode: %w", err)
	}
	block, err := aes.NewCipher(p.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(data) < gcm.NonceSize() {
		return "", fmt.Errorf("ciphertext too short")
	}
	nonce, ct := data[:gcm.NonceSize()], data[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", fmt.Errorf("decryption failed (wrong password?): %w", err)
	}
	return string(plaintext), nil
}
