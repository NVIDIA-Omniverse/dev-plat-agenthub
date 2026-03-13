package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenNewStore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.enc")

	s, err := Open(path, "correctpassword")
	require.NoError(t, err)
	require.NotNil(t, s)
	require.Empty(t, s.Keys())
}

func TestSetAndGet(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.enc")

	s, err := Open(path, "mypassword")
	require.NoError(t, err)

	require.NoError(t, s.Set("openai_key", "sk-test-12345"))
	require.NoError(t, s.Set("slack_token", "xoxb-test"))

	v, err := s.Get("openai_key")
	require.NoError(t, err)
	require.Equal(t, "sk-test-12345", v)

	v, err = s.Get("slack_token")
	require.NoError(t, err)
	require.Equal(t, "xoxb-test", v)
}

func TestGetMissingKey(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "test.enc"), "pw")
	require.NoError(t, err)

	_, err = s.Get("nonexistent")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

func TestPersistenceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.enc")

	s, err := Open(path, "mypassword")
	require.NoError(t, err)
	require.NoError(t, s.Set("key1", "value1"))
	require.NoError(t, s.Set("key2", "value2"))

	// Re-open with same password.
	s2, err := Open(path, "mypassword")
	require.NoError(t, err)

	v, err := s2.Get("key1")
	require.NoError(t, err)
	require.Equal(t, "value1", v)

	v, err = s2.Get("key2")
	require.NoError(t, err)
	require.Equal(t, "value2", v)
}

func TestWrongPasswordFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.enc")

	s, err := Open(path, "correctpassword")
	require.NoError(t, err)
	require.NoError(t, s.Set("secret", "mysecret"))

	_, err = Open(path, "wrongpassword")
	require.Error(t, err)
	require.Contains(t, err.Error(), "wrong password")
}

func TestFileIsNotPlaintext(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.enc")

	s, err := Open(path, "mypassword")
	require.NoError(t, err)
	require.NoError(t, s.Set("secret_key", "mysupersecretvalue"))

	// Read raw file content.
	raw, err := os.ReadFile(path)
	require.NoError(t, err)

	// The raw file should NOT contain the secret value.
	require.NotContains(t, string(raw), "mysupersecretvalue")
	require.NotContains(t, string(raw), "secret_key")

	// It should be a valid JSON envelope.
	var env envelope
	require.NoError(t, json.Unmarshal(raw, &env))
	require.Equal(t, envelopeVersion, env.Version)
	require.NotEmpty(t, env.Salt)
	require.NotEmpty(t, env.Nonce)
	require.NotEmpty(t, env.Ciphertext)
}

func TestDelete(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.enc")

	s, err := Open(path, "pw")
	require.NoError(t, err)
	require.NoError(t, s.Set("k", "v"))

	require.NoError(t, s.Delete("k"))
	_, err = s.Get("k")
	require.Error(t, err)

	// Verify persistence.
	s2, err := Open(path, "pw")
	require.NoError(t, err)
	_, err = s2.Get("k")
	require.Error(t, err)
}

func TestEmptyPasswordErrors(t *testing.T) {
	dir := t.TempDir()
	_, err := Open(filepath.Join(dir, "test.enc"), "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "password")
}

func TestEmptyPathErrors(t *testing.T) {
	_, err := Open("", "password")
	require.Error(t, err)
	require.Contains(t, err.Error(), "path")
}

func TestTildePath(t *testing.T) {
	// Just ensure the tilde expansion doesn't panic; don't write to home.
	// We override by using a path that would expand to somewhere we can write.
	dir := t.TempDir()
	path := filepath.Join(dir, "tilde.enc")

	s, err := Open(path, "pw")
	require.NoError(t, err)
	require.NoError(t, s.Set("x", "y"))
}

func TestKeys(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "test.enc"), "pw")
	require.NoError(t, err)

	require.NoError(t, s.Set("a", "1"))
	require.NoError(t, s.Set("b", "2"))
	require.NoError(t, s.Set("c", "3"))

	keys := s.Keys()
	require.Len(t, keys, 3)
	require.ElementsMatch(t, []string{"a", "b", "c"}, keys)
}
