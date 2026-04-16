package proxy

import "testing"

func TestExtractSession(t *testing.T) {
	cases := []struct {
		user string
		base string
		want string
	}{
		{"alice", "alice", ""},
		{"alice-session-abc123", "alice", "abc123"},
		{"alice:session:xyz", "alice", "xyz"},
		{"alice-session-", "alice", ""},  // empty suffix → no id
		{"bob-session-abc", "alice", ""}, // base mismatch
		{"", "alice", ""},                // empty user
		{"alice-foo", "alice", ""},       // wrong separator
		{"alice-session-abc-session-def", "alice", "abc-session-def"},
	}
	for _, tc := range cases {
		if got := extractSession(tc.user, tc.base); got != tc.want {
			t.Errorf("extractSession(%q,%q)=%q want %q", tc.user, tc.base, got, tc.want)
		}
	}
}

func TestAuthMatches(t *testing.T) {
	cases := []struct {
		user string
		base string
		want bool
	}{
		{"alice", "alice", true},
		{"alice-session-abc", "alice", true},
		{"alice:session:abc", "alice", true},
		{"alice-session-", "alice", false}, // equal length, not >
		{"bob", "alice", false},
		{"alice2", "alice", false},
		{"", "alice", false},
		{"", "", true},
	}
	for _, tc := range cases {
		if got := authMatches(tc.user, tc.base); got != tc.want {
			t.Errorf("authMatches(%q,%q)=%v want %v", tc.user, tc.base, got, tc.want)
		}
	}
}
