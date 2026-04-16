package config

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBootstrapDownloadsWhenMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("# fake config\nlisten:\n  socks5: tcp://127.0.0.1:1080\n"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	dl, err := Bootstrap(context.Background(), path, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if !dl {
		t.Error("expected downloaded=true")
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "fake config") {
		t.Errorf("file content unexpected: %s", b)
	}
}

func TestBootstrapNoOpWhenExists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte("existing"), 0o600)

	dl, err := Bootstrap(context.Background(), path, "http://invalid-url-should-not-be-fetched.invalid")
	if err != nil {
		t.Fatal(err)
	}
	if dl {
		t.Error("expected downloaded=false")
	}
}

func TestLooksLikeTemplateDetectsPlaceholders(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.yaml")
	os.WriteFile(path, []byte(`
security:
  hmac_secret: "change_me_32b_minimum_random_string"
`), 0o600)

	got, err := LooksLikeTemplate(path)
	if err != nil {
		t.Fatal(err)
	}
	if got == "" {
		t.Error("expected to detect placeholder")
	}
}

func TestLooksLikeTemplateClean(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.yaml")
	os.WriteFile(path, []byte(`
security:
  hmac_secret: "real-and-strong-secret-yes"
accounts:
  - id: abc123
    token: cf_real_token
    subdomain: my-real-subdomain
`), 0o600)

	got, err := LooksLikeTemplate(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("false positive: %q", got)
	}
}

func TestBootstrapFetchError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()
	dir := t.TempDir()
	_, err := Bootstrap(context.Background(), filepath.Join(dir, "x.yaml"), srv.URL)
	if err == nil {
		t.Fatal("expected error on 500")
	}
}
