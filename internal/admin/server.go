package admin

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/pprof"
	"strconv"
	"strings"
	"sync"
	"time"

	vm "github.com/VictoriaMetrics/metrics"
	"github.com/Vozec/flarex/internal/logger"
	"github.com/Vozec/flarex/internal/pool"
)

//go:embed ui.html
var uiHTML []byte

var (
	ShutdownTimeoutD = 2 * time.Second
	TokenOpTimeoutD  = 120 * time.Second
)

type Server struct {
	Addr        string
	Pool        *pool.Pool
	Token       string
	APIKey      string
	BasicUser   string
	BasicPass   string
	EnablePprof bool

	// TOTPSecret (base32) enables 2FA on POST /session. Empty = disabled.
	TOTPSecret string

	// apiKeyCache is a hash→record index rebuilt on demand to avoid
	// O(n) scans of the bbolt store on every authenticated request.
	apiKeyCache   map[string]APIKeyRecord
	apiKeyCacheMu sync.RWMutex

	AddTokenFunc func(ctx context.Context, token string, count int) ([]string, error)

	RemoveTokenFunc func(ctx context.Context, accountID, token string) ([]string, error)

	// LogTailFunc opens a live log tail for the named Worker.
	// Returns a message channel + cleanup. Channel closes when tail ends.
	LogTailFunc func(ctx context.Context, workerName string) (<-chan []byte, func(), error)

	// QuotaHistoryFunc returns persisted quota snapshots. Caller (admin UI /
	// shell) can narrow with ?days=N&account=<id>.
	QuotaHistoryFunc func(days int, accountID string) ([]any, error)

	// ConfigDumpFunc returns a sanitized JSON dump of the running config
	// (secrets redacted). Used by the admin UI Config tab.
	ConfigDumpFunc func() map[string]any

	// RecycleWorkerFunc triggers a graceful recycle of the named worker
	// (drain + redeploy). Returns the new worker name.
	RecycleWorkerFunc func(ctx context.Context, workerName string) (string, error)

	// APIKeys is the bbolt-backed key registry. Optional — when nil, the
	// /apikeys endpoints return 503.
	APIKeys APIKeyStore

	// Audit records admin mutations ({who, action, target}). Optional —
	// when nil, audit writes are silently dropped but endpoints still work.
	Audit AuditLog

	// UIEnabled gates the React SPA + its supporting endpoints
	// (/ui/*, /apikeys, /accounts/{id}/pause|resume, /metrics/series).
	// Set from cfg.Admin.UI.
	UIEnabled bool

	// SeriesFunc returns the current metrics ring-buffer snapshot (1-min
	// samples, up to 60 entries). Wired to metrics.DefaultSeries.Snapshot
	// from main.
	SeriesFunc func() any

	// TestRequestFunc routes an HTTP GET through the current pool and
	// returns {egress, colo, worker, status, latency, body}. Used by the
	// admin UI's "Test request" tab. nil = endpoint returns 503.
	TestRequestFunc func(ctx context.Context, targetURL string) (TestRequestResult, error)

	// SetProxyModeFunc atomically swaps pool.proxy_mode at runtime. Used
	// by the admin UI's proxy-mode pill. nil = endpoint returns 503.
	SetProxyModeFunc func(mode string) error

	// TestHistory persists the last N /test-request runs for replay in
	// the admin UI. Optional — nil disables history persistence.
	TestHistory TestHistoryStore

	// AlertHook is called by POST /alerts/webhook for each alert in the
	// Alertmanager payload. Main wires it to the alerts.Dispatcher so the
	// same sinks (Discord, HTTP webhooks) receive external alerts.
	AlertHook func(ctx context.Context, source, summary, severity, status string)

	// AccountNamesFunc returns the current account ID → human name map
	// (CF account display name from discovery). Used by /accounts to show
	// "Arthur's Account" instead of "90ad2c12…" in the admin UI.
	AccountNamesFunc func() map[string]string

	// DeployMoreFunc deploys `count` additional workers on an account we
	// already know the token for (loaded from cfg.Accounts). Lets the UI
	// scale a pool without re-asking for the token on every request.
	DeployMoreFunc func(ctx context.Context, accountID string, count int) ([]string, error)

	// UpdateConfigFunc applies a runtime config change. path uses dot
	// notation ("pool.proxy_mode", "filter.allow_ports"). Returns whether
	// the change was applied live vs. will require restart. Errors include
	// unknown path or type mismatch.
	UpdateConfigFunc func(path string, value any) (applied bool, requiresRestart bool, err error)

	// sessionSecret signs the HTTP session cookie + CSRF token. Set via
	// Server.SessionSecret() at boot. nil = session/CSRF disabled, SPA
	// falls back to X-API-Key header auth.
	sessionSecret []byte
}

