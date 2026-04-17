package admin

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// TestMutationTrackerEviction verifies that expired entries are
// cleaned up from the mutation tracker to prevent memory leaks.
func TestMutationTrackerEviction(t *testing.T) {
	// Reset global state for the test.
	apikeyMut.mu.Lock()
	apikeyMut.hits = make(map[string]*apikeyMutState)
	apikeyMut.mu.Unlock()

	s := &Server{}

	// Simulate an old entry that should be evicted.
	apikeyMut.mu.Lock()
	apikeyMut.hits["old-user"] = &apikeyMutState{
		count:       5,
		windowStart: time.Now().Add(-apikeyMutWindow * 3),
	}
	apikeyMut.mu.Unlock()

	// Make a request that triggers cleanup.
	req, _ := http.NewRequestWithContext(
		context.WithValue(context.Background(), whoKey{}, "new-user"),
		http.MethodPost, "/apikeys", nil,
	)
	req.RemoteAddr = "127.0.0.1:12345"

	s.allowAPIKeyMutation(req)

	apikeyMut.mu.Lock()
	_, oldExists := apikeyMut.hits["old-user"]
	_, newExists := apikeyMut.hits["new-user"]
	apikeyMut.mu.Unlock()

	if oldExists {
		t.Error("expected old-user entry to be evicted")
	}
	if !newExists {
		t.Error("expected new-user entry to exist")
	}
}

// TestMutationTrackerRateLimit verifies that the rate limiter
// correctly blocks after exceeding the threshold.
func TestMutationTrackerRateLimit(t *testing.T) {
	apikeyMut.mu.Lock()
	apikeyMut.hits = make(map[string]*apikeyMutState)
	apikeyMut.mu.Unlock()

	s := &Server{}

	req, _ := http.NewRequestWithContext(
		context.WithValue(context.Background(), whoKey{}, "test-user"),
		http.MethodPost, "/apikeys", nil,
	)
	req.RemoteAddr = "127.0.0.1:12345"

	// First apikeyMutMaxOps calls should be allowed.
	for i := 0; i < apikeyMutMaxOps; i++ {
		if !s.allowAPIKeyMutation(req) {
			t.Fatalf("expected call %d to be allowed", i+1)
		}
	}

	// Next call should be blocked.
	if s.allowAPIKeyMutation(req) {
		t.Fatal("expected call to be blocked after exceeding limit")
	}
}
