package state

import (
	"path/filepath"
	"testing"
)

func TestQuotaHistory_PutAndList(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	rows := []QuotaDay{
		{Date: "2026-04-10", AccountID: "a1", Used: 10, Limit: 100},
		{Date: "2026-04-11", AccountID: "a1", Used: 30, Limit: 100},
		{Date: "2026-04-11", AccountID: "a2", Used: 5, Limit: 50},
	}
	for _, r := range rows {
		if err := s.PutQuotaSnapshot(r); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ListQuotaHistory()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d rows, want 3", len(got))
	}
}

func TestQuotaHistory_SameDayOverwrite(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	_ = s.PutQuotaSnapshot(QuotaDay{Date: "2026-04-11", AccountID: "a1", Used: 10, Limit: 100})
	_ = s.PutQuotaSnapshot(QuotaDay{Date: "2026-04-11", AccountID: "a1", Used: 50, Limit: 100})
	got, _ := s.ListQuotaHistory()
	if len(got) != 1 {
		t.Fatalf("same-day key should overwrite; got %d rows", len(got))
	}
	if got[0].Used != 50 {
		t.Errorf("expected latest Used=50, got %d", got[0].Used)
	}
}

func TestQuotaHistory_EmptyStore(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.ListQuotaHistory()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("empty store should return 0 rows, got %d", len(got))
	}
}