// TestRequestResult is the response shape of POST /test-request. All
// network-facing info the caller needs to assess "did the proxy work?".
type TestRequestResult struct {
	Worker       string            `json:"worker"`
	Colo         string            `json:"colo,omitempty"`
	EgressIP     string            `json:"egress_ip,omitempty"`
	Status       int               `json:"status"`
	LatencyMs    int64             `json:"latency_ms"`
	Mode         string            `json:"mode"`
	Headers      map[string]string `json:"headers,omitempty"`
	Body         string            `json:"body"`
	BodyTruncAt  int               `json:"body_trunc_at,omitempty"`
	ResolvedHost string            `json:"resolved_host,omitempty"`
}

// TestHistoryStore persists test-request runs for replay. Tiny interface so
// admin doesn't import state.
type TestHistoryStore interface {
	PutTestHistory(at time.Time, raw []byte) error
	ListTestHistory(limit int) ([][]byte, error)
}

// AuditLog records admin-side mutations. Implementation lives in
// internal/state; admin only needs Put + List. Whom is derived from the
// request's authenticated identity.
type AuditLog interface {
	PutAudit(AuditRecord) error
	ListAudit(limit int) ([]AuditRecord, error)
}

// AuditRecord is the admin-package view of state.AuditEvent. Duplicated
// across the package boundary so admin doesn't import state.
type AuditRecord struct {
	At     time.Time `json:"at"`
	Who    string    `json:"who"`
	Action string    `json:"action"`
	Target string    `json:"target"`
	Detail string    `json:"detail,omitempty"`
}

// APIKeyStore is the minimum surface admin needs from internal/state.
// Kept as an interface so tests can stub it and we don't import state
// from admin (avoid cycles).
type APIKeyStore interface {
	PutAPIKey(k APIKeyRecord) error
	ListAPIKeys() ([]APIKeyRecord, error)
	GetAPIKey(id string) (APIKeyRecord, error)
	DeleteAPIKey(id string) error
	MarkAPIKeyUsed(hash string)
	SetAPIKeyDisabled(id string, disabled bool) error
}

// APIKeyRecord mirrors state.APIKey — duplicated here so admin doesn't
// import state. Main wires a thin adapter between them.
type APIKeyRecord struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Hash       string     `json:"hash"`
	Prefix     string     `json:"prefix"`
	Scopes     []string   `json:"scopes"`
	Disabled   bool       `json:"disabled"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt time.Time  `json:"last_used_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}

