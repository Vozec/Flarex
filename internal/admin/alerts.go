package admin

import (
	"encoding/json"
	"net/http"
)

// alertmanagerPayload models the Alertmanager v4 webhook envelope. We
// only consume the fields we forward; other keys are preserved as-is on
// the wire but not needed in Go.
type alertmanagerPayload struct {
	Receiver string              `json:"receiver"`
	Status   string              `json:"status"`
	Alerts   []alertmanagerAlert `json:"alerts"`
}

type alertmanagerAlert struct {
	Status      string            `json:"status"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
}

// handleAlertsWebhook accepts an Alertmanager webhook payload and fires
// each contained alert through the registered AlertHook (which in turn
// hits the same sinks as quota/worker-down alerts). Read scope is enough
// — the forwarder only reads-then-writes-to-sinks, no mutation of pool
// state.
func (s *Server) handleAlertsWebhook(w http.ResponseWriter, r *http.Request) {
	if !methodPOST(w, r) {
		return
	}
	if s.AlertHook == nil {
		http.Error(w, "alert hook not wired", http.StatusServiceUnavailable)
		return
	}
	var p alertmanagerPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	n := 0
	for _, a := range p.Alerts {
		summary := firstNonEmpty(a.Annotations["summary"], a.Annotations["description"], a.Labels["alertname"], "(no summary)")
		severity := firstNonEmpty(a.Labels["severity"], "info")
		status := firstNonEmpty(a.Status, p.Status, "firing")
		source := firstNonEmpty(p.Receiver, "alertmanager")
		s.AlertHook(r.Context(), source, summary, severity, status)
		n++
	}
	s.audit(r, "alertmanager.webhook", p.Receiver, "")
	writeJSON(w, http.StatusOK, map[string]any{"received": n})
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}
