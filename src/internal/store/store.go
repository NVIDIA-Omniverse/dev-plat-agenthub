// Package store provides an encrypted key-value store for agenthub secrets.
//
// All sensitive values (API keys, Slack tokens, admin password hash, session secret)
// are stored here — never in config.yaml or environment variables.
//
// Encryption: AES-256-GCM with a key derived from the admin password via Argon2id.
// On-disk format: a JSON envelope containing base64-encoded salt, nonce, and ciphertext.
// The plaintext is a JSON map of string key→value pairs.
package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/crypto/argon2"
)

const (
	// Argon2id parameters (OWASP recommended minimum).
	argonTime    = 1
	argonMemory  = 64 * 1024 // 64 MB
	argonThreads = 4
	argonKeyLen  = 32 // AES-256

	saltLen  = 16
	nonceLen = 12

	envelopeVersion = 1
)

// envelope is the on-disk JSON structure.
type envelope struct {
	Version    int    `json:"version"`
	Salt       string `json:"salt"`       // base64
	Nonce      string `json:"nonce"`      // base64
	Ciphertext string `json:"ciphertext"` // base64
}

// Store is an encrypted key-value store backed by a single file.
// The encryption key is derived from the password supplied at Open time
// and is never stored on disk.
type Store struct {
	path     string
	key      []byte            // 32-byte AES-256 key
	salt     []byte            // stored salt (re-used across writes)
	data     map[string]string // decrypted in-memory map
}

// Open opens (or creates) the encrypted store at path, using password to
// derive the encryption key. If the file does not exist, an empty store is
// created in memory (call Set + a write to persist). If the file exists but
// the password is wrong, an error is returned.
func Open(path, password string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("store path must not be empty")
	}
	if password == "" {
		return nil, fmt.Errorf("password must not be empty")
	}

	// Expand ~ in path.
	if len(path) > 0 && path[0] == '~' {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolving home dir: %w", err)
		}
		path = filepath.Join(home, path[1:])
	}

	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		// New store: generate a fresh salt and empty data map.
		salt := make([]byte, saltLen)
		if _, err := io.ReadFull(rand.Reader, salt); err != nil {
			return nil, fmt.Errorf("generating salt: %w", err)
		}
		key := deriveKey([]byte(password), salt)
		return &Store{
			path: path,
			key:  key,
			salt: salt,
			data: make(map[string]string),
		}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading store file: %w", err)
	}

	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("parsing store envelope: %w", err)
	}

	salt, err := base64.StdEncoding.DecodeString(env.Salt)
	if err != nil {
		return nil, fmt.Errorf("decoding salt: %w", err)
	}
	nonce, err := base64.StdEncoding.DecodeString(env.Nonce)
	if err != nil {
		return nil, fmt.Errorf("decoding nonce: %w", err)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(env.Ciphertext)
	if err != nil {
		return nil, fmt.Errorf("decoding ciphertext: %w", err)
	}

	key := deriveKey([]byte(password), salt)

	plaintext, err := decrypt(key, nonce, ciphertext)
	if err != nil {
		// AES-GCM authentication failure means wrong password.
		return nil, fmt.Errorf("decrypting store (wrong password?): %w", err)
	}

	var data map[string]string
	if err := json.Unmarshal(plaintext, &data); err != nil {
		return nil, fmt.Errorf("parsing store data: %w", err)
	}

	return &Store{
		path: path,
		key:  key,
		salt: salt,
		data: data,
	}, nil
}

// Get returns the value for key, or an error if the key does not exist.
func (s *Store) Get(key string) (string, error) {
	v, ok := s.data[key]
	if !ok {
		return "", fmt.Errorf("key %q not found in store", key)
	}
	return v, nil
}

// Set writes a key-value pair and persists the store to disk.
func (s *Store) Set(key, value string) error {
	s.data[key] = value
	return s.save()
}

// Delete removes a key and persists the store to disk.
func (s *Store) Delete(key string) error {
	delete(s.data, key)
	return s.save()
}

// Keys returns all keys currently stored.
func (s *Store) Keys() []string {
	keys := make([]string, 0, len(s.data))
	for k := range s.data {
		keys = append(keys, k)
	}
	return keys
}

// save encrypts the in-memory data map and writes it to disk.
func (s *Store) save() error {
	plaintext, err := json.Marshal(s.data)
	if err != nil {
		return fmt.Errorf("marshaling store data: %w", err)
	}

	nonce := make([]byte, nonceLen)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return fmt.Errorf("generating nonce: %w", err)
	}

	ciphertext, err := encrypt(s.key, nonce, plaintext)
	if err != nil {
		return fmt.Errorf("encrypting store: %w", err)
	}

	env := envelope{
		Version:    envelopeVersion,
		Salt:       base64.StdEncoding.EncodeToString(s.salt),
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(ciphertext),
	}

	raw, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling envelope: %w", err)
	}

	// Ensure parent directory exists.
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return fmt.Errorf("creating store directory: %w", err)
	}

	// Write atomically via temp file.
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0600); err != nil {
		return fmt.Errorf("writing store temp file: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("renaming store temp file: %w", err)
	}
	return nil
}

// deriveKey derives a 32-byte AES key from password and salt using Argon2id.
func deriveKey(password, salt []byte) []byte {
	return argon2.IDKey(password, salt, argonTime, argonMemory, argonThreads, argonKeyLen)
}

// encrypt encrypts plaintext with AES-256-GCM using key and nonce.
func encrypt(key, nonce, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm.Seal(nil, nonce, plaintext, nil), nil
}

// decrypt decrypts ciphertext with AES-256-GCM using key and nonce.
func decrypt(key, nonce, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, nonce, ciphertext, nil)
}