func (s *Server) Serve(ctx context.Context) error {
	mux := http.NewServeMux()

	mux.HandleFunc("/metrics", s.authed(func(w http.ResponseWriter, _ *http.Request) {
		vm.WritePrometheus(w, true)
	}))

	// Root handler. Behavior:
	//   - /          → redirect to /ui/ when admin.ui=true; else serve
	//                  the legacy single-file dashboard (backwards compat).
	//   - /<other>   → 404.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if s.UIEnabled {
			http.Redirect(w, r, "/ui/", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(uiHTML)
	})

	mux.HandleFunc("/status", s.authed(s.handleStatus))
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("/tokens", s.authed(s.handleTokens))
	mux.HandleFunc("/workers/", s.authed(s.handleWorkerSub))
	mux.HandleFunc("/metrics/history", s.authed(s.handleQuotaHistory))
	mux.HandleFunc("/metrics/series", s.authed(s.handleMetricsSeries))
	mux.HandleFunc("/accounts", s.authed(s.handleAccounts))
	mux.HandleFunc("/accounts/", s.authed(s.handleAccountAction))
	mux.HandleFunc("/config", s.authed(s.handleConfig))
	mux.HandleFunc("/apikeys", s.authed(s.handleAPIKeys))
	mux.HandleFunc("/apikeys/", s.authed(s.handleAPIKeys))
	mux.HandleFunc("/audit", s.authed(s.handleAudit))
	if s.UIEnabled {
		mux.HandleFunc("/test-request", s.authed(s.handleTestRequest))
		mux.HandleFunc("/test-history", s.authed(s.handleTestHistory))
		mux.HandleFunc("/config/proxy-mode", s.authed(s.handleProxyMode))
	}
	// Alertmanager webhook — gated behind the `alerts` scope substitute
	// (we reuse `write` since it's a mutation of external systems).
	mux.HandleFunc("/alerts/webhook", s.authed(s.handleAlertsWebhook))
	// /session is unauthenticated on POST (login) / DELETE (logout) but
	// records failed attempts to the brute-force limiter.
	mux.HandleFunc("/session", s.handleSession)
	if s.UIEnabled {
		mux.HandleFunc("/ui", s.handleSPA)
		mux.HandleFunc("/ui/", s.handleSPA)
	}

	if s.EnablePprof {
		mux.HandleFunc("/debug/pprof/", s.authed(pprof.Index))
		mux.HandleFunc("/debug/pprof/cmdline", s.authed(pprof.Cmdline))
		mux.HandleFunc("/debug/pprof/profile", s.authed(pprof.Profile))
		mux.HandleFunc("/debug/pprof/symbol", s.authed(pprof.Symbol))
		mux.HandleFunc("/debug/pprof/trace", s.authed(pprof.Trace))
	}

	srv := &http.Server{
		Addr:              s.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), ShutdownTimeoutD) //nolint:gosec // G118: shutdown must outlive request context
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	logger.L.Info().Str("addr", s.Addr).Bool("pprof", s.EnablePprof).Bool("ui", s.UIEnabled).Msg("admin HTTP listening")
	s.logSPAStatus()
	return srv.ListenAndServe()
}

func (s *Server) authed(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// No auth configured at all → wide-open (loopback-only assumed).
		if s.noAuthConfigured() {
			r = r.WithContext(contextWithWho(r.Context(), "anonymous"))
			h(w, r)
			return
		}
		scopes, who, ok := s.resolveScopes(r)
		if !ok {
			// Surface failed auth to the per-IP brute-force limiter so
			// noisy sources get 429'd. Silently dropped when rate limit
			// disabled or not configured.
			s.recordFailedAuth(r)
			// Only advertise Basic when actually accepted — otherwise
			// browsers pop up a credential dialog on any XHR 401 (admin
			// UI session expiry, bootstrap probe, etc.). Bearer doesn't
			// trigger the popup.
			if s.BasicUser != "" && s.BasicPass != "" {
				w.Header().Set("WWW-Authenticate", `Basic realm="flarex", charset="UTF-8"`)
			} else {
				w.Header().Set("WWW-Authenticate", `Bearer realm="flarex"`)
			}
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if s.isBlockedIP(r) {
			http.Error(w, "too many failed auth attempts — try again later", http.StatusTooManyRequests)
			return
		}
		need := RequiredScope(r.Method, r.URL.Path)
		if !HasScope(scopes, need) {
			http.Error(w, "forbidden (missing scope: "+string(need)+")", http.StatusForbidden)
			return
		}
		if !s.checkCSRF(r) {
			http.Error(w, "csrf token missing or invalid (mutations require X-CSRF-Token)", http.StatusForbidden)
			return
		}
		s.maybeRefreshSession(w, r, who)
		r = r.WithContext(contextWithWho(r.Context(), who))
		h(w, r)
	}
}

