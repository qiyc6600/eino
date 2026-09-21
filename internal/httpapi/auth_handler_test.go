package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/example/agent-eino-demo/internal/auth"
)

func TestLoginHandlerRateLimitAndDirectPeer(t *testing.T) {
	users := auth.NewInMemoryUserStore()
	service, err := auth.NewServiceWithUserStore(auth.NewInMemorySessionStore(), users, auth.NewRBACManager())
	if err != nil {
		t.Fatal(err)
	}
	if err := service.EnsureBootstrapAdmin(context.Background(), "admin", "secure-admin-password"); err != nil {
		t.Fatal(err)
	}
	limiter, err := auth.NewLoginLimiter(auth.NewInMemoryLoginLimitStore(), auth.LoginRatePolicy{
		MaxFailures: 2, Window: time.Minute, Lockout: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	service.SetLoginLimiter(limiter)
	handler := NewAuthHandler(service, SessionCookieConfig{TTL: time.Minute})
	attempt := func(source string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"admin","password":"wrong"}`))
		req.RemoteAddr = source + ":12345"
		req.Header.Set("X-Forwarded-For", "192.0.2.99")
		recorder := httptest.NewRecorder()
		handler.Login(recorder, req)
		return recorder
	}
	for i := 0; i < 2; i++ {
		if got := attempt("192.0.2.1").Code; got != http.StatusUnauthorized {
			t.Fatalf("failure %d: expected 401, got %d", i, got)
		}
	}
	locked := attempt("192.0.2.2")
	if locked.Code != http.StatusTooManyRequests || locked.Header().Get("Retry-After") == "" {
		t.Fatalf("expected username-wide 429 and Retry-After, got %d headers=%v", locked.Code, locked.Header())
	}
	var body map[string]string
	if err := json.Unmarshal(locked.Body.Bytes(), &body); err != nil || body["error"] != auth.ErrLoginRateLimited.Error() {
		t.Fatalf("unexpected rate-limit body: %s err=%v", locked.Body.String(), err)
	}
	// The forwarded header is ignored; only the actual HTTP peer determines
	// the source bucket.
	if got := attempt("192.0.2.3").Code; got != http.StatusTooManyRequests {
		t.Fatalf("username limit should still apply from another peer, got %d", got)
	}
}

func TestLoginHandlerIgnoresUntrustedForwardedIP(t *testing.T) {
	service, err := auth.NewServiceWithUserStore(auth.NewInMemorySessionStore(), auth.NewInMemoryUserStore(), auth.NewRBACManager())
	if err != nil {
		t.Fatal(err)
	}
	limiter, err := auth.NewLoginLimiter(auth.NewInMemoryLoginLimitStore(), auth.LoginRatePolicy{
		MaxFailures: 2, Window: time.Minute, Lockout: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	service.SetLoginLimiter(limiter)
	handler := NewAuthHandler(service, SessionCookieConfig{TTL: time.Minute})
	attempt := func(username, peer string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"`+username+`","password":"wrong"}`))
		req.RemoteAddr = peer + ":12345"
		req.Header.Set("X-Forwarded-For", "192.0.2.99")
		recorder := httptest.NewRecorder()
		handler.Login(recorder, req)
		return recorder.Code
	}
	if got := attempt("absent-1", "192.0.2.1"); got != http.StatusUnauthorized {
		t.Fatalf("first attempt: %d", got)
	}
	if got := attempt("absent-2", "192.0.2.1"); got != http.StatusUnauthorized {
		t.Fatalf("second attempt: %d", got)
	}
	if got := attempt("absent-3", "192.0.2.2"); got != http.StatusUnauthorized {
		t.Fatalf("forwarded header unexpectedly shared the source limit: %d", got)
	}
	if got := attempt("absent-4", "192.0.2.1"); got != http.StatusTooManyRequests {
		t.Fatalf("direct peer limit was bypassed: %d", got)
	}
}
