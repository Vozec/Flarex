package admin

import (
	"sync"
	"testing"
	"time"
)

// stubAPIKeyStore is a minimal in-memory APIKeyStore for testing.
type stubAPIKeyStore struct {
	mu   sync.Mutex
	keys []APIKeyRecord
}

func (s *stubAPIKeyStore) PutAPIKey(k APIKeyRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys = append(s.keys, k)
	return nil
}
func (s *stubAPIKeyStore) ListAPIKeys() ([]APIKeyRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]APIKeyRecord, len(s.keys))
	copy(out, s.keys)
	return out, nil
}
func (s *stubAPIKeyStore) GetAPIKey(id string) (APIKeyRecord, error)        { return APIKeyRecord{}, nil }
func (s *stubAPIKeyStore) DeleteAPIKey(id string) error                     { return nil }
func (s *stubAPIKeyStore) MarkAPIKeyUsed(hash string)                       {}
func (s *stubAPIKeyStore) SetAPIKeyDisabled(id string, disabled bool) error { return nil }

// TestAPIKeyCacheLookup verifies that lookupAPIKeyByHash returns
// the correct key and builds the cache lazily.
func TestAPIKeyCacheLookup(t *testing.T) {
	store := &stubAPIKeyStore{
		keys: []APIKeyRecord{
			{ID: "k1", Name: "test-key", Hash: hashAPIKey("raw-key-1"), Scopes: []string{"read"}},
			{ID: "k2", Name: "admin-key", Hash: hashAPIKey("raw-key-2"), Scopes: []string{"read", "write"}},
		},
	}
	srv := &Server{APIKeys: store}

	// First lookup should build the cache.
	k, ok := srv.lookupAPIKeyByHash(hashAPIKey("raw-key-1"))
	if !ok {
		t.Fatal("expected to find key by hash")
	}
	if k.Name != "test-key" {
		t.Fatalf("expected test-key, got %s", k.Name)
	}

	// Second lookup should hit the cache (no store call).
	k2, ok := srv.lookupAPIKeyByHash(hashAPIKey("raw-key-2"))
	if !ok {
		t.Fatal("expected to find second key")
	}
	if k2.Name != "admin-key" {
		t.Fatalf("expected admin-key, got %s", k2.Name)
	}

	// Non-existent key.
	_, ok = srv.lookupAPIKeyByHash(hashAPIKey("nonexistent"))
	if ok {
		t.Fatal("expected miss for nonexistent key")
	}
}

// TestAPIKeyCacheInvalidation verifies that InvalidateAPIKeyCache
// forces a rebuild on the next lookup.
func TestAPIKeyCacheInvalidation(t *testing.T) {
	store := &stubAPIKeyStore{
		keys: []APIKeyRecord{
			{ID: "k1", Name: "old-key", Hash: hashAPIKey("raw-1")},
		},
	}
	srv := &Server{APIKeys: store}

	// Populate cache.
	_, ok := srv.lookupAPIKeyByHash(hashAPIKey("raw-1"))
	if !ok {
		t.Fatal("expected to find old-key")
	}

	// Add a new key to the store directly.
	store.PutAPIKey(APIKeyRecord{
		ID: "k2", Name: "new-key", Hash: hashAPIKey("raw-2"),
	})

	// Without invalidation, new key is invisible.
	_, ok = srv.lookupAPIKeyByHash(hashAPIKey("raw-2"))
	if ok {
		t.Fatal("new key should not be visible before invalidation")
	}

	// Invalidate and lookup again.
	srv.InvalidateAPIKeyCache()
	_, ok = srv.lookupAPIKeyByHash(hashAPIKey("raw-2"))
	if !ok {
		t.Fatal("new key should be visible after invalidation")
	}
}

// TestAPIKeyCacheConcurrent verifies that concurrent lookups and
// invalidations don't race.
func TestAPIKeyCacheConcurrent(t *testing.T) {
	store := &stubAPIKeyStore{
		keys: []APIKeyRecord{
			{ID: "k1", Name: "key-1", Hash: hashAPIKey("raw-1")},
		},
	}
	srv := &Server{APIKeys: store}

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			srv.lookupAPIKeyByHash(hashAPIKey("raw-1"))
		}()
		go func() {
			defer wg.Done()
			srv.InvalidateAPIKeyCache()
		}()
	}
	wg.Wait()
}

// TestAPIKeyCacheDisabledKey verifies that disabled keys are found
// in the cache but can be checked by the caller.
func TestAPIKeyCacheDisabledKey(t *testing.T) {
	store := &stubAPIKeyStore{
		keys: []APIKeyRecord{
			{ID: "k1", Name: "disabled-key", Hash: hashAPIKey("raw-1"), Disabled: true},
		},
	}
	srv := &Server{APIKeys: store}

	k, ok := srv.lookupAPIKeyByHash(hashAPIKey("raw-1"))
	if !ok {
		t.Fatal("disabled key should still be found in cache")
	}
	if !k.Disabled {
		t.Fatal("expected key to be disabled")
	}
}

// TestAPIKeyCacheExpiredKey verifies that expired keys are found
// in the cache (expiry is checked by the caller, not the cache).
func TestAPIKeyCacheExpiredKey(t *testing.T) {
	past := time.Now().Add(-1 * time.Hour)
	store := &stubAPIKeyStore{
		keys: []APIKeyRecord{
			{ID: "k1", Name: "expired-key", Hash: hashAPIKey("raw-1"), ExpiresAt: &past},
		},
	}
	srv := &Server{APIKeys: store}

	k, ok := srv.lookupAPIKeyByHash(hashAPIKey("raw-1"))
	if !ok {
		t.Fatal("expired key should still be in cache")
	}
	if k.ExpiresAt == nil || !time.Now().After(*k.ExpiresAt) {
		t.Fatal("expected key to be expired")
	}
}
