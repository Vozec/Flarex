package admin

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Vozec/flarex/internal/logger"
)

// handleAPIKeys dispatches /apikeys based on method. GET = list, POST =
// create. PATCH/DELETE on /apikeys/{id} routed via handleAPIKeyOne.
func (s *Server) handleAPIKeys(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !s.UIEnabled {
		http.NotFound(w, r)
		return
	}
	if s.APIKeys == nil {
		http.Error(w, `{"error":"api keys disabled (no state store)"}`, http.StatusServiceUnavailable)
		return
	}
	// /apikeys or /apikeys/ → collection
	// /apikeys/{id}       → single
	path := strings.TrimPrefix(r.URL.Path, "/apikeys")
	path = strings.TrimPrefix(path, "/")
	if path == "" {
		switch r.Method {
		case http.MethodGet:
			s.apiKeysList(w, r)
		case http.MethodPost:
			if !s.allowAPIKeyMutation(r) {
				http.Error(w, rateLimitMsg, http.StatusTooManyRequests)
				return
			}
			s.apiKeysCreate(w, r)
		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
		return
	}
	// path is the ID
	id := path
	switch r.Method {
	case http.MethodDelete:
		if !s.allowAPIKeyMutation(r) {
			http.Error(w, rateLimitMsg, http.StatusTooManyRequests)
			return
		}
		s.apiKeysDelete(w, r, id)
	case http.MethodPatch:
		if !s.allowAPIKeyMutation(r) {
			http.Error(w, rateLimitMsg, http.StatusTooManyRequests)
			return
		}
		s.apiKeysPatch(w, r, id)
	case http.MethodGet:
		s.apiKeysGet(w, r, id)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (s *Server) apiKeysList(w http.ResponseWriter, _ *http.Request) {
	keys, err := s.APIKeys.ListAPIKeys()
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}
	// Strip hash from the wire representation — prefix is enough for UI.
	out := make([]map[string]any, 0, len(keys))
	now := time.Now()
	for _, k := range keys {
		expired := k.ExpiresAt != nil && now.After(*k.ExpiresAt)
		out = append(out, map[string]any{
			"id":           k.ID,
			"name":         k.Name,
			"prefix":       k.Prefix,
			"scopes":       k.Scopes,
			"disabled":     k.Disabled,
			"expired":      expired,
			"created_at":   k.CreatedAt,
			"last_used_at": k.LastUsedAt,
			"expires_at":   k.ExpiresAt,
		})
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"keys": out})
}

type createAPIKeyReq struct {
	Name      string   `json:"name"`
	Scopes    []string `json:"scopes"`
	ExpiresIn string   `json:"expires_in,omitempty"` // Go duration (e.g. "720h"); empty = never
}

func (s *Server) apiKeysCreate(w http.ResponseWriter, r *http.Request) {
	var req createAPIKeyReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		http.Error(w, `{"error":"body must be {\"name\":\"...\", \"scopes\":[...]}"}`, http.StatusBadRequest)
		return
	}
	// Reject duplicate names — avoids "which prod key is mine?" confusion.
	existing, err := s.APIKeys.ListAPIKeys()
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}
	for _, k := range existing {
		if k.Name == req.Name {
			http.Error(w, `{"error":"a key with that name already exists — pick a unique name or revoke the old one"}`, http.StatusConflict)
			return
		}
	}
	scopes := ParseScopes(req.Scopes)
	if len(scopes) == 0 {
		scopes = []Scope{ScopeRead}
	}
	var expiresAt *time.Time
	if req.ExpiresIn != "" {
		d, err := time.ParseDuration(req.ExpiresIn)
		if err != nil || d <= 0 {
			http.Error(w, `{"error":"expires_in must be a positive Go duration like \"720h\" or empty for never"}`, http.StatusBadRequest)
			return
		}
		t := time.Now().UTC().Add(d)
		expiresAt = &t
	}
	raw, err := generateRawKey()
	if err != nil {
		http.Error(w, `{"error":"rng failure"}`, http.StatusInternalServerError)
		return
	}
	rec := APIKeyRecord{
		ID:        newULID(),
		Name:      req.Name,
		Hash:      hashAPIKey(raw),
		Prefix:    raw[:12],
		Scopes:    ScopesToStrings(scopes),
		CreatedAt: time.Now().UTC(),
		ExpiresAt: expiresAt,
	}
	if err := s.APIKeys.PutAPIKey(rec); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}
	s.InvalidateAPIKeyCache()
	logger.L.Info().Str("id", rec.ID).Str("name", rec.Name).Strs("scopes", rec.Scopes).Msg("admin: API key created")
	s.audit(r, "apikey.create", rec.ID, rec.Name)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":         rec.ID,
		"name":       rec.Name,
		"prefix":     rec.Prefix,
		"scopes":     rec.Scopes,
		"expires_at": rec.ExpiresAt,
		"key":        raw,
		"note":       "store this key now; it will never be shown again",
	})
}

func (s *Server) apiKeysGet(w http.ResponseWriter, _ *http.Request, id string) {
	k, err := s.APIKeys.GetAPIKey(id)
	if err != nil {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":           k.ID,
		"name":         k.Name,
		"prefix":       k.Prefix,
		"scopes":       k.Scopes,
		"disabled":     k.Disabled,
		"created_at":   k.CreatedAt,
		"last_used_at": k.LastUsedAt,
		"expires_at":   k.ExpiresAt,
	})
}

