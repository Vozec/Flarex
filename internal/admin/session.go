package admin

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/pquerna/otp/totp"
)

const (
	sessionCookieName = "flarex_session"
	csrfCookieName    = "flarex_csrf"
	csrfHeaderName    = "X-CSRF-Token"
	sessionTTL        = 24 * time.Hour

	// Brute-force lockout: N failed auth attempts in W seconds → IP is
	// 429'd for L seconds. Conservative defaults — admin is typically
	// loopback so a false positive would only affect the operator.
	bruteforceWindow      = 60 * time.Second
	bruteforceMaxFailures = 10
	bruteforceLockout     = 5 * time.Minute
)

// SessionSecret sets the signing key for the HTTP session cookie + CSRF
// token. Called from main at boot. If never set, /session returns 503 and
// resolveSessionCookie is a no-op.
func (s *Server) SessionSecret(secret []byte) {
	s.sessionSecret = secret
}

// --- HMAC session cookie helpers ---

func hmacSign(secret []byte, payload string) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(payload))
	return base64URLEncode(m.Sum(nil))
}

func hmacEqual(a, b string) bool {
	return hmac.Equal([]byte(a), []byte(b))
}

func base64URLEncode(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

func base64URLDecode(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}

// --- /session endpoint ---

type sessionReq struct {
	APIKey   string `json:"api_key"`
	TOTPCode string `json:"totp_code"`
}

type sessionResp struct {
	Who       string `json:"who"`
	Expires   int64  `json:"expires_at"`
	CSRFToken string `json:"csrf_token"`
}

// totpErrResp is returned on POST /session when the API key is valid but
// the TOTP code is missing/wrong. Status 401, body {"totp_required": true}
// so the SPA knows to show the 6-digit input.
type totpErrResp struct {
	Error         string `json:"error"`
	TOTPRequired  bool   `json:"totp_required"`
}

// handleSession authenticates a POST body against the bootstrap/registry
// credentials, then sets an HttpOnly + SameSite=Strict signed cookie. The
// SPA uses this to stop passing X-API-Key on every request (reduces XSS
// exfiltration risk — cookie is HttpOnly).
func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodDelete {
		s.clearSession(w)
		_, _ = w.Write([]byte(`{"ok":true}`))
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST or DELETE"}`, http.StatusMethodNotAllowed)
		return
	}
	if s.sessionSecret == nil {
		http.Error(w, `{"error":"session cookie disabled (set admin.ui: true and restart)"}`, http.StatusServiceUnavailable)
		return
	}
	var req sessionReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.APIKey == "" {
		http.Error(w, `{"error":"body must be {\"api_key\":\"...\"}"}`, http.StatusBadRequest)
		return
	}
	// Re-run the same auth logic used on every request — but against a
	// synthetic request carrying the body's api_key as X-API-Key.
	synthetic := r.Clone(r.Context())
	synthetic.Header.Set("X-API-Key", req.APIKey)
	scopes, who, ok := s.resolveScopes(synthetic)
	if !ok {
		s.recordFailedAuth(r)
		http.Error(w, `{"error":"invalid key"}`, http.StatusUnauthorized)
		return
	}
	_ = scopes // scopes are re-derived per request; not embedded in the session cookie
	if s.TOTPSecret != "" {
		if req.TOTPCode == "" {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(totpErrResp{Error: "totp required", TOTPRequired: true})
			return
		}
		if !totp.Validate(req.TOTPCode, s.TOTPSecret) {
			s.recordFailedAuth(r)
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(totpErrResp{Error: "invalid totp code", TOTPRequired: true})
			return
		}
	}
	exp, csrf := s.issueSessionCookies(w, who)
	_ = json.NewEncoder(w).Encode(sessionResp{Who: who, Expires: exp, CSRFToken: csrf})
}

func (s *Server) clearSession(w http.ResponseWriter) {
	for _, n := range []string{sessionCookieName, csrfCookieName} {
		http.SetCookie(w, &http.Cookie{Name: n, Value: "", Path: "/", MaxAge: -1})
	}
}

