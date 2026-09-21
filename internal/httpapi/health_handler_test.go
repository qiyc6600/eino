package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type readinessFunc func(context.Context) error

func (f readinessFunc) Ready(ctx context.Context) error { return f(ctx) }

func responseStatus(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()
	var body map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body["status"]
}

func TestHealthHandlerLive(t *testing.T) {
	handler := NewHealthHandler(nil)
	recorder := httptest.NewRecorder()
	handler.Live(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if recorder.Code != http.StatusOK || responseStatus(t, recorder) != "ok" {
		t.Fatalf("unexpected response: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("expected no-store, got %q", got)
	}
}

func TestHealthHandlerReady(t *testing.T) {
	tests := []struct {
		name     string
		checker  ReadinessChecker
		wantCode int
		wantBody string
	}{
		{name: "no dependencies", wantCode: http.StatusOK, wantBody: "ready"},
		{name: "ready dependency", checker: readinessFunc(func(context.Context) error { return nil }), wantCode: http.StatusOK, wantBody: "ready"},
		{name: "unavailable dependency", checker: readinessFunc(func(context.Context) error {
			return errors.New("secret database hostname")
		}), wantCode: http.StatusServiceUnavailable, wantBody: "unavailable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := NewHealthHandler(tt.checker)
			recorder := httptest.NewRecorder()
			handler.Ready(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
			if recorder.Code != tt.wantCode || responseStatus(t, recorder) != tt.wantBody {
				t.Fatalf("unexpected response: status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if strings.Contains(recorder.Body.String(), "secret") {
				t.Fatal("readiness response leaked the dependency error")
			}
		})
	}
}

func TestHealthHandlerReadinessTimeout(t *testing.T) {
	handler := NewHealthHandler(readinessFunc(func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}))
	handler.timeout = 10 * time.Millisecond
	recorder := httptest.NewRecorder()
	started := time.Now()
	handler.Ready(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", recorder.Code)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("readiness timeout took too long: %v", elapsed)
	}
}

func TestHealthHandlerRejectsOtherMethods(t *testing.T) {
	handler := NewHealthHandler(nil)
	for path, serve := range map[string]func(http.ResponseWriter, *http.Request){
		"/healthz": handler.Live,
		"/readyz":  handler.Ready,
	} {
		recorder := httptest.NewRecorder()
		serve(recorder, httptest.NewRequest(http.MethodPost, path, nil))
		if recorder.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s: expected 405, got %d", path, recorder.Code)
		}
	}
}
