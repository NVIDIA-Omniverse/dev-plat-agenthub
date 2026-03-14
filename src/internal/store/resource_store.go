package store

import "fmt"

// SetResourceCredential stores a credential value for a resource.
// key is a short name like "token", "refresh_token", "secret".
// The full store key becomes "resource:<resourceID>:<key>".
func (s *Store) SetResourceCredential(resourceID, key, value string) error {
	return s.Set(fmt.Sprintf("resource:%s:%s", resourceID, key), value)
}

// GetResourceCredential retrieves a credential value for a resource.
func (s *Store) GetResourceCredential(resourceID, key string) (string, error) {
	return s.Get(fmt.Sprintf("resource:%s:%s", resourceID, key))
}

// DeleteResourceCredentials removes all credential keys for a resource.
// It attempts to delete common keys; errors are ignored.
func (s *Store) DeleteResourceCredentials(resourceID string) {
	for _, k := range []string{"token", "refresh_token", "secret", "password", "api_key"} {
		_ = s.Delete(fmt.Sprintf("resource:%s:%s", resourceID, k))
	}
}
