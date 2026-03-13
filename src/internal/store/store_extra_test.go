package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.enc")
	require.NoError(t, os.WriteFile(path, []byte("not valid json"), 0600))

	_, err := Open(path, "password")
	require.Error(t, err)
	require.Contains(t, err.Error(), "parsing store envelope")
}

func TestOpenCorruptBase64Salt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.enc")
	// Valid JSON but invalid base64 in salt field.
	require.NoError(t, os.WriteFile(path, []byte(`{"version":1,"salt":"!!!not-base64!!!","nonce":"","ciphertext":""}`), 0600))

	_, err := Open(path, "password")
	require.Error(t, err)
	require.Contains(t, err.Error(), "decoding salt")
}

func TestOpenCorruptBase64Nonce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.enc")
	// Valid JSON, valid base64 salt, invalid base64 nonce.
	validSalt := "AAAAAAAAAAAAAAAAAAAAAA==" // 16 bytes in base64
	require.NoError(t, os.WriteFile(path, []byte(`{"version":1,"salt":"`+validSalt+`","nonce":"!!!bad!!!","ciphertext":""}`), 0600))

	_, err := Open(path, "password")
	require.Error(t, err)
	require.Contains(t, err.Error(), "decoding nonce")
}

func TestOpenCorruptBase64Ciphertext(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.enc")
	validSalt := "AAAAAAAAAAAAAAAAAAAAAA==" // 16 bytes
	validNonce := "AAAAAAAAAAAAAAAA"         // 12 bytes in base64
	require.NoError(t, os.WriteFile(path, []byte(`{"version":1,"salt":"`+validSalt+`","nonce":"`+validNonce+`","ciphertext":"!!!bad!!!"}`), 0600))

	_, err := Open(path, "password")
	require.Error(t, err)
	require.Contains(t, err.Error(), "decoding ciphertext")
}

func TestOpenDecryptionError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.enc")

	// Write with correct password.
	s, err := Open(path, "correct")
	require.NoError(t, err)
	require.NoError(t, s.Set("k", "v"))

	// Re-open with wrong password (AES-GCM authentication failure).
	_, err = Open(path, "wrong-password")
	require.Error(t, err)
	require.Contains(t, err.Error(), "wrong password")
}

func TestOpenTildePath(t *testing.T) {
	// Test that tilde expansion works for a path starting with ~.
	// We can't easily test writing to home, but we can test that a
	// non-existent path under ~ starts a new store (not an error).
	_, err := Open("~/nonexistent-agenthub-test-store.enc", "password")
	// Should succeed (creates new in-memory store), even if parent dir doesn't exist
	// The actual save won't happen until Set is called.
	require.NoError(t, err)
}

func TestOpenReadPermissionError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "unreadable.enc")
	// Create the file so ReadFile finds it, then make it unreadable.
	require.NoError(t, os.WriteFile(path, []byte("data"), 0600))
	require.NoError(t, os.Chmod(path, 0000))
	t.Cleanup(func() { _ = os.Chmod(path, 0600) })

	_, err := Open(path, "password")
	if err == nil {
		t.Skip("running as root or chmod not enforced — skip this test")
	}
	require.Error(t, err)
	require.Contains(t, err.Error(), "reading store file")
}

func TestSaveWriteFileError(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "store.enc")

	// Create store and save once so the file exists.
	st, err := Open(storePath, "password")
	require.NoError(t, err)
	require.NoError(t, st.Set("k", "v"))

	// Now make the directory unwritable so WriteFile(.tmp) fails,
	// but MkdirAll(dir) will succeed (dir already exists).
	require.NoError(t, os.Chmod(dir, 0500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) })

	err = st.Set("k2", "v2")
	if err == nil {
		t.Skip("running as root or chmod not enforced — skip this test")
	}
	require.Error(t, err)
	require.Contains(t, err.Error(), "writing store temp file")
}

func TestSaveMkdirAllError(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "subdir", "store.enc")

	// Open creates an in-memory store (path doesn't exist on disk yet).
	st, err := Open(storePath, "password")
	require.NoError(t, err)

	// Make the parent dir unwritable so MkdirAll("dir/subdir") fails.
	require.NoError(t, os.Chmod(dir, 0500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) }) // restore for TempDir cleanup

	// Set triggers save which calls MkdirAll → should fail.
	err = st.Set("key", "value")
	if err == nil {
		t.Skip("running as root or chmod not enforced — skip this test")
	}
	require.Error(t, err)
	require.Contains(t, err.Error(), "creating store directory")
}