// maybeRefreshSession re-issues the session + CSRF cookies with a fresh
// TTL when the caller is within the last half of the current cookie's
// window. Rolling TTL means active users don't get logged out mid-task
// but idle sessions still expire within sessionTTL of last activity.
func (s *Server) maybeRefreshSession(w http.ResponseWriter, r *http.Request, who string) {
	if s.sessionSecret == nil {
		return
	}
	if !strings.HasPrefix(who, "session:") {
		return // Header-auth callers don't have a session cookie to refresh.
	}
	_, exp, ok := s.resolveSessionCookieWithExp(r)
	if !ok {
		return
	}
	ttl := sessionTTL
	remaining := time.Until(time.Unix(exp, 0))
	if remaining > ttl/2 {
		return
	}
	rawWho := strings.TrimPrefix(who, "session:")
	_, _ = s.issueSessionCookies(w, rawWho)
}

// issueSessionCookies writes a fresh flarex_session + flarex_csrf pair
// scoped to sessionTTL. Shared between POST /session and the rolling
// refresh path in authed(). Returns the expiry timestamp and the CSRF
// token so callers (POST /session) can echo them in the response body.
func (s *Server) issueSessionCookies(w http.ResponseWriter, who string) (int64, string) {
	exp := time.Now().Add(sessionTTL).Unix()
	whoEnc := base64URLEncode([]byte(who))
	expEnc := base64URLEncode([]byte(strconv.FormatInt(exp, 10)))
	payload := whoEnc + "." + expEnc
	sig := hmacSign(s.sessionSecret, payload)
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: payload + "." + sig,
		Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode,
		MaxAge: int(sessionTTL.Seconds()),
	})
	csrf := hmacSign(s.sessionSecret, "csrf:"+payload)
	http.SetCookie(w, &http.Cookie{
		Name: csrfCookieName, Value: csrf,
		Path: "/", HttpOnly: false, SameSite: http.SameSiteStrictMode,
		MaxAge: int(sessionTTL.Seconds()),
	})
	return exp, csrf
}

// handleAudit returns the most recent admin audit entries. ?limit=N
// (default 200, max 5000). Requires `read` scope (set via RequiredScope).
func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if s.Audit == nil {
		http.Error(w, `{"error":"audit log disabled"}`, http.StatusServiceUnavailable)
		return
	}
	limit := 200
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 5000 {
			limit = n
		}
	}
	rows, err := s.Audit.ListAudit(limit)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"events": rows})
}

func contextWithWho(ctx context.Context, who string) context.Context {
	return context.WithValue(ctx, whoKey{}, who)
}

func (s *Server) noAuthConfigured() bool {
	return s.Token == "" && s.APIKey == "" && (s.BasicUser == "" || s.BasicPass == "")
}

// whoKey is the context key used to smuggle the authenticated identity
// from authed() down to handlers that want to record an audit line.
type whoKey struct{}

// audit writes one line to the configured AuditLog, best-effort — failures
// don't block the request. The "who" string is extracted from context;
// falls back to "unknown" outside of an authed handler. Target + detail are
// passed through redactURL so credentials in query strings never land in
// the audit bucket.
func (s *Server) audit(r *http.Request, action, target, detail string) {
	if s.Audit == nil {
		return
	}
	who := "unknown"
	if v := r.Context().Value(whoKey{}); v != nil {
		if str, ok := v.(string); ok {
			who = str
		}
	}
	_ = s.Audit.PutAudit(AuditRecord{
		At: time.Now().UTC(), Who: who, Action: action,
		Target: redactURL(target), Detail: redactURL(detail),
	})
}

// redactSensitiveKeys lists query-string param names whose values are
// always masked in audit logs. Kept lowercase; match is case-insensitive.
var redactSensitiveKeys = []string{"api_key", "apikey", "token", "secret", "password", "passwd", "access_token", "refresh_token", "x-api-key"}

