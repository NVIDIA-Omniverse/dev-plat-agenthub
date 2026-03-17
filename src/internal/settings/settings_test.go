package settings

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// memPersister is an in-memory Persister for tests.
type memPersister struct {
	mu   sync.RWMutex
	data map[string]string
}

func newMem(initial map[string]string) *memPersister {
	if initial == nil {
		initial = make(map[string]string)
	}
	return &memPersister{data: initial}
}
func (m *memPersister) Set(k, v string) error {
	m.mu.Lock(); m.data[k] = v; m.mu.Unlock(); return nil
}
func (m *memPersister) Delete(k string) error {
	m.mu.Lock(); delete(m.data, k); m.mu.Unlock(); return nil
}
func (m *memPersister) Keys() []string {
	m.mu.RLock(); defer m.mu.RUnlock()
	keys := make([]string, 0, len(m.data))
	for k := range m.data { keys = append(keys, k) }
	return keys
}
func (m *memPersister) Get(k string) (string, error) {
	m.mu.RLock(); defer m.mu.RUnlock(); return m.data[k], nil
}

func TestGetMissing(t *testing.T) {
	s := New(newMem(nil))
	require.Equal(t, "", s.Get("no-such-key"))
}

func TestSetGet(t *testing.T) {
	s := New(newMem(nil))
	require.NoError(t, s.Set("foo", "bar"))
	require.Equal(t, "bar", s.Get("foo"))
}

func TestSetPersists(t *testing.T) {
	p := newMem(nil)
	s := New(p)
	_ = s.Set("foo", "bar")
	require.Equal(t, "bar", p.data["foo"])
}

func TestNewLoadsExisting(t *testing.T) {
	// Values already in the persister must be available immediately after New.
	p := newMem(map[string]string{"a": "1", "b": "2"})
	s := New(p)
	require.Equal(t, "1", s.Get("a"))
	require.Equal(t, "2", s.Get("b"))
}

func TestSeedDoesNotOverwrite(t *testing.T) {
	p := newMem(map[string]string{"k": "original"})
	s := New(p)
	s.Seed("k", "default")
	require.Equal(t, "original", s.Get("k"))
}

func TestSeedSetsDefault(t *testing.T) {
	s := New(newMem(nil))
	s.Seed("k", "default")
	require.Equal(t, "default", s.Get("k"))
}

func TestSeedEmptyValueIgnored(t *testing.T) {
	s := New(newMem(nil))
	s.Seed("k", "")
	require.Equal(t, "", s.Get("k"))
}

func TestWatchCalledOnSet(t *testing.T) {
	s := New(newMem(nil))
	var got string
	s.Watch("k", func(v string) { got = v })
	_ = s.Set("k", "hello")
	require.Equal(t, "hello", got)
}

func TestWatchCalledOnDelete(t *testing.T) {
	s := New(newMem(map[string]string{"k": "v"}))
	var got = "original"
	s.Watch("k", func(v string) { got = v })
	_ = s.Delete("k")
	require.Equal(t, "", got)
}

func TestWatchNotCalledForOtherKey(t *testing.T) {
	s := New(newMem(nil))
	var called bool
	s.Watch("a", func(v string) { called = true })
	_ = s.Set("b", "x")
	require.False(t, called)
}

func TestMultipleWatchers(t *testing.T) {
	s := New(newMem(nil))
	var count int32
	s.Watch("k", func(v string) { atomic.AddInt32(&count, 1) })
	s.Watch("k", func(v string) { atomic.AddInt32(&count, 1) })
	_ = s.Set("k", "v")
	require.Equal(t, int32(2), atomic.LoadInt32(&count))
}

func TestWatcherCanCallGet(t *testing.T) {
	// Verify watchers don't deadlock when they call Get inside the callback.
	s := New(newMem(nil))
	var seen string
	s.Watch("k", func(v string) { seen = s.Get("k") })
	_ = s.Set("k", "world")
	require.Equal(t, "world", seen)
}

func TestConcurrentReadWrite(t *testing.T) {
	s := New(newMem(nil))
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); _ = s.Set("k", "v") }()
		go func() { defer wg.Done(); _ = s.Get("k") }()
	}
	wg.Wait()
}

func TestKeys(t *testing.T) {
	s := New(newMem(nil))
	require.Empty(t, s.Keys())
	_ = s.Set("a", "1")
	_ = s.Set("b", "2")
	keys := s.Keys()
	require.Len(t, keys, 2)
	require.ElementsMatch(t, []string{"a", "b"}, keys)
}

func TestSetResourceCredential(t *testing.T) {
	s := New(newMem(nil))
	require.NoError(t, s.SetResourceCredential("r1", "token", "tok123"))
	v := s.Get("resource:r1:token")
	require.Equal(t, "tok123", v)
}

func TestGetResourceCredential(t *testing.T) {
	s := New(newMem(nil))
	_ = s.Set("resource:r1:api_key", "sk-test")
	v := s.GetResourceCredential("r1", "api_key")
	require.Equal(t, "sk-test", v)
}

func TestGetResourceCredentialMissing(t *testing.T) {
	s := New(newMem(nil))
	v := s.GetResourceCredential("r1", "api_key")
	require.Equal(t, "", v)
}

func TestDeleteResourceCredentials(t *testing.T) {
	s := New(newMem(nil))
	_ = s.SetResourceCredential("r1", "token", "tok")
	_ = s.SetResourceCredential("r1", "api_key", "key")
	_ = s.SetResourceCredential("r1", "password", "pw")

	s.DeleteResourceCredentials("r1")

	require.Equal(t, "", s.GetResourceCredential("r1", "token"))
	require.Equal(t, "", s.GetResourceCredential("r1", "api_key"))
	require.Equal(t, "", s.GetResourceCredential("r1", "password"))
}

func TestDeletePersistsAndNotifiesWatcher(t *testing.T) {
	p := newMem(map[string]string{"k": "v"})
	s := New(p)
	require.Equal(t, "v", s.Get("k"))
	require.NoError(t, s.Delete("k"))
	require.Equal(t, "", s.Get("k"))
	_, ok := p.data["k"]
	require.False(t, ok)
}

func TestSeedMultipleKeys(t *testing.T) {
	s := New(newMem(nil))
	s.Seed("x", "10")
	s.Seed("y", "20")
	require.Equal(t, "10", s.Get("x"))
	require.Equal(t, "20", s.Get("y"))
}

func TestSetOverwritesExisting(t *testing.T) {
	s := New(newMem(nil))
	_ = s.Set("k", "first")
	_ = s.Set("k", "second")
	require.Equal(t, "second", s.Get("k"))
}
