package admin

import (
	"net/http"
	"testing"
)

func TestRequiredScope(t *testing.T) {
	cases := []struct {
		method, path string
		want         Scope
	}{
		{"GET", "/status", ScopeRead},
		{"GET", "/metrics", ScopeRead},
		{"GET", "/metrics/history", ScopeRead},
		{"GET", "/metrics/series", ScopeRead},
		{"GET", "/config", ScopeRead},
		{"GET", "/accounts", ScopeRead},
		{"POST", "/tokens", ScopeWrite},
		{"DELETE", "/tokens", ScopeWrite},
		{"POST", "/accounts/abc/pause", ScopeWrite},
		{"POST", "/workers/flarex-x/recycle", ScopeWrite},
		{"GET", "/apikeys", ScopeRead},
		{"POST", "/apikeys", ScopeAPIKeys},
		{"DELETE", "/apikeys/xyz", ScopeAPIKeys},
		{"PATCH", "/apikeys/xyz", ScopeAPIKeys},
		{"GET", "/debug/pprof/heap", ScopePprof},
		{"POST", "/debug/pprof/trace", ScopePprof},
	}
	for _, tc := range cases {
		if got := RequiredScope(tc.method, tc.path); got != tc.want {
			t.Errorf("%s %s → %s, want %s", tc.method, tc.path, got, tc.want)
		}
	}
}

func TestHasScope(t *testing.T) {
	have := []Scope{ScopeRead, ScopeWrite}
	if !HasScope(have, ScopeRead) {
		t.Error("read should match")
	}
	if HasScope(have, ScopeAPIKeys) {
		t.Error("apikeys should NOT match")
	}
	if HasScope(AllScopes, ScopePprof) == false {
		t.Error("AllScopes missing pprof")
	}
}

func TestParseScopes_DropsUnknownAndDedup(t *testing.T) {
	got := ParseScopes([]string{"read", "bogus", "read", "write"})
	if len(got) != 2 {
		t.Fatalf("got %v, want 2", got)
	}
	if got[0] != ScopeRead || got[1] != ScopeWrite {
		t.Errorf("order / dedup wrong: %v", got)
	}
}

// sanity: the scope table matches the response middleware uses
func TestRequiredScope_UnknownHTTPMethodDefaultsRead(t *testing.T) {
	// FlareX doesn't serve TRACE, but middleware should still return a
	// conservative read scope (safer to 403 than crash on exotic input).
	if got := RequiredScope(http.MethodTrace, "/status"); got != ScopeRead {
		t.Errorf("TRACE /status → %s, want read", got)
	}
}
