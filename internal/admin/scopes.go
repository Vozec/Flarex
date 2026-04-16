package admin

import "net/http"

// Scope is a coarse capability granted to an API key. See
// docs/admin-web-ui.md. Four scopes are defined:
//   - ScopeRead   : every GET (status, metrics, logs, config, apikeys list)
//   - ScopeWrite  : mutating ops on tokens, workers, accounts
//   - ScopeAPIKeys: creating / revoking / patching keys (privilege escalation)
//   - ScopePprof  : /debug/pprof/* (leaks internals, opt-in)
type Scope string

const (
	ScopeRead    Scope = "read"
	ScopeWrite   Scope = "write"
	ScopeAPIKeys Scope = "apikeys"
	ScopePprof   Scope = "pprof"
)

// AllScopes is granted to the static bootstrap credentials (admin.api_key,
// admin.token, admin.basic_*). Dynamic keys in the registry each carry
// their own subset.
var AllScopes = []Scope{ScopeRead, ScopeWrite, ScopeAPIKeys, ScopePprof}

// RequiredScope derives the minimum scope a request needs based on path +
// method. Called from the auth middleware. Default is ScopeRead, which
// makes safe GETs accessible to read-only keys.
func RequiredScope(method, path string) Scope {
	// pprof is its own gate regardless of method.
	if startsWith(path, "/debug/pprof/") {
		return ScopePprof
	}
	// API key admin endpoints — any method.
	if startsWith(path, "/apikeys") {
		switch method {
		case http.MethodGet:
			return ScopeRead
		default:
			return ScopeAPIKeys
		}
	}
	// Write-side mutations on tokens / workers / accounts.
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch:
		return ScopeWrite
	}
	return ScopeRead
}

func startsWith(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// HasScope checks whether the holder's scopes cover the required one.
func HasScope(have []Scope, need Scope) bool {
	for _, s := range have {
		if s == need {
			return true
		}
	}
	return false
}

// ParseScopes normalizes a user-provided list (e.g. from request body) into
// a deduplicated set of known scopes. Unknown values are ignored.
func ParseScopes(raw []string) []Scope {
	seen := map[Scope]struct{}{}
	out := make([]Scope, 0, len(raw))
	known := map[Scope]struct{}{ScopeRead: {}, ScopeWrite: {}, ScopeAPIKeys: {}, ScopePprof: {}}
	for _, r := range raw {
		s := Scope(r)
		if _, ok := known[s]; !ok {
			continue
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// ScopesToStrings converts []Scope → []string for JSON output.
func ScopesToStrings(scopes []Scope) []string {
	out := make([]string, len(scopes))
	for i, s := range scopes {
		out[i] = string(s)
	}
	return out
}
