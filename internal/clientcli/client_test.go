package clientcli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "client.yaml")
	cfg := &ConfigFile{URL: "http://srv:9090", APIKey: "k123"}
	if err := SaveTo(cfg, path); err != nil {
		t.Fatal(err)
	}
	got, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.URL != cfg.URL || got.APIKey != cfg.APIKey {
		t.Errorf("roundtrip mismatch: %+v", got)
	}
}

func TestLoadNotLoggedIn(t *testing.T) {
	_, err := LoadFrom(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != ErrNotLoggedIn {
		t.Errorf("got %v, want ErrNotLoggedIn", err)
	}
}

func TestApplyAuthAPIKey(t *testing.T) {
	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("X-API-Key")
		w.WriteHeader(200)
	}))
	defer srv.Close()
	c := New(&ConfigFile{URL: srv.URL, APIKey: "secret"})
	resp, err := c.Do(context.Background(), "GET", "/status", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if seen != "secret" {
		t.Errorf("X-API-Key not sent: %q", seen)
	}
}

func TestApplyAuthBasic(t *testing.T) {
	var u, p string
	var ok atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bu, bp, b := r.BasicAuth()
		u, p = bu, bp
		ok.Store(b)
		w.WriteHeader(200)
	}))
	defer srv.Close()
	c := New(&ConfigFile{URL: srv.URL, BasicU: "alice", BasicP: "x"})
	resp, _ := c.Do(context.Background(), "GET", "/status", nil)
	resp.Body.Close()
	if !ok.Load() || u != "alice" || p != "x" {
		t.Errorf("basic auth mismatch: ok=%v u=%s p=%s", ok.Load(), u, p)
	}
}

func TestGetJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"pool_size": 3})
	}))
	defer srv.Close()
	c := New(&ConfigFile{URL: srv.URL})
	var out struct {
		PoolSize int `json:"pool_size"`
	}
	if err := c.GetJSON(context.Background(), "/status", &out); err != nil {
		t.Fatal(err)
	}
	if out.PoolSize != 3 {
		t.Errorf("got %d", out.PoolSize)
	}
}

func TestGetJSONErrorPropagates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusUnauthorized)
	}))
	defer srv.Close()
	c := New(&ConfigFile{URL: srv.URL, APIKey: "wrong"})
	var out any
	if err := c.GetJSON(context.Background(), "/x", &out); err == nil {
		t.Error("expected error on 401")
	}
}
