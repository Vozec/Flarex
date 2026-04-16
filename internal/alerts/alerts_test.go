package alerts

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPSinkSends(t *testing.T) {
	var hit atomic.Int64
	var got Event
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit.Add(1)
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	s := NewHTTPSink(srv.URL, map[string]string{"X-Test": "1"})
	err := s.Send(context.Background(), Event{Kind: KindQuotaWarn, AccountID: "a1", Message: "hi", At: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if hit.Load() != 1 {
		t.Fatalf("hit=%d", hit.Load())
	}
	if got.Kind != KindQuotaWarn || got.AccountID != "a1" || got.Message != "hi" {
		t.Errorf("bad payload: %+v", got)
	}
}

func TestDiscordSinkSends(t *testing.T) {
	var hit atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit.Add(1)
		w.WriteHeader(204)
	}))
	defer srv.Close()
	s := NewDiscordSink(srv.URL, "bot")
	if err := s.Send(context.Background(), Event{Kind: KindQuotaLimit, Message: "boom", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if hit.Load() != 1 {
		t.Fatalf("hit=%d", hit.Load())
	}
}

func TestDispatcherCooldownDedup(t *testing.T) {
	var hit atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		hit.Add(1)
	}))
	defer srv.Close()

	d := NewDispatcher(2*time.Second, NewHTTPSink(srv.URL, nil))
	for i := 0; i < 5; i++ {
		d.Fire(context.Background(), Event{Kind: KindQuotaWarn, AccountID: "acc", Message: "m"})
	}

	time.Sleep(200 * time.Millisecond)
	if got := hit.Load(); got != 1 {
		t.Errorf("cooldown failed: got=%d, want 1", got)
	}
}

func TestDispatcherNoCooldownFires(t *testing.T) {
	var hit atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		hit.Add(1)
	}))
	defer srv.Close()
	d := NewDispatcher(0, NewHTTPSink(srv.URL, nil))
	for i := 0; i < 3; i++ {
		d.Fire(context.Background(), Event{Kind: KindQuotaWarn, AccountID: "acc", Message: "m"})
	}
	time.Sleep(200 * time.Millisecond)
	if got := hit.Load(); got != 3 {
		t.Errorf("want 3 fires, got %d", got)
	}
}

func TestPrettifyAccountName(t *testing.T) {
	cases := map[string]string{
		"alice@example.com's Account": "alice@example.com",
		"Bob@Foo.com’s Account":       "bob@foo.com",
		"NoEmail's Account":           "NoEmail",
		"plain":                       "plain",
		"":                            "",
		"  Foo@Bar.com  ":             "foo@bar.com",
	}
	for in, want := range cases {
		if got := prettifyAccountName(in); got != want {
			t.Errorf("prettify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDispatcherCooldownNoDoubleFire(t *testing.T) {
	var hit atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		hit.Add(1)
	}))
	defer srv.Close()
	d := NewDispatcher(2*time.Second, NewHTTPSink(srv.URL, nil))

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d.Fire(context.Background(), Event{Kind: KindQuotaWarn, AccountID: "race-acc", Message: "x"})
		}()
	}
	wg.Wait()
	time.Sleep(300 * time.Millisecond)
	if got := hit.Load(); got != 1 {
		t.Errorf("expected exactly 1 fire under race, got %d", got)
	}
}

func TestDispatcherSeparateAccountsNotDeduped(t *testing.T) {
	var hit atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		hit.Add(1)
	}))
	defer srv.Close()
	d := NewDispatcher(10*time.Second, NewHTTPSink(srv.URL, nil))
	d.Fire(context.Background(), Event{Kind: KindQuotaWarn, AccountID: "a1", Message: "m"})
	d.Fire(context.Background(), Event{Kind: KindQuotaWarn, AccountID: "a2", Message: "m"})
	time.Sleep(200 * time.Millisecond)
	if got := hit.Load(); got != 2 {
		t.Errorf("different accounts should fire separately, got %d", got)
	}
}
