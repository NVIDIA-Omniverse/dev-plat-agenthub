// Package settings provides a reactive, write-through configuration cache.
//
// All runtime configuration — secrets and tunable parameters — is held in
// memory for O(1) reads. Writes update memory, persist to the backing store,
// and synchronously notify any registered watchers so components can rebuild
// derived state (e.g. re-creating an HTTP client) without polling or restarting.
//
// Usage pattern:
//
//	// Startup: seed defaults from YAML, then load persisted overrides.
//	s := settings.New(store)
//	s.Seed("openai.model", cfg.OpenAI.Model)
//	s.Seed("openai.base_url", cfg.OpenAI.BaseURL)
//
//	// Component: react to changes rather than reading lazily.
//	s.Watch("openai_api_key", func(v string) { chatter.Rebuild() })
//	s.Watch("openai.model",   func(v string) { chatter.Rebuild() })
//
//	// Hot-path: O(1) memory read, no lock contention for reads.
//	token := s.Get("registration_token")
package settings

import (
	"fmt"
	"sync"
)

// Persister is the interface settings delegates to for durable storage.
// The encrypted store satisfies this interface.
type Persister interface {
	Set(key, value string) error
	Delete(key string) error
	Keys() []string
	Get(key string) (string, error)
}

// Store is a thread-safe, reactive, write-through configuration cache.
type Store struct {
	mu      sync.RWMutex
	data    map[string]string
	subs    map[string][]func(string)
	persist Persister
}

// New creates a Store, loading all keys from the backing Persister into memory.
func New(p Persister) *Store {
	s := &Store{
		data:    make(map[string]string),
		subs:    make(map[string][]func(string)),
		persist: p,
	}
	// Bulk-load everything persisted so all reads are in-memory from here on.
	for _, k := range p.Keys() {
		if v, err := p.Get(k); err == nil {
			s.data[k] = v
		}
	}
	return s
}

// Get returns the value for key, or "" if not set. Never returns an error —
// missing keys are simply empty strings. O(1), no I/O.
func (s *Store) Get(key string) string {
	s.mu.RLock()
	v := s.data[key]
	s.mu.RUnlock()
	return v
}

// Set writes key=value to memory, persists it, then notifies any watchers.
// Watchers are called outside the lock so they can safely call Get.
func (s *Store) Set(key, value string) error {
	if err := s.persist.Set(key, value); err != nil {
		return err
	}
	s.mu.Lock()
	s.data[key] = value
	fns := append(([]func(string))(nil), s.subs[key]...) // copy under lock
	s.mu.Unlock()

	for _, fn := range fns {
		fn(value)
	}
	return nil
}

// Delete removes a key from memory and the backing store.
func (s *Store) Delete(key string) error {
	if err := s.persist.Delete(key); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.data, key)
	fns := append(([]func(string))(nil), s.subs[key]...)
	s.mu.Unlock()

	for _, fn := range fns {
		fn("")
	}
	return nil
}

// Seed sets key=value only if the key is not already present in the store.
// Use this to apply YAML defaults without overwriting operator-set values.
func (s *Store) Seed(key, value string) {
	if value == "" {
		return
	}
	s.mu.Lock()
	if _, exists := s.data[key]; !exists {
		s.data[key] = value
	}
	s.mu.Unlock()
}

// Watch registers fn to be called whenever key is changed via Set or Delete.
// fn receives the new value ("" on delete). Multiple watchers per key are
// supported and called in registration order.
func (s *Store) Watch(key string, fn func(newValue string)) {
	s.mu.Lock()
	s.subs[key] = append(s.subs[key], fn)
	s.mu.Unlock()
}

// Keys returns all keys currently in the store.
func (s *Store) Keys() []string {
	s.mu.RLock()
	keys := make([]string, 0, len(s.data))
	for k := range s.data {
		keys = append(keys, k)
	}
	s.mu.RUnlock()
	return keys
}

// SetResourceCredential stores a credential for a resource.
// The full key is "resource:<resourceID>:<key>".
func (s *Store) SetResourceCredential(resourceID, key, value string) error {
	return s.Set(fmt.Sprintf("resource:%s:%s", resourceID, key), value)
}

// GetResourceCredential retrieves a credential for a resource. Returns "" if not set.
func (s *Store) GetResourceCredential(resourceID, key string) string {
	return s.Get(fmt.Sprintf("resource:%s:%s", resourceID, key))
}

// DeleteResourceCredentials removes common credential keys for a resource.
func (s *Store) DeleteResourceCredentials(resourceID string) {
	for _, k := range []string{"token", "refresh_token", "secret", "password", "api_key"} {
		_ = s.Delete(fmt.Sprintf("resource:%s:%s", resourceID, k))
	}
}
