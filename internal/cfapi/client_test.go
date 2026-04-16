package cfapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func mockCF(t *testing.T, h http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	c := New("acc-id", "tok")
	c.http = &http.Client{
		Transport: rewriteTransport{dst: srv.URL},
	}
	return c, srv
}

type rewriteTransport struct{ dst string }

func (r rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {

	clone := req.Clone(req.Context())
	u := *req.URL

	i := strings.Index(r.dst, "://")
	if i < 0 {
		return nil, fmt.Errorf("rewriteTransport: dst missing scheme: %q", r.dst)
	}
	u.Scheme = r.dst[:i]
	u.Host = r.dst[i+3:]
	clone.URL = &u
	clone.Host = u.Host
	return http.DefaultTransport.RoundTrip(clone)
}

func TestUploadWorkerSuccess(t *testing.T) {
	c, _ := mockCF(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" || !strings.Contains(r.URL.Path, "/workers/scripts/my-script") {
			http.Error(w, "bad path", 400)
			return
		}
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			http.Error(w, "bad ct", 400)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			http.Error(w, "bad auth", http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "cloudflare:sockets") && !strings.Contains(string(body), "worker.js") {
			http.Error(w, "no script", 400)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "result": map[string]string{"id": "my-script"}})
	})
	if err := c.UploadWorker(context.Background(), "my-script", "export default{async fetch(){return new Response('ok')}}"); err != nil {
		t.Fatal(err)
	}
}

func TestCFErrorBubblesUp(t *testing.T) {
	c, _ := mockCF(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"errors":  []map[string]any{{"code": 10013, "message": "script not found"}},
		})
	})
	err := c.DeleteWorker(context.Background(), "missing")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "10013") || !strings.Contains(err.Error(), "script not found") {
		t.Errorf("error didn't bubble up: %v", err)
	}
}

func TestListWorkersParsesResult(t *testing.T) {
	c, _ := mockCF(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"result": []map[string]any{
				{"id": "cft-aaa"},
				{"id": "cft-bbb"},
				{"id": "other"},
			},
		})
	})
	scripts, err := c.ListWorkers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(scripts) != 3 {
		t.Errorf("got %d scripts", len(scripts))
	}
}

func TestEnableWorkersDevPOSTs(t *testing.T) {
	hit := false
	c, _ := mockCF(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/subdomain") {
			hit = true
			json.NewEncoder(w).Encode(map[string]any{"success": true, "result": map[string]any{}})
			return
		}
		http.Error(w, "bad", 400)
	})
	if err := c.EnableWorkersDev(context.Background(), "s"); err != nil {
		t.Fatal(err)
	}
	if !hit {
		t.Error("POST /subdomain not called")
	}
}
