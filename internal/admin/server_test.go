package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	vm "github.com/VictoriaMetrics/metrics"
	"github.com/Vozec/flarex/internal/pool"
)

func init() {
	vm.NewCounter(`cft_admin_test_counter`).Inc()
}

func spinAdmin(t *testing.T, token string, pprof bool) (*Server, string, context.CancelFunc) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	w := pool.NewWorker("acc1", "w1", "https://x")
	w.Requests.Add(42)
	p := pool.New([]*pool.Worker{w})

	srv := &Server{Addr: addr, Pool: p, Token: token, EnablePprof: pprof}
	ctx, cancel := context.WithCancel(context.Background())
	go srv.Serve(ctx)
	for i := 0; i < 50; i++ {
		c, err := net.Dial("tcp", addr)
		if err == nil {
			c.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	return srv, "http://" + addr, cancel
}

func mustGet(t *testing.T, url string) *http.Response {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func mustDo(t *testing.T, req *http.Request) *http.Response {
	t.Helper()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestAdminHealth(t *testing.T) {
	_, base, cancel := spinAdmin(t, "", false)
	defer cancel()
	resp := mustGet(t, base+"/health")
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status=%d", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	if string(b) != "ok" {
		t.Errorf("body=%q", b)
	}
}

func TestAdminStatusJSON(t *testing.T) {
	_, base, cancel := spinAdmin(t, "", false)
	defer cancel()
	resp := mustGet(t, base+"/status")
	defer resp.Body.Close()
	var out struct {
		PoolSize int `json:"pool_size"`
		Workers  []struct {
			Name     string `json:"name"`
			Requests uint64 `json:"requests"`
			Healthy  bool   `json:"healthy"`
		} `json:"workers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.PoolSize != 1 {
		t.Errorf("pool_size=%d", out.PoolSize)
	}
	if len(out.Workers) != 1 || out.Workers[0].Requests != 42 {
		t.Errorf("workers=%+v", out.Workers)
	}
}

func TestAdminMetricsPrometheusFormat(t *testing.T) {
	_, base, cancel := spinAdmin(t, "", false)
	defer cancel()
	resp := mustGet(t, base+"/metrics")
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), "cft_") {
		end := len(b)
		if end > 400 {
			end = 400
		}
		t.Errorf("no cft_ metric in: %s", b[:end])
	}
}

func TestAdminAuthBearer(t *testing.T) {
	_, base, cancel := spinAdmin(t, "secret", false)
	defer cancel()

	r1 := mustGet(t, base+"/status")
	if r1.StatusCode != 401 {
		t.Errorf("no token: status=%d, want 401", r1.StatusCode)
	}
	r1.Body.Close()

	r2 := mustGet(t, base+"/health")
	if r2.StatusCode != 200 {
		t.Errorf("/health shouldn't require auth: status=%d", r2.StatusCode)
	}
	r2.Body.Close()

	req, _ := http.NewRequest("GET", base+"/status", nil)
	req.Header.Set("Authorization", "Bearer secret")
	r3 := mustDo(t, req)
	defer r3.Body.Close()
	if r3.StatusCode != 200 {
		t.Errorf("bearer: status=%d", r3.StatusCode)
	}

	req2, _ := http.NewRequest("GET", base+"/status", nil)
	req2.Header.Set("Authorization", "Bearer nope")
	r4 := mustDo(t, req2)
	if r4.StatusCode != 401 {
		t.Errorf("bad token: status=%d", r4.StatusCode)
	}
	r4.Body.Close()
}

func TestAdminAuthAPIKey(t *testing.T) {
	srv, base, cancel := spinAdmin(t, "", false)
	defer cancel()
	srv.APIKey = "key123"

	req, _ := http.NewRequest("GET", base+"/status", nil)
	req.Header.Set("X-API-Key", "key123")
	r1 := mustDo(t, req)
	if r1.StatusCode != 200 {
		t.Errorf("X-API-Key: status=%d", r1.StatusCode)
	}
	r1.Body.Close()

	req2, _ := http.NewRequest("GET", base+"/status", nil)
	req2.Header.Set("Authorization", "ApiKey key123")
	r2 := mustDo(t, req2)
	if r2.StatusCode != 200 {
		t.Errorf("ApiKey: status=%d", r2.StatusCode)
	}
	r2.Body.Close()

	req3, _ := http.NewRequest("GET", base+"/status", nil)
	req3.Header.Set("X-API-Key", "wrong")
	r3 := mustDo(t, req3)
	if r3.StatusCode != 401 {
		t.Errorf("bad key: status=%d", r3.StatusCode)
	}
	r3.Body.Close()

	r4 := mustGet(t, base+"/status")
	if r4.StatusCode != 401 {
		t.Errorf("no key: status=%d", r4.StatusCode)
	}
	r4.Body.Close()
}

func TestAdminAuthBasic(t *testing.T) {
	srv, base, cancel := spinAdmin(t, "", false)
	defer cancel()
	srv.BasicUser = "alice"
	srv.BasicPass = "pa$$"

	req, _ := http.NewRequest("GET", base+"/status", nil)
	req.SetBasicAuth("alice", "pa$$")
	r1 := mustDo(t, req)
	if r1.StatusCode != 200 {
		t.Errorf("basic: status=%d", r1.StatusCode)
	}
	r1.Body.Close()

	req2, _ := http.NewRequest("GET", base+"/status", nil)
	req2.SetBasicAuth("alice", "wrong")
	r2 := mustDo(t, req2)
	if r2.StatusCode != 401 {
		t.Errorf("bad basic: status=%d", r2.StatusCode)
	}
	r2.Body.Close()

	r3 := mustGet(t, base+"/status")
	if r3.Header.Get("WWW-Authenticate") == "" {
		t.Error("missing WWW-Authenticate header")
	}
	r3.Body.Close()
}

func TestAdminAuthMultipleSchemes(t *testing.T) {
	srv, base, cancel := spinAdmin(t, "tok", false)
	defer cancel()
	srv.APIKey = "key"
	srv.BasicUser = "u"
	srv.BasicPass = "p"

	req, _ := http.NewRequest("GET", base+"/status", nil)
	req.Header.Set("Authorization", "Bearer tok")
	r1 := mustDo(t, req)
	if r1.StatusCode != 200 {
		t.Errorf("bearer: %d", r1.StatusCode)
	}
	r1.Body.Close()

	req2, _ := http.NewRequest("GET", base+"/status", nil)
	req2.Header.Set("X-API-Key", "key")
	r2 := mustDo(t, req2)
	if r2.StatusCode != 200 {
		t.Errorf("apikey: %d", r2.StatusCode)
	}
	r2.Body.Close()

	req3, _ := http.NewRequest("GET", base+"/status", nil)
	req3.SetBasicAuth("u", "p")
	r3 := mustDo(t, req3)
	if r3.StatusCode != 200 {
		t.Errorf("basic: %d", r3.StatusCode)
	}
	r3.Body.Close()
}

func TestAdminPprofGated(t *testing.T) {
	_, base, cancel := spinAdmin(t, "", false)
	defer cancel()
	resp := mustGet(t, base+"/debug/pprof/")
	if resp.StatusCode != 404 {
		t.Errorf("pprof disabled: status=%d, want 404", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestAdminPprofEnabled(t *testing.T) {
	_, base, cancel := spinAdmin(t, "", true)
	defer cancel()
	resp := mustGet(t, base+"/debug/pprof/")
	if resp.StatusCode != 200 {
		t.Errorf("pprof enabled: status=%d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestAdminAddTokenPOST(t *testing.T) {
	srv, base, cancel := spinAdmin(t, "", false)
	defer cancel()
	srv.AddTokenFunc = func(_ context.Context, tok string, _ int) ([]string, error) {
		if tok != "valid-token" {
			return nil, fmt.Errorf("nope")
		}
		return []string{"cft-aaa", "cft-bbb"}, nil
	}

	resp, err := http.Post(base+"/tokens", "application/json", strings.NewReader(`{"token":"valid-token"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status=%d", resp.StatusCode)
	}
	var out struct {
		Deployed []string `json:"deployed_workers"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if len(out.Deployed) != 2 {
		t.Errorf("got %v", out.Deployed)
	}
}

func TestAdminAddTokenBadBody(t *testing.T) {
	srv, base, cancel := spinAdmin(t, "", false)
	defer cancel()
	srv.AddTokenFunc = func(_ context.Context, _ string, _ int) ([]string, error) { return nil, nil }

	resp, err := http.Post(base+"/tokens", "application/json", strings.NewReader(`bad`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestAdminRemoveTokenDELETE(t *testing.T) {
	srv, base, cancel := spinAdmin(t, "", false)
	defer cancel()
	var calledAcc, calledTok string
	srv.RemoveTokenFunc = func(_ context.Context, accID, tok string) ([]string, error) {
		calledAcc, calledTok = accID, tok
		return []string{"cft-x", "cft-y"}, nil
	}

	req, _ := http.NewRequest(http.MethodDelete, base+"/tokens?account=acc-1", nil)
	resp := mustDo(t, req)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status=%d", resp.StatusCode)
	}
	if calledAcc != "acc-1" || calledTok != "" {
		t.Errorf("bad args acc=%s tok=%s", calledAcc, calledTok)
	}
	var out struct {
		Removed []string `json:"removed_workers"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if len(out.Removed) != 2 {
		t.Errorf("got %v", out.Removed)
	}
}

func TestAdminRemoveTokenMissingArg(t *testing.T) {
	srv, base, cancel := spinAdmin(t, "", false)
	defer cancel()
	srv.RemoveTokenFunc = func(_ context.Context, _, _ string) ([]string, error) { return nil, nil }

	req, _ := http.NewRequest(http.MethodDelete, base+"/tokens", nil)
	resp := mustDo(t, req)
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestAdminMethodNotAllowed(t *testing.T) {
	_, base, cancel := spinAdmin(t, "", false)
	defer cancel()
	req, _ := http.NewRequest(http.MethodPut, base+"/tokens", nil)
	resp := mustDo(t, req)
	defer resp.Body.Close()
	if resp.StatusCode != 405 {
		t.Errorf("expected 405, got %d", resp.StatusCode)
	}
}
