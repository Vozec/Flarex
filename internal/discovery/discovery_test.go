package discovery

import "testing"

func TestRandHexLength(t *testing.T) {
	for n := 1; n <= 8; n++ {
		got := randHex(n)
		if len(got) != n*2 {
			t.Errorf("randHex(%d) len=%d", n, len(got))
		}
	}
}

// TestResolveSubdomainCreate was removed: cfapi.ListAccounts builds its own
// http.Client, which bypasses a transport override on http.DefaultClient.
// Reinstating this test needs a cfapi refactor (injectable BaseURL or
// Transport) — tracked separately. The previous body had zero assertions.
