package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPutListDelete(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	recs := []WorkerRec{
		{Name: "w1", AccountID: "a1", URL: "u1", CreatedAt: time.Now()},
		{Name: "w2", AccountID: "a2", URL: "u2", CreatedAt: time.Now()},
	}
	for _, r := range recs {
		if err := st.PutWorker(r); err != nil {
			t.Fatal(err)
		}
	}

	out, err := st.ListWorkers()
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("list: got %d, want 2", len(out))
	}

	if err := st.DeleteWorker("w1"); err != nil {
		t.Fatal(err)
	}
	out, _ = st.ListWorkers()
	if len(out) != 1 || out[0].Name != "w2" {
		t.Errorf("delete failed: %+v", out)
	}
}

func TestPersistAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "p.db")

	st, _ := Open(path)
	st.PutWorker(WorkerRec{Name: "w1", AccountID: "a", URL: "u", CreatedAt: time.Now()})
	st.Close()

	st2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	out, _ := st2.ListWorkers()
	if len(out) != 1 || out[0].Name != "w1" {
		t.Errorf("reopen: %+v", out)
	}
}

func TestOpenFailsBadPath(t *testing.T) {
	_, err := Open("/nonexistent-dir/x.db")
	if err == nil {
		t.Error("expected error for bad path")
	}
	_ = os.TempDir
}
