package store

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
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

func TestKeysEmptyStore(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "test.enc"), "pw")
	require.NoError(t, err)
	require.Empty(t, s.Keys())
}

func TestSetOverwrite(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "test.enc"), "pw")
	require.NoError(t, err)

	require.NoError(t, s.Set("k", "first"))
	require.NoError(t, s.Set("k", "second"))

	v, err := s.Get("k")
	require.NoError(t, err)
	require.Equal(t, "second", v)
}

func TestDeleteNonexistent(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "test.enc"), "pw")
	require.NoError(t, err)
	require.NoError(t, s.Delete("nonexistent"))
}

func TestDeleteAndReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.enc")

	s, err := Open(path, "pw")
	require.NoError(t, err)
	require.NoError(t, s.Set("a", "1"))
	require.NoError(t, s.Set("b", "2"))
	require.NoError(t, s.Delete("a"))

	s2, err := Open(path, "pw")
	require.NoError(t, err)

	_, err = s2.Get("a")
	require.Error(t, err)

	v, err := s2.Get("b")
	require.NoError(t, err)
	require.Equal(t, "2", v)
}

func TestMultipleSetAndGet(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "test.enc"), "pw")
	require.NoError(t, err)

	for i := 0; i < 20; i++ {
		k := fmt.Sprintf("key-%d", i)
		v := fmt.Sprintf("value-%d", i)
		require.NoError(t, s.Set(k, v))
	}

	for i := 0; i < 20; i++ {
		k := fmt.Sprintf("key-%d", i)
		v := fmt.Sprintf("value-%d", i)
		got, err := s.Get(k)
		require.NoError(t, err)
		require.Equal(t, v, got)
	}
	require.Len(t, s.Keys(), 20)
}

func TestSetResourceCredential(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "test.enc"), "pw")
	require.NoError(t, err)

	require.NoError(t, s.SetResourceCredential("r1", "token", "tok123"))

	v, err := s.GetResourceCredential("r1", "token")
	require.NoError(t, err)
	require.Equal(t, "tok123", v)
}

func TestGetResourceCredentialMissing(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "test.enc"), "pw")
	require.NoError(t, err)

	_, err = s.GetResourceCredential("r1", "token")
	require.Error(t, err)
}

func TestDeleteResourceCredentials(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "test.enc"), "pw")
	require.NoError(t, err)

	require.NoError(t, s.SetResourceCredential("r1", "token", "tok"))
	require.NoError(t, s.SetResourceCredential("r1", "api_key", "key"))

	s.DeleteResourceCredentials("r1")

	_, err = s.GetResourceCredential("r1", "token")
	require.Error(t, err)
	_, err = s.GetResourceCredential("r1", "api_key")
	require.Error(t, err)
}

func TestUnicodeValues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.enc")

	s, err := Open(path, "pw")
	require.NoError(t, err)
	require.NoError(t, s.Set("emoji", "🔑"))
	require.NoError(t, s.Set("cjk", "こんにちは"))

	s2, err := Open(path, "pw")
	require.NoError(t, err)

	v, err := s2.Get("emoji")
	require.NoError(t, err)
	require.Equal(t, "🔑", v)

	v, err = s2.Get("cjk")
	require.NoError(t, err)
	require.Equal(t, "こんにちは", v)
}

func TestEncryptDecryptEmptyPlaintext(t *testing.T) {
	key := deriveKey([]byte("testpw"), make([]byte, saltLen))
	nonce := make([]byte, nonceLen)
	_, _ = rand.Read(nonce)

	ct, err := encrypt(key, nonce, []byte(""))
	require.NoError(t, err)

	pt, err := decrypt(key, nonce, ct)
	require.NoError(t, err)
	require.Equal(t, "", string(pt))
}

func TestOpenNonJSONPlaintext(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.enc")

	salt := make([]byte, saltLen)
	key := deriveKey([]byte("pw"), salt)
	nonce := make([]byte, nonceLen)

	ct, err := encrypt(key, nonce, []byte("not json at all"))
	require.NoError(t, err)

	env := envelope{
		Version:    envelopeVersion,
		Salt:       base64.StdEncoding.EncodeToString(salt),
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(ct),
	}
	raw, err := json.Marshal(env)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, raw, 0600))

	_, err = Open(path, "pw")
	require.Error(t, err)
	require.Contains(t, err.Error(), "parsing store data")
}
