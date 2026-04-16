package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Vozec/flarex/internal/pool"
)

// stubStore is an in-memory APIKeyStore for tests — no bbolt, no disk.
type stubStore struct {
	mu   sync.Mutex
	keys map[string]APIKeyRecord // keyed by ID
}

func newStubStore() *stubStore { return &stubStore{keys: map[string]APIKeyRecord{}} }

func (s *stubStore) PutAPIKey(k APIKeyRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys[k.ID] = k
	return nil
}
func (s *stubStore) ListAPIKeys() ([]APIKeyRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]APIKeyRecord, 0, len(s.keys))
	for _, k := range s.keys {
		out = append(out, k)
	}
	return out, nil
}
func (s *stubStore) GetAPIKey(id string) (APIKeyRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, ok := s.keys[id]
	if !ok {
		return APIKeyRecord{}, errNotFound
	}
	return k, nil
}
func (s *stubStore) DeleteAPIKey(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.keys, id)
	return nil
}
func (s *stubStore) MarkAPIKeyUsed(hash string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, k := range s.keys {
		if k.Hash == hash {
			k.LastUsedAt = time.Now().UTC()
			s.keys[id] = k
			return
		}
	}
}
func (s *stubStore) SetAPIKeyDisabled(id string, disabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, ok := s.keys[id]
	if !ok {
		return errNotFound
	}
	k.Disabled = disabled
	s.keys[id] = k
	return nil
}

type stubErr string

func (e stubErr) Error() string { return string(e) }

const errNotFound stubErr = "not found"

// spin builds a minimal admin Server with the apikeys bits wired up
// (no pool traffic, no auth required by default — auth tests opt in).
func spin(t *testing.T, withAuth bool) (*Server, *httptest.Server) {
	t.Helper()
	srv := &Server{
		Pool:      pool.New(nil),
		UIEnabled: true,
		APIKeys:   newStubStore(),
	}
	if withAuth {
		srv.APIKey = "test-bootstrap-key"
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/apikeys", srv.authed(srv.handleAPIKeys))
	mux.HandleFunc("/apikeys/", srv.authed(srv.handleAPIKeys))
	mux.HandleFunc("/status", srv.authed(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"pool_size":0,"workers":[]}`))
	}))
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return srv, ts
}

func doJSON(t *testing.T, method, url string, headers map[string]string, body any) (int, map[string]any) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	var req *http.Request
	var err error
	if reader != nil {
		req, err = http.NewRequest(method, url, reader)
	} else {
		req, err = http.NewRequest(method, url, nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestAPIKeys_CreateListDelete(t *testing.T) {
	_, ts := spin(t, false) // no auth → open for simplicity
	// Create
	status, body := doJSON(t, http.MethodPost, ts.URL+"/apikeys", nil, map[string]any{
		"name":   "scraper",
		"scopes": []string{"read", "write"},
	})
	if status != 200 {
		t.Fatalf("create status=%d body=%v", status, body)
	}
	if body["key"] == nil || !strings.HasPrefix(body["key"].(string), "flx_") {
		t.Fatalf("raw key missing / wrong prefix: %v", body["key"])
	}
	id, _ := body["id"].(string)
	// List
	status, body = doJSON(t, http.MethodGet, ts.URL+"/apikeys", nil, nil)
	if status != 200 {
		t.Fatalf("list status=%d", status)
	}
	keys := body["keys"].([]any)
	if len(keys) != 1 {
		t.Fatalf("expected 1 key, got %d", len(keys))
	}
	// Check hash was NOT leaked (only prefix)
	if k := keys[0].(map[string]any); k["hash"] != nil {
		t.Error("list leaks hash field")
	}
	// Delete
	status, _ = doJSON(t, http.MethodDelete, ts.URL+"/apikeys/"+id, nil, nil)
	if status != 200 {
		t.Fatalf("delete status=%d", status)
	}
	// List empty
	_, body = doJSON(t, http.MethodGet, ts.URL+"/apikeys", nil, nil)
	if len(body["keys"].([]any)) != 0 {
		t.Error("key survived delete")
	}
}

func TestAPIKeys_DuplicateNameRejected(t *testing.T) {
	_, ts := spin(t, false)
	status, _ := doJSON(t, http.MethodPost, ts.URL+"/apikeys", nil, map[string]any{"name": "dup"})
	if status != 200 {
		t.Fatalf("first create: %d", status)
	}
	status, body := doJSON(t, http.MethodPost, ts.URL+"/apikeys", nil, map[string]any{"name": "dup"})
	if status != http.StatusConflict {
		t.Fatalf("second create: status=%d body=%v", status, body)
	}
}

func TestAPIKeys_ExpiresIn(t *testing.T) {
	_, ts := spin(t, false)
	status, body := doJSON(t, http.MethodPost, ts.URL+"/apikeys", nil, map[string]any{
		"name":       "short-lived",
		"expires_in": "24h",
	})
	if status != 200 {
		t.Fatalf("status=%d body=%v", status, body)
	}
	if body["expires_at"] == nil {
		t.Fatal("expires_at missing in response")
	}
	// Invalid duration → 400
	status, _ = doJSON(t, http.MethodPost, ts.URL+"/apikeys", nil, map[string]any{
		"name": "bad", "expires_in": "not-a-duration",
	})
	if status != http.StatusBadRequest {
		t.Fatalf("bad duration expected 400, got %d", status)
	}
}

func TestAPIKeys_ScopeEnforcement_ReadOnlyKeyCannotCreate(t *testing.T) {
	_, ts := spin(t, true) // auth ON, bootstrap = "test-bootstrap-key"
	// Use bootstrap to create a read-only key
	status, body := doJSON(t, http.MethodPost, ts.URL+"/apikeys",
		map[string]string{"X-API-Key": "test-bootstrap-key"},
		map[string]any{"name": "ro", "scopes": []string{"read"}})
	if status != 200 {
		t.Fatalf("create ro: %d", status)
	}
	readOnlyKey := body["key"].(string)
	// Read-only key → GET /status OK
	status, _ = doJSON(t, http.MethodGet, ts.URL+"/status",
		map[string]string{"X-API-Key": readOnlyKey}, nil)
	if status != 200 {
		t.Fatalf("ro key /status: %d", status)
	}
	// Read-only key → POST /apikeys FORBIDDEN (needs `apikeys` scope)
	status, _ = doJSON(t, http.MethodPost, ts.URL+"/apikeys",
		map[string]string{"X-API-Key": readOnlyKey},
		map[string]any{"name": "nope"})
	if status != http.StatusForbidden {
		t.Fatalf("ro key creating keys: expected 403, got %d", status)
	}
}

func TestAPIKeys_DisabledKeyCannotAuth(t *testing.T) {
	srv, ts := spin(t, true)
	status, body := doJSON(t, http.MethodPost, ts.URL+"/apikeys",
		map[string]string{"X-API-Key": "test-bootstrap-key"},
		map[string]any{"name": "tmp", "scopes": []string{"read"}})
	if status != 200 {
		t.Fatalf("create: %d", status)
	}
	id := body["id"].(string)
	raw := body["key"].(string)
	// Disable
	status, _ = doJSON(t, http.MethodPatch, ts.URL+"/apikeys/"+id,
		map[string]string{"X-API-Key": "test-bootstrap-key"},
		map[string]any{"disabled": true})
	if status != 200 {
		t.Fatalf("patch: %d", status)
	}
	// Try to use → 401
	status, _ = doJSON(t, http.MethodGet, ts.URL+"/status",
		map[string]string{"X-API-Key": raw}, nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("disabled key: expected 401, got %d", status)
	}
	_ = srv
}
