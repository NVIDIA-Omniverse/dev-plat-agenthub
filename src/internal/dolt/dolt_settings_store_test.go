package dolt

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestSettingsEncryptBadKey(t *testing.T) {
	db, _ := newMockDB(t)
	p := &DoltPersister{db: db, key: []byte("bad")}
	_, err := p.encrypt("test")
	require.Error(t, err)
}

func TestSettingsDecryptBadKey(t *testing.T) {
	db, _ := newMockDB(t)
	p := &DoltPersister{db: db, key: []byte("bad")}
	data := make([]byte, 40)
	_, _ = rand.Read(data)
	_, err := p.decrypt(base64.StdEncoding.EncodeToString(data))
	require.Error(t, err)
}

func TestSettingsDecryptBadBase64(t *testing.T) {
	db, _ := newMockDB(t)
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	p, err := NewDoltPersister(db, key)
	require.NoError(t, err)

	_, err = p.decrypt("!!!not-valid-base64!!!")
	require.Error(t, err)
	require.Contains(t, err.Error(), "base64")
}

func TestSettingsDecryptShortCiphertext(t *testing.T) {
	db, _ := newMockDB(t)
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	p, err := NewDoltPersister(db, key)
	require.NoError(t, err)

	short := base64.StdEncoding.EncodeToString([]byte("abc"))
	_, err = p.decrypt(short)
	require.Error(t, err)
	require.Contains(t, err.Error(), "too short")
}

func TestSettingsDecryptCorruptedCiphertext(t *testing.T) {
	db, _ := newMockDB(t)
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	p, err := NewDoltPersister(db, key)
	require.NoError(t, err)

	garbage := make([]byte, 40)
	_, _ = rand.Read(garbage)
	encoded := base64.StdEncoding.EncodeToString(garbage)
	_, err = p.decrypt(encoded)
	require.Error(t, err)
	require.Contains(t, err.Error(), "decryption failed")
}

func TestSettingsEncryptDecryptEmpty(t *testing.T) {
	db, _ := newMockDB(t)
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	p, err := NewDoltPersister(db, key)
	require.NoError(t, err)

	enc, err := p.encrypt("")
	require.NoError(t, err)
	dec, err := p.decrypt(enc)
	require.NoError(t, err)
	require.Equal(t, "", dec)
}

type settingsMigrationSource struct {
	keys   []string
	values map[string]string
	getErr error
}

func (m *settingsMigrationSource) Keys() []string { return m.keys }

func (m *settingsMigrationSource) Get(key string) (string, error) {
	if m.getErr != nil {
		return "", m.getErr
	}
	return m.values[key], nil
}

func TestMigrateFromSrcGetError(t *testing.T) {
	db, mock := newMockDB(t)
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	p, err := NewDoltPersister(db, key)
	require.NoError(t, err)

	src := &settingsMigrationSource{
		keys:   []string{"api_key"},
		getErr: fmt.Errorf("src read error"),
	}

	mock.ExpectQuery("SELECT value FROM settings").
		WithArgs("api_key").
		WillReturnRows(sqlmock.NewRows([]string{"value"}))

	require.NoError(t, p.MigrateFrom(src))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMigrateFromGetError(t *testing.T) {
	db, mock := newMockDB(t)
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	p, err := NewDoltPersister(db, key)
	require.NoError(t, err)

	src := &settingsMigrationSource{
		keys:   []string{"api_key"},
		values: map[string]string{"api_key": "sk-123"},
	}

	mock.ExpectQuery("SELECT value FROM settings").
		WithArgs("api_key").
		WillReturnError(fmt.Errorf("db read error"))

	err = p.MigrateFrom(src)
	require.Error(t, err)
}

func TestMigrateFromSetError(t *testing.T) {
	db, mock := newMockDB(t)
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	p, err := NewDoltPersister(db, key)
	require.NoError(t, err)

	src := &settingsMigrationSource{
		keys:   []string{"api_key"},
		values: map[string]string{"api_key": "sk-123"},
	}

	mock.ExpectQuery("SELECT value FROM settings").
		WithArgs("api_key").
		WillReturnRows(sqlmock.NewRows([]string{"value"}))
	mock.ExpectExec("INSERT INTO settings").
		WillReturnError(fmt.Errorf("db write error"))

	err = p.MigrateFrom(src)
	require.Error(t, err)
	require.Contains(t, err.Error(), "migrating key")
}

func TestMigrateFromEmptyValue(t *testing.T) {
	db, mock := newMockDB(t)
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	p, err := NewDoltPersister(db, key)
	require.NoError(t, err)

	src := &settingsMigrationSource{
		keys:   []string{"api_key"},
		values: map[string]string{"api_key": ""},
	}

	mock.ExpectQuery("SELECT value FROM settings").
		WithArgs("api_key").
		WillReturnRows(sqlmock.NewRows([]string{"value"}))

	require.NoError(t, p.MigrateFrom(src))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEnsureSaltBadHex(t *testing.T) {
	db, mock := newMockDB(t)

	mock.ExpectQuery("SELECT value FROM settings").
		WithArgs(saltKey).
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("not-valid-hex!!!"))

	_, err := ensureSalt(db)
	require.Error(t, err)
}

func TestEnsureSaltStoreError(t *testing.T) {
	db, mock := newMockDB(t)

	mock.ExpectQuery("SELECT value FROM settings").
		WithArgs(saltKey).
		WillReturnRows(sqlmock.NewRows([]string{"value"}))
	mock.ExpectExec("INSERT INTO settings").
		WillReturnError(fmt.Errorf("db write error"))

	_, err := ensureSalt(db)
	require.Error(t, err)
}
