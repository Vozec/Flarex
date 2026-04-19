package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// ListedWorker is the richer worker-inventory row surfaced by GET /workers.
// Mirrors what `flarex list` prints locally: backend + hostname come from
// the deploy backend (workers_dev or custom_domain), not from the in-memory
// pool which only has url + name.
type ListedWorker struct {
	Name      string `json:"name"`
	URL       string `json:"url"`
	Account   string `json:"account"`
	Backend   string `json:"backend,omitempty"`
	Hostname  string `json:"hostname,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
}

// CleanDNSRecord describes a DNS record touched (or previewed) by the
// prefix-scoped clean operation. Zone is the parent zone name.
type CleanDNSRecord struct {
	Zone string `json:"zone"`
	Type string `json:"type"`
	Name string `json:"name"`
	ID   string `json:"id,omitempty"`
}

// cleanResp is the JSON shape returned by POST /workers/clean. Lists what
// was (or would be) deleted — worker names + DNS rows — plus the dry-run
// flag for the caller to render clearly.
type cleanResp struct {
	DryRun  bool             `json:"dry_run"`
	Workers []string         `json:"workers"`
	DNS     []CleanDNSRecord `json:"dns_records"`
	Error   string           `json:"error,omitempty"`
}

// handleWorkersRoot dispatches /workers (exact — no trailing slash).
//   - GET    → list every worker known to the deploy backends.
//   - DELETE → destroy every worker across all configured accounts.
//     Requires ?confirm=true to avoid fat-finger mass-deletion.
func (s *Server) handleWorkersRoot(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.Method {
	case http.MethodGet:
		s.handleWorkersList(w, r)
	case http.MethodDelete:
		s.handleWorkersDestroyAll(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
		_, _ = w.Write([]byte(`{"error":"GET or DELETE required"}`))
	}
}

func (s *Server) handleWorkersList(w http.ResponseWriter, r *http.Request) {
	if s.ListWorkersFunc == nil {
		http.Error(w, `{"error":"list workers disabled"}`, http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), TokenOpTimeoutD)
	defer cancel()
	ws, err := s.ListWorkersFunc(ctx)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadGateway)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"workers": ws})
}

func (s *Server) handleWorkersDestroyAll(w http.ResponseWriter, r *http.Request) {
	if s.DestroyAllFunc == nil {
		http.Error(w, `{"error":"destroy disabled"}`, http.StatusServiceUnavailable)
		return
	}
	if r.URL.Query().Get("confirm") != "true" {
		http.Error(w, `{"error":"add ?confirm=true to destroy every worker"}`, http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), TokenOpTimeoutD)
	defer cancel()
	names, err := s.DestroyAllFunc(ctx)
	if err != nil {
		s.audit(r, "workers.destroy_all.failed", "", err.Error())
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadGateway)
		return
	}
	s.audit(r, "workers.destroy_all", "", fmt.Sprintf("%d workers", len(names)))
	_ = json.NewEncoder(w).Encode(map[string]any{"removed_workers": names})
}

// handleWorkersDeploy is POST /workers/deploy. Body: {account, count}.
// Empty account → deploy on every configured account (uses DeployMoreFunc
// per id). Count defaults to worker.count.
func (s *Server) handleWorkersDeploy(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		_, _ = w.Write([]byte(`{"error":"POST required"}`))
		return
	}
	if s.DeployMoreFunc == nil {
		http.Error(w, `{"error":"deploy disabled"}`, http.StatusServiceUnavailable)
		return
	}
	var body struct {
		Account string `json:"account"`
		Count   int    `json:"count"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if strings.TrimSpace(body.Account) == "" {
		http.Error(w, `{"error":"body must include {\"account\":\"<id>\",\"count\":N}"}`, http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), TokenOpTimeoutD)
	defer cancel()
	names, err := s.DeployMoreFunc(ctx, body.Account, body.Count)
	if err != nil {
		s.audit(r, "workers.deploy.failed", body.Account, err.Error())
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadGateway)
		return
	}
	s.audit(r, "workers.deploy", body.Account, fmt.Sprintf("%d workers", len(names)))
	_ = json.NewEncoder(w).Encode(map[string]any{
		"account":          body.Account,
		"deployed_workers": names,
	})
}

// handleWorkersClean is POST /workers/clean. Body: {dry_run}. Runs the
// prefix-scoped worker + DNS purge — mirror of `flarex clean`. Safety
// guards (empty/short prefix, silent default) are enforced inside CleanFunc
// by the caller's wiring, not here.
func (s *Server) handleWorkersClean(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		_, _ = w.Write([]byte(`{"error":"POST required"}`))
		return
	}
	if s.CleanFunc == nil {
		http.Error(w, `{"error":"clean disabled"}`, http.StatusServiceUnavailable)
		return
	}
	var body struct {
		DryRun bool `json:"dry_run"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	ctx, cancel := context.WithTimeout(r.Context(), TokenOpTimeoutD)
	defer cancel()
	workers, dns, err := s.CleanFunc(ctx, body.DryRun)
	if err != nil {
		s.audit(r, "workers.clean.failed", "", err.Error())
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(cleanResp{DryRun: body.DryRun, Error: err.Error()})
		return
	}
	if !body.DryRun {
		s.audit(r, "workers.clean", "",
			fmt.Sprintf("%d workers %d dns", len(workers), len(dns)))
	}
	_ = json.NewEncoder(w).Encode(cleanResp{
		DryRun:  body.DryRun,
		Workers: workers,
		DNS:     dns,
	})
}