// redactURL replaces sensitive query parameters with "****". Works on
// absolute URLs and ?-only fragments ("target=https://x?token=foo" or
// just "token=foo&user=bar"). Non-URL strings pass through unchanged.
func redactURL(s string) string {
	if s == "" {
		return s
	}
	// Fast reject: no '?' and no '=' → nothing to redact.
	if !strings.ContainsAny(s, "?=") {
		return s
	}
	// Find query component. For absolute URLs keep the prefix intact.
	prefix := ""
	q := s
	if i := strings.IndexByte(s, '?'); i >= 0 {
		prefix = s[:i+1]
		q = s[i+1:]
	}
	// Split on & and redact targeted keys. Preserve order.
	parts := strings.Split(q, "&")
	for i, p := range parts {
		eq := strings.IndexByte(p, '=')
		if eq <= 0 {
			continue
		}
		k := strings.ToLower(p[:eq])
		for _, sk := range redactSensitiveKeys {
			if k == sk {
				parts[i] = p[:eq+1] + "****"
				break
			}
		}
	}
	return prefix + strings.Join(parts, "&")
}

// resolveScopes validates the request's credentials and returns the scope
// set they grant. The second return is false when no valid credential is
// presented.
//
// Order of checks:
//  1. Bootstrap static credentials from config (admin.token/api_key/basic_*)
//     → all scopes.
//  2. Dynamic API key from the registry (hashed lookup) → its recorded
//     scopes; updates LastUsedAt.
func (s *Server) resolveScopes(r *http.Request) ([]Scope, string, bool) {
	authHdr := r.Header.Get("Authorization")
	apiKeyHdr := r.Header.Get("X-API-Key")

	// Session cookie (set by POST /session). Checked first because UIs use it.
	if who, ok := s.resolveSessionCookie(r); ok {
		return AllScopes, who, true
	}

	if s.Token != "" && authHdr == "Bearer "+s.Token {
		return AllScopes, "bootstrap:token", true
	}
	if s.APIKey != "" {
		if apiKeyHdr == s.APIKey || authHdr == "ApiKey "+s.APIKey {
			return AllScopes, "bootstrap:api_key", true
		}
	}
	if s.BasicUser != "" && s.BasicPass != "" {
		u, p, ok := r.BasicAuth()
		if ok && u == s.BasicUser && p == s.BasicPass {
			return AllScopes, "bootstrap:basic:" + u, true
		}
	}
	// Dynamic registry lookup: client may send raw key via X-API-Key or
	// Authorization: Bearer <raw>. Hash it, look up via in-memory cache
	// (O(1) instead of scanning all keys from bbolt on every request).
	if s.APIKeys != nil {
		raw := apiKeyHdr
		if raw == "" && strings.HasPrefix(authHdr, "Bearer ") {
			raw = authHdr[len("Bearer "):]
		}
		if raw != "" {
			h := hashAPIKey(raw)
			if k, ok := s.lookupAPIKeyByHash(h); ok {
				if k.Disabled {
					return nil, "", false
				}
				if k.ExpiresAt != nil && time.Now().After(*k.ExpiresAt) {
					return nil, "", false
				}
				go s.APIKeys.MarkAPIKeyUsed(h)
				scopes := make([]Scope, 0, len(k.Scopes))
				for _, ss := range k.Scopes {
					scopes = append(scopes, Scope(ss))
				}
				return scopes, "apikey:" + k.Name, true
			}
		}
	}
	return nil, "", false
}

// lookupAPIKeyByHash returns a cached API key record by its hash.
// The cache is lazily built from the store and invalidated by
// InvalidateAPIKeyCache (called from mutation handlers).
func (s *Server) lookupAPIKeyByHash(hash string) (APIKeyRecord, bool) {
	s.apiKeyCacheMu.RLock()
	if s.apiKeyCache != nil {
		k, ok := s.apiKeyCache[hash]
		s.apiKeyCacheMu.RUnlock()
		return k, ok
	}
	s.apiKeyCacheMu.RUnlock()

	// Cache miss: rebuild from store.
	s.apiKeyCacheMu.Lock()
	defer s.apiKeyCacheMu.Unlock()
	// Double-check after acquiring write lock.
	if s.apiKeyCache != nil {
		k, ok := s.apiKeyCache[hash]
		return k, ok
	}
	s.rebuildAPIKeyCacheLocked()
	k, ok := s.apiKeyCache[hash]
	return k, ok
}