// checkCSRF enforces the double-submit cookie pattern on non-GET requests
// coming from a session cookie (i.e. browser-originated). X-API-Key
// holders skip the check — they're authenticated per-request already.
func (s *Server) checkCSRF(r *http.Request) bool {
	// Only enforce when the request carried a session cookie.
	c, err := r.Cookie(sessionCookieName)
	if err != nil || c.Value == "" {
		return true // no cookie → no CSRF to guard (client using header auth)
	}
	// Bypass for safe methods.
	if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
		return true
	}
	hdr := r.Header.Get(csrfHeaderName)
	csrfCookie, err := r.Cookie(csrfCookieName)
	if err != nil || csrfCookie.Value == "" || hdr == "" {
		return false
	}
	return hmac.Equal([]byte(hdr), []byte(csrfCookie.Value))
}

// --- brute-force lockout ---

type ipFailureTracker struct {
	mu     sync.Mutex
	failed map[string]*ipFailState // key = client IP
}

type ipFailState struct {
	count       int
	windowStart time.Time
	lockedUntil time.Time
}

var globalBruteforce = &ipFailureTracker{failed: make(map[string]*ipFailState)}

// apikeyMutLimiter caps how fast one caller can mint/revoke API keys. At 10
// /min this blocks a stolen session from mass-escalating scoped keys but
// leaves plenty of headroom for legit admin flows.
const (
	apikeyMutWindow   = 60 * time.Second
	apikeyMutMaxOps   = 10
)

type apikeyMutTracker struct {
	mu    sync.Mutex
	hits  map[string]*apikeyMutState // key = who (from ctx) or IP fallback
}

type apikeyMutState struct {
	count       int
	windowStart time.Time
}

var apikeyMut = &apikeyMutTracker{hits: make(map[string]*apikeyMutState)}

// allowAPIKeyMutation returns false when the caller has exceeded
// apikeyMutMaxOps mutations in the last apikeyMutWindow. Call from every
// /apikeys POST/PATCH/DELETE handler.
func (s *Server) allowAPIKeyMutation(r *http.Request) bool {
	who := "unknown"
	if v := r.Context().Value(whoKey{}); v != nil {
		if str, ok := v.(string); ok && str != "" {
			who = str
		}
	}
	if who == "unknown" {
		who = clientIP(r)
	}
	now := time.Now()
	apikeyMut.mu.Lock()
	defer apikeyMut.mu.Unlock()

	// Evict expired entries to prevent unbounded growth.
	for k, st := range apikeyMut.hits {
		if now.Sub(st.windowStart) > apikeyMutWindow*2 {
			delete(apikeyMut.hits, k)
		}
	}

	st, ok := apikeyMut.hits[who]
	if !ok || now.Sub(st.windowStart) > apikeyMutWindow {
		apikeyMut.hits[who] = &apikeyMutState{count: 1, windowStart: now}
		return true
	}
	st.count++
	return st.count <= apikeyMutMaxOps
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) recordFailedAuth(r *http.Request) {
	ip := clientIP(r)
	now := time.Now()
	globalBruteforce.mu.Lock()
	defer globalBruteforce.mu.Unlock()
	st, ok := globalBruteforce.failed[ip]
	if !ok || now.Sub(st.windowStart) > bruteforceWindow {
		globalBruteforce.failed[ip] = &ipFailState{count: 1, windowStart: now}
		return
	}
	st.count++
	if st.count >= bruteforceMaxFailures {
		st.lockedUntil = now.Add(bruteforceLockout)
	}
}

func (s *Server) isBlockedIP(r *http.Request) bool {
	ip := clientIP(r)
	globalBruteforce.mu.Lock()
	defer globalBruteforce.mu.Unlock()
	st, ok := globalBruteforce.failed[ip]
	if !ok {
		return false
	}
	if time.Now().After(st.lockedUntil) {
		return false
	}
	return !st.lockedUntil.IsZero()
}

// for the whoKey type declared in server.go — ensure strings import stays used
var _ = strings.TrimPrefix
