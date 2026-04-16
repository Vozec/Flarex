package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleTestRequestSuccess(t *testing.T) {
	srv := &Server{
		UIEnabled: true,
		TestRequestFunc: func(_ context.Context, _ string) (TestRequestResult, error) {
			return TestRequestResult{
				Worker: "flarex-stub", Colo: "CDG", EgressIP: "203.0.113.1",
				Status: 200, LatencyMs: 42, Mode: "hybrid", Body: "ok",
			}, nil
		},
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/test-request", strings.NewReader(`{"url":"https://example.com"}`))
	srv.handleTestRequest(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var res TestRequestResult
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Worker != "flarex-stub" || res.Status != 200 || res.EgressIP != "203.0.113.1" {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestHandleTestRequestBadURL(t *testing.T) {
	srv := &Server{
		UIEnabled: true,
		TestRequestFunc: func(_ context.Context, _ string) (TestRequestResult, error) {
			t.Fatalf("should not be called on bad url")
			return TestRequestResult{}, nil
		},
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/test-request", strings.NewReader(`{"url":"not-a-url"}`))
	srv.handleTestRequest(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rr.Code)
	}
}

func TestHandleTestRequestWrongMethod(t *testing.T) {
	srv := &Server{
		UIEnabled: true,
		TestRequestFunc: func(_ context.Context, _ string) (TestRequestResult, error) {
			return TestRequestResult{}, nil
		},
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test-request", nil)
	srv.handleTestRequest(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("want 405, got %d", rr.Code)
	}
}

func TestRedactURL(t *testing.T) {
	cases := map[string]string{
		"https://example.com?api_key=abc&keep=1":      "https://example.com?api_key=****&keep=1",
		"token=xyz&user=me":                           "token=****&user=me",
		"no query here":                               "no query here",
		"https://x?Access_Token=CAP&ApIkEy=ONE&foo=2": "https://x?Access_Token=****&ApIkEy=****&foo=2",
	}
	for in, want := range cases {
		if got := redactURL(in); got != want {
			t.Errorf("redactURL(%q)=%q want %q", in, got, want)
		}
	}
}

func TestHandleAlertsWebhook(t *testing.T) {
	var got struct {
		Source, Summary, Severity, Status string
		Called                            int
	}
	srv := &Server{
		AlertHook: func(_ context.Context, source, summary, severity, status string) {
			got.Source = source
			got.Summary = summary
			got.Severity = severity
			got.Status = status
			got.Called++
		},
	}
	body := strings.NewReader(`{
		"receiver":"flarex","status":"firing",
		"alerts":[
			{"status":"firing","labels":{"alertname":"HighErr","severity":"critical"},"annotations":{"summary":"pool hot"}}
		]
	}`)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/alerts/webhook", body)
	srv.handleAlertsWebhook(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if got.Called != 1 || got.Summary != "pool hot" || got.Severity != "critical" || got.Source != "flarex" || got.Status != "firing" {
		t.Fatalf("hook called wrong: %+v", got)
	}
}