// InvalidateAPIKeyCache clears the in-memory hash index so it is
// rebuilt on the next lookup. Call after any API key mutation.
func (s *Server) InvalidateAPIKeyCache() {
	s.apiKeyCacheMu.Lock()
	s.apiKeyCache = nil
	s.apiKeyCacheMu.Unlock()
}

func (s *Server) rebuildAPIKeyCacheLocked() {
	s.apiKeyCache = make(map[string]APIKeyRecord)
	keys, err := s.APIKeys.ListAPIKeys()
	if err != nil {
		return
	}
	for _, k := range keys {
		s.apiKeyCache[k.Hash] = k
	}
}

// resolveSessionCookie validates the HMAC-signed flarex_session cookie set
// by POST /session. Returns the "who" (session:<api_key_name>) on success,
// plus the expiry so authed() can refresh the cookie when halfway through
// its TTL (rolling session).
func (s *Server) resolveSessionCookie(r *http.Request) (string, bool) {
	who, _, ok := s.resolveSessionCookieWithExp(r)
	return who, ok
}

func (s *Server) resolveSessionCookieWithExp(r *http.Request) (string, int64, bool) {
	c, err := r.Cookie(sessionCookieName)
	if err != nil || c.Value == "" || s.sessionSecret == nil {
		return "", 0, false
	}
	parts := strings.SplitN(c.Value, ".", 3)
	if len(parts) != 3 {
		return "", 0, false
	}
	payload := parts[0] + "." + parts[1]
	mac := hmacSign(s.sessionSecret, payload)
	if !hmacEqual(mac, parts[2]) {
		return "", 0, false
	}
	who, err := base64URLDecode(parts[0])
	if err != nil {
		return "", 0, false
	}
	exp, err := base64URLDecode(parts[1])
	if err != nil {
		return "", 0, false
	}
	expTs, err := strconv.ParseInt(string(exp), 10, 64)
	if err != nil || time.Now().Unix() > expTs {
		return "", 0, false
	}
	return "session:" + string(who), expTs, true
}

// hashAPIKey returns the sha256 hex of a raw API key. Centralized here so
// admin.go and internal/state stay in sync on the hash algorithm.
func hashAPIKey(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

type workerStatus struct {
	Name        string  `json:"name"`
	URL         string  `json:"url"`
	Account     string  `json:"account"`
	Healthy     bool    `json:"healthy"`
	QuotaPaused bool    `json:"quota_paused"`
	Breaker     string  `json:"breaker"`
	Inflight    int64   `json:"inflight"`
	Requests    uint64  `json:"requests"`
	Errors      uint64  `json:"errors"`
	ErrRate     float64 `json:"err_rate_ewma"`
	AgeSec      int64   `json:"age_sec"`
	Colo        string  `json:"colo,omitempty"`
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	out := make([]workerStatus, 0, s.Pool.Size())
	for _, wk := range s.Pool.All() {
		out = append(out, workerStatus{
			Name:        wk.Name,
			URL:         wk.URL,
			Account:     wk.AccountID,
			Healthy:     wk.Healthy.Load(),
			QuotaPaused: wk.QuotaPaused.Load(),
			Breaker:     wk.BreakerState(),
			Inflight:    wk.Inflight.Load(),
			Requests:    wk.Requests.Load(),
			Errors:      wk.Errors.Load(),
			ErrRate:     wk.EWMAErrRate(),
			AgeSec:      int64(time.Since(wk.CreatedAt).Seconds()),
			Colo:        wk.ColoString(),
		})
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"pool_size": s.Pool.Size(),
		"workers":   out,
	})
}

