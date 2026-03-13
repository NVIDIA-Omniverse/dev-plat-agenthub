package store

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEncryptBadKeyLength(t *testing.T) {
	// AES requires key lengths of 16, 24, or 32 bytes; a short key triggers an error.
	_, err := encrypt([]byte("short-key"), []byte("123456789012"), []byte("plaintext"))
	require.Error(t, err)
}

func TestDecryptBadKeyLength(t *testing.T) {
	_, err := decrypt([]byte("short-key"), []byte("123456789012"), []byte("ciphertext"))
	require.Error(t, err)
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := make([]byte, 32)
	nonce := make([]byte, 12)
	plaintext := []byte(`{"hello":"world"}`)

	ciphertext, err := encrypt(key, nonce, plaintext)
	require.NoError(t, err)
	require.NotEqual(t, plaintext, ciphertext)

	recovered, err := decrypt(key, nonce, ciphertext)
	require.NoError(t, err)
	require.Equal(t, plaintext, recovered)
}

func TestDecryptBadCiphertext(t *testing.T) {
	key := make([]byte, 32)
	nonce := make([]byte, 12)
	// Tampered ciphertext → authentication failure.
	_, err := decrypt(key, nonce, []byte("bad ciphertext that is definitely not valid GCM"))
	require.Error(t, err)
}
