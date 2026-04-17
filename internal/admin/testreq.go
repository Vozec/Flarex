package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// handleTestRequest routes a user-supplied URL through the current proxy
// pool and returns a structured report (worker, colo, egress IP, latency,
// status, truncated body). POST body: {"url":"https://example.com"}.
func (s *Server) handleTestRequest(w http.ResponseWriter, r *http.Request) {
	if !methodPOST(w, r) {
		return
	}
	if s.TestRequestFunc == nil {
		http.Error(w, "test request not wired", http.StatusServiceUnavailable)
		return
	}
	var body struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	body.URL = strings.TrimSpace(body.URL)
	if body.URL == "" {
		http.Error(w, "url required", http.StatusBadRequest)
		return
	}
	u, err := url.Parse(body.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		http.Error(w, "url must be an absolute http(s) URL", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	res, err := s.TestRequestFunc(ctx, body.URL)
	if err != nil {
		s.audit(r, "test.request.failed", body.URL, err.Error())
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	s.audit(r, "test.request", body.URL, res.Worker)
	if s.TestHistory != nil {
		entry := map[string]any{"at": time.Now().UTC(), "url": body.URL, "result": res}
		if raw, err := json.Marshal(entry); err == nil {
			_ = s.TestHistory.PutTestHistory(time.Now().UTC(), raw)
		}
	}
	writeJSON(w, http.StatusOK, res)
}

// handleTestHistory returns the persisted /test-request runs, newest
// first. GET only. Read-only, so `read` scope suffices.
func (s *Server) handleTestHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.TestHistory == nil {
		writeJSON(w, http.StatusOK, map[string]any{"runs": []any{}})
		return
	}
	raws, err := s.TestHistory.ListTestHistory(100)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	runs := make([]json.RawMessage, 0, len(raws))
	for _, raw := range raws {
		runs = append(runs, raw)
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

// handleProxyMode swaps pool.proxy_mode at runtime. POST body:
// {"mode":"socket|fetch|hybrid"}.
func (s *Server) handleProxyMode(w http.ResponseWriter, r *http.Request) {
	if !methodPOST(w, r) {
		return
	}
	if s.SetProxyModeFunc == nil {
		http.Error(w, "proxy-mode switch not wired", http.StatusServiceUnavailable)
		return
	}
	var body struct {
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	switch body.Mode {
	case "socket", "fetch", "hybrid":
	default:
		http.Error(w, "mode must be one of: socket, fetch, hybrid", http.StatusBadRequest)
		return
	}
	if err := s.SetProxyModeFunc(body.Mode); err != nil {
		s.audit(r, "proxy.mode.failed", body.Mode, err.Error())
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.audit(r, "proxy.mode", body.Mode, "")
	writeJSON(w, http.StatusOK, map[string]any{"mode": body.Mode})
}