var _ = fmt.Sprintf

type addTokenReq struct {
	Token string `json:"token"`
	Count int    `json:"count,omitempty"` // 0 = use server's worker.count
}

type tokenResp struct {
	Deployed []string `json:"deployed_workers,omitempty"`
	Removed  []string `json:"removed_workers,omitempty"`
	Error    string   `json:"error,omitempty"`
}

func (s *Server) handleTokens(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.Method {
	case http.MethodPost:
		s.handleAddToken(w, r)
	case http.MethodDelete:
		s.handleRemoveToken(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(tokenResp{Error: "POST or DELETE required"})
	}
}

func (s *Server) handleAddToken(w http.ResponseWriter, r *http.Request) {
	if s.AddTokenFunc == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(tokenResp{Error: "runtime token add disabled"})
		return
	}
	var req addTokenReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Token == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(tokenResp{Error: "body must be {\"token\":\"...\"}"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), TokenOpTimeoutD)
	defer cancel()
	names, err := s.AddTokenFunc(ctx, req.Token, req.Count)
	if err != nil {
		logger.L.Warn().Err(err).Msg("admin: add token failed")
		s.audit(r, "token.add.failed", "", err.Error())
		// Map known error categories to specific HTTP statuses so the UI
		// can render distinct toasts and the CLI gets a meaningful exit
		// code. Anything else falls through to 502 (upstream-ish).
		status := http.StatusBadGateway
		msg := err.Error()
		switch {
		case strings.Contains(msg, "already registered"):
			status = http.StatusConflict
		case strings.Contains(msg, "Invalid access token") || strings.Contains(msg, "token validation failed") || strings.Contains(msg, "needs Account.Workers"):
			status = http.StatusUnauthorized
		case strings.Contains(msg, "is empty"):
			status = http.StatusBadRequest
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(tokenResp{Error: err.Error()})
		return
	}
	s.audit(r, "token.add", strings.Join(names, ","), fmt.Sprintf("count=%d", req.Count))
	_ = json.NewEncoder(w).Encode(tokenResp{Deployed: names})
}

// handleAccounts aggregates pool workers by account and returns one row per
// account with {id, worker_count, healthy, quota_paused}. Used by the admin
// UI "Accounts" tab to give an at-a-glance view + targets for DELETE.
func (s *Server) handleAccounts(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	type row struct {
		ID          string `json:"id"`
		Name        string `json:"name,omitempty"`
		Workers     int    `json:"workers"`
		Healthy     int    `json:"healthy"`
		QuotaPaused int    `json:"quota_paused"`
	}
	var names map[string]string
	if s.AccountNamesFunc != nil {
		names = s.AccountNamesFunc()
	}
	acc := make(map[string]*row)
	for _, wk := range s.Pool.All() {
		r, ok := acc[wk.AccountID]
		if !ok {
			r = &row{ID: wk.AccountID, Name: names[wk.AccountID]}
			acc[wk.AccountID] = r
		}
		r.Workers++
		if wk.Healthy.Load() {
			r.Healthy++
		}
		if wk.QuotaPaused.Load() {
			r.QuotaPaused++
		}
	}
	out := make([]*row, 0, len(acc))
	for _, r := range acc {
		out = append(out, r)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"accounts": out})
}

// handleConfig dumps a sanitized view of the running config (secrets
// redacted). Invoked by the UI Config tab.
func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodPatch {
		s.handleConfigPatch(w, r)
		return
	}
	if s.ConfigDumpFunc == nil {
		http.Error(w, `{"error":"config dump disabled"}`, http.StatusServiceUnavailable)
		return
	}
	_ = json.NewEncoder(w).Encode(s.ConfigDumpFunc())
}

