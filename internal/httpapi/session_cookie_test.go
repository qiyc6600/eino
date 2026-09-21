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

// cookieTestHandler builds an AuthHandler over a fresh service with one account.
func cookieTestHandler(t *testing.T, secure bool) (*AuthHandler, *auth.Service) {
	t.Helper()
	service, err := auth.NewServiceWithUserStore(auth.NewInMemorySessionStore(), auth.NewInMemoryUserStore(), auth.NewRBACManager())
	if err != nil {
		t.Fatal(err)
	}
	if err := service.EnsureBootstrapAdmin(context.Background(), "admin", "secure-admin-password"); err != nil {
		t.Fatal(err)
	}
	return NewAuthHandler(service, SessionCookieConfig{TTL: 30 * time.Minute, Secure: secure}), service
}

func loginForCookie(t *testing.T, handler *AuthHandler) *http.Cookie {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login",
		strings.NewReader(`{"username":"admin","password":"secure-admin-password"}`))
	recorder := httptest.NewRecorder()
	handler.Login(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("login failed: %d %s", recorder.Code, recorder.Body.String())
	}
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == auth.SessionCookieName {
			return cookie
		}
	}
	t.Fatalf("login did not set the %s cookie: %v", auth.SessionCookieName, recorder.Result().Header)
	return nil
}

// TestLoginSetsHttpOnlySessionCookie covers the properties that make the cookie
// a better place for the session than JS-reachable storage: script cannot read
// it, and SameSite blocks cross-site requests from carrying it.
func TestLoginSetsHttpOnlySessionCookie(t *testing.T) {
	handler, _ := cookieTestHandler(t, true)
	cookie := loginForCookie(t, handler)

	if !cookie.HttpOnly {
		t.Error("session cookie must be HttpOnly so page scripts cannot read it")
	}
	if !cookie.Secure {
		t.Error("session cookie must be Secure when configured for HTTPS")
	}
	if cookie.SameSite != http.SameSiteStrictMode {
		t.Errorf("session cookie must be SameSite=Strict for CSRF cover, got %v", cookie.SameSite)
	}
	if cookie.Path != "/" {
		t.Errorf("session cookie must cover the whole app, got path %q", cookie.Path)
	}
	if cookie.Value == "" {
		t.Error("session cookie carries no session id")
	}

	// Secure is configurable because browsers drop Secure cookies over plain HTTP
	// on a non-localhost address, which would make every request unauthenticated.
	insecureHandler, _ := cookieTestHandler(t, false)
	if got := loginForCookie(t, insecureHandler); got.Secure {
		t.Error("SESSION_COOKIE_SECURE=false must produce a non-Secure cookie")
	}
}

// TestSessionCookieAuthenticatesRequests is the reload path: with no
// Authorization header at all, the cookie alone must identify the caller.
func TestSessionCookieAuthenticatesRequests(t *testing.T) {
	_, service := cookieTestHandler(t, true)
	middleware := auth.AuthMiddleware(service)

	cookie := loginForCookie(t, &AuthHandler{authSvc: service, cookie: SessionCookieConfig{TTL: time.Minute}})

	var seen *auth.AuthContext
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = auth.FromContext(r.Context())
	})

	// Cookie only.
	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req.AddCookie(cookie)
	recorder := httptest.NewRecorder()
	middleware(next).ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("cookie-only request was rejected: %d %s", recorder.Code, recorder.Body.String())
	}
	if seen == nil || seen.Username != "admin" {
		t.Fatalf("cookie did not establish identity: %+v", seen)
	}

	// No cookie, no header: still rejected.
	recorder = httptest.NewRecorder()
	middleware(next).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/auth/me", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("a request with no credential must be rejected, got %d", recorder.Code)
	}

	// The header still wins when both are present, so non-browser clients are
	// unaffected by the cookie path.
	headerReq := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	headerReq.Header.Set("Authorization", "Bearer "+cookie.Value)
	recorder = httptest.NewRecorder()
	middleware(next).ServeHTTP(recorder, headerReq)
	if recorder.Code != http.StatusOK || seen == nil || seen.Username != "admin" {
		t.Fatalf("header authentication broke: %d %+v", recorder.Code, seen)
	}
}

// TestLogoutClearsSessionCookie asserts the browser is actually signed out: the
// cookie must be expired, not merely ignored.
func TestLogoutClearsSessionCookie(t *testing.T) {
	handler, service := cookieTestHandler(t, true)
	cookie := loginForCookie(t, handler)

	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	req.AddCookie(cookie)
	// The handler reads identity from the context, which the middleware normally
	// provides.
	req = req.WithContext(auth.ContextWithAuth(req.Context(), &auth.AuthContext{
		SessionID: cookie.Value, UserID: "u_admin", Username: "admin", Roles: []string{"admin"},
	}))
	recorder := httptest.NewRecorder()
	handler.Logout(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("logout failed: %d", recorder.Code)
	}
	var cleared *http.Cookie
	for _, c := range recorder.Result().Cookies() {
		if c.Name == auth.SessionCookieName {
			cleared = c
		}
	}
	if cleared == nil {
		t.Fatal("logout did not touch the session cookie")
	}
	if cleared.MaxAge >= 0 || cleared.Value != "" {
		t.Fatalf("logout must expire the cookie, got MaxAge=%d value=%q", cleared.MaxAge, cleared.Value)
	}
	// And the server-side session must be gone, so a replayed cookie fails.
	if _, err := service.ValidateSession(context.Background(), cookie.Value); err == nil {
		t.Fatal("the session survived logout")
	}
}

// TestLoginResponseStillCarriesSessionID pins the non-browser contract: API
// clients keep getting the id in the body and using the Authorization header.
func TestLoginResponseStillCarriesSessionID(t *testing.T) {
	handler, _ := cookieTestHandler(t, true)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login",
		strings.NewReader(`{"username":"admin","password":"secure-admin-password"}`))
	recorder := httptest.NewRecorder()
	handler.Login(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("login failed: %d %s", recorder.Code, recorder.Body.String())
	}

	var body map[string]any
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	id, _ := body["sessionId"].(string)
	if id == "" {
		t.Fatalf("login response must still return sessionId: %v", body)
	}

	// The cookie issued by the same response must carry that same session.
	var cookieValue string
	for _, c := range recorder.Result().Cookies() {
		if c.Name == auth.SessionCookieName {
			cookieValue = c.Value
		}
	}
	if cookieValue != id {
		t.Fatalf("cookie value %q does not match the returned sessionId %q", cookieValue, id)
	}
}