type patchAPIKeyReq struct {
	Disabled *bool `json:"disabled,omitempty"`
}

func (s *Server) apiKeysPatch(w http.ResponseWriter, r *http.Request, id string) {
	var req patchAPIKeyReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"bad body"}`, http.StatusBadRequest)
		return
	}
	if req.Disabled == nil {
		http.Error(w, `{"error":"body must include disabled:bool"}`, http.StatusBadRequest)
		return
	}
	if err := s.APIKeys.SetAPIKeyDisabled(id, *req.Disabled); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadGateway)
		return
	}
	s.InvalidateAPIKeyCache()
	logger.L.Info().Str("id", id).Bool("disabled", *req.Disabled).Msg("admin: API key patched")
	action := "apikey.enable"
	if *req.Disabled {
		action = "apikey.disable"
	}
	s.audit(r, action, id, "")
	_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "disabled": *req.Disabled})
}

func (s *Server) apiKeysDelete(w http.ResponseWriter, r *http.Request, id string) {
	if err := s.APIKeys.DeleteAPIKey(id); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadGateway)
		return
	}
	s.InvalidateAPIKeyCache()
	logger.L.Info().Str("id", id).Msg("admin: API key revoked")
	s.audit(r, "apikey.revoke", id, "")
	_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "deleted": true})
}

// handleAccountAction dispatches /accounts/{id}/pause and /accounts/{id}/resume.
func (s *Server) handleAccountAction(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !s.UIEnabled {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST required"}`, http.StatusMethodNotAllowed)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/accounts/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 {
		http.Error(w, `{"error":"use /accounts/{id}/pause or /accounts/{id}/resume"}`, http.StatusBadRequest)
		return
	}
	id, action := parts[0], parts[1]
	if action == "deploy" {
		s.handleAccountDeploy(w, r, id)
		return
	}
	var paused bool
	switch action {
	case "pause":
		paused = true
	case "resume":
		paused = false
	default:
		http.Error(w, `{"error":"unknown action"}`, http.StatusBadRequest)
		return
	}
	n := s.Pool.SetAccountPaused(id, paused)
	logger.L.Info().Str("account", id).Bool("paused", paused).Int("workers", n).Msg("admin: account mass-toggle")
	auditAction := "account.resume"
	if paused {
		auditAction = "account.pause"
	}
	s.audit(r, auditAction, id, fmt.Sprintf("%d workers", n))
	_ = json.NewEncoder(w).Encode(map[string]any{"account": id, "paused": paused, "affected": n})
}

// handleAccountDeploy deploys `count` additional workers on an existing
// account using the stored token — no re-auth required. POST body is
// `{count: int}`; missing/0 falls back to cfg.Worker.Count.
func (s *Server) handleAccountDeploy(w http.ResponseWriter, r *http.Request, id string) {
	if s.DeployMoreFunc == nil {
		http.Error(w, `{"error":"deploy hook not wired"}`, http.StatusServiceUnavailable)
		return
	}
	var body struct {
		Count int `json:"count"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	ctx, cancel := context.WithTimeout(r.Context(), TokenOpTimeoutD)
	defer cancel()
	names, err := s.DeployMoreFunc(ctx, id, body.Count)
	if err != nil {
		s.audit(r, "account.deploy.failed", id, err.Error())
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}
	s.audit(r, "account.deploy", id, fmt.Sprintf("%d workers", len(names)))
	_ = json.NewEncoder(w).Encode(map[string]any{"account": id, "deployed_workers": names})
}

// handleMetricsSeries serves the in-memory ring buffer of per-minute
// counter snapshots. Frontend chart reads this.
func (s *Server) handleMetricsSeries(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !s.UIEnabled {
		http.NotFound(w, r)
		return
	}
	// The series lives in the metrics package. Pull via accessor to avoid
	// admin → metrics direct coupling beyond the already-imported package.
	if s.SeriesFunc == nil {
		http.Error(w, `{"error":"series not initialized"}`, http.StatusServiceUnavailable)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"samples": s.SeriesFunc()})
}

// --- helpers ---

func generateRawKey() (string, error) {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	// url-safe base64 without padding → 32 chars from 24 bytes
	return "flx_" + base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// newULID returns a monotonic-ish unique 26-char ID. Hand-rolled to avoid
// adding oklog/ulid as a dep just for this. Time-prefixed so keys sort
// chronologically in bbolt's byte-ordered cursor.
func newULID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	ts := time.Now().UTC().UnixMilli()
	b[0] = byte(ts >> 40)
	b[1] = byte(ts >> 32)
	b[2] = byte(ts >> 24)
	b[3] = byte(ts >> 16)
	b[4] = byte(ts >> 8)
	b[5] = byte(ts)
	return strings.ToLower(hexNoDash(b[:]))
}

func hexNoDash(b []byte) string {
	const alpha = "0123456789abcdefghijklmnop" // crockford-ish, 26 chars total
	out := make([]byte, 26)
	for i := range out {
		out[i] = alpha[int(b[i%len(b)])%26]
	}
	return string(out)
}