// handleConfigPatch receives {path, value} and calls the registered
// UpdateConfigFunc. Response indicates whether the change was live-applied
// or needs a restart to take effect.
func (s *Server) handleConfigPatch(w http.ResponseWriter, r *http.Request) {
	if s.UpdateConfigFunc == nil {
		http.Error(w, `{"error":"runtime config updates disabled"}`, http.StatusServiceUnavailable)
		return
	}
	var body struct {
		Path  string      `json:"path"`
		Value interface{} `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Path == "" {
		http.Error(w, `{"error":"body must be {\"path\":\"section.field\",\"value\":...}"}`, http.StatusBadRequest)
		return
	}
	applied, needsRestart, err := s.UpdateConfigFunc(body.Path, body.Value)
	if err != nil {
		s.audit(r, "config.update.failed", body.Path, err.Error())
		http.Error(w, fmt.Sprintf(`{"error":%q,"path":%q}`, err.Error(), body.Path), http.StatusBadRequest)
		return
	}
	s.audit(r, "config.update", body.Path, fmt.Sprintf("applied=%v restart=%v", applied, needsRestart))
	_ = json.NewEncoder(w).Encode(map[string]any{
		"path":             body.Path,
		"applied":          applied,
		"requires_restart": needsRestart,
	})
}

// handleWorkerRecycle triggers a graceful redeploy of a single worker.
// POST /workers/{name}/recycle. Returns the new worker name on success.
func (s *Server) handleWorkerRecycle(w http.ResponseWriter, r *http.Request, name string) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		_, _ = w.Write([]byte(`{"error":"POST required"}`))
		return
	}
	if s.RecycleWorkerFunc == nil {
		http.Error(w, `{"error":"recycle disabled"}`, http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), TokenOpTimeoutD)
	defer cancel()
	newName, err := s.RecycleWorkerFunc(ctx, name)
	if err != nil {
		s.audit(r, "worker.recycle.failed", name, err.Error())
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	s.audit(r, "worker.recycle", name, "→"+newName)
	_ = json.NewEncoder(w).Encode(map[string]string{"old": name, "new": newName})
}

func (s *Server) handleQuotaHistory(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if s.QuotaHistoryFunc == nil {
		http.Error(w, `{"error":"history not enabled"}`, http.StatusServiceUnavailable)
		return
	}
	days := 7
	if v := r.URL.Query().Get("days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 365 {
			days = n
		}
	}
	account := r.URL.Query().Get("account")
	out, err := s.QuotaHistoryFunc(days, account)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"days": days, "account": account, "series": out})
}

// handleWorkerSub dispatches /workers/{name}/logs → SSE log stream.
func (s *Server) handleWorkerSub(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/workers/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 {
		http.Error(w, "use /workers/{name}/logs", http.StatusBadRequest)
		return
	}
	name, sub := parts[0], parts[1]
	if sub == "recycle" {
		s.handleWorkerRecycle(w, r, name)
		return
	}
	if sub != "logs" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if s.LogTailFunc == nil {
		http.Error(w, "log tail disabled", http.StatusServiceUnavailable)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch, cleanup, err := s.LogTailFunc(r.Context(), name)
	if err != nil {
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", err.Error())
		flusher.Flush()
		return
	}
	defer cleanup()

	for {
		select {
		case <-r.Context().Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()
		}
	}
}

func (s *Server) handleRemoveToken(w http.ResponseWriter, r *http.Request) {
	if s.RemoveTokenFunc == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(tokenResp{Error: "runtime token remove disabled"})
		return
	}
	accountID := r.URL.Query().Get("account")
	token := r.URL.Query().Get("token")
	if accountID == "" && token == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(tokenResp{Error: "?account=<id> or ?token=<...> required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), TokenOpTimeoutD)
	defer cancel()
	names, err := s.RemoveTokenFunc(ctx, accountID, token)
	if err != nil {
		logger.L.Warn().Err(err).Msg("admin: remove token failed")
		s.audit(r, "token.remove.failed", accountID, err.Error())
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(tokenResp{Error: err.Error()})
		return
	}
	s.audit(r, "token.remove", accountID, fmt.Sprintf("%d workers", len(names)))
	_ = json.NewEncoder(w).Encode(tokenResp{Removed: names})
}
