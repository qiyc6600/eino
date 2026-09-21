package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestAuthMiddleware_SlidesTheSessionCookie is the regression guard for a session
// that ended at a fixed moment regardless of activity.
//
// The store slides a session's expiry on every validation, but a cookie's MaxAge
// is absolute in the browser. Issued once at login, it made the browser drop the
// credential a fixed TTL after login — so a user who had been active the whole
// time was logged out, and the sliding lifetime the store implements never got a
// chance to matter. Re-issuing the cookie on each authenticated request keeps the
// browser's copy in step with the store's.
func TestAuthMiddleware_SlidesTheSessionCookie(t *testing.T) {
	store := NewInMemorySessionStore()
	svc := newTestService(t, store, NewRBACManager())

	resp, _ := svc.Login(nil, "admin", "admin123")
	if resp == nil || resp.SessionID == "" {
		t.Fatal("login did not return a session")
	}

	ttl := 45 * time.Minute
	handler := AuthMiddleware(svc, SessionCookieConfig{TTL: ttl, Secure: true})(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))

	sessionCookie := func(rec *httptest.ResponseRecorder) *http.Cookie {
		t.Helper()
		for _, c := range rec.Result().Cookies() {
			if c.Name == SessionCookieName {
				return c
			}
		}
		return nil
	}

	t.Run("a cookie-borne session gets a fresh cookie", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/tools", nil)
		req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: resp.SessionID})
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		got := sessionCookie(rec)
		if got == nil {
			t.Fatalf("no %s cookie re-issued; the browser's copy would expire on its own "+
				"schedule (cookies: %v)", SessionCookieName, rec.Result().Cookies())
		}
		if got.Value != resp.SessionID {
			t.Fatalf("the cookie must carry the same session, got %q", got.Value)
		}
		if got.MaxAge != int(ttl.Seconds()) {
			t.Fatalf("the cookie should be re-issued with the full TTL, got MaxAge=%d want %d",
				got.MaxAge, int(ttl.Seconds()))
		}
		if !got.HttpOnly || !got.Secure || got.SameSite != http.SameSiteStrictMode {
			t.Fatalf("the re-issued cookie must keep its protections: %+v", got)
		}
	})

	t.Run("a header-borne session gets no cookie", func(t *testing.T) {
		// A script using Authorization has no cookie to slide, and handing it one
		// would create a browser credential the client never asked for.
		req := httptest.NewRequest(http.MethodGet, "/api/tools", nil)
		req.Header.Set("Authorization", "Bearer "+resp.SessionID)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if got := sessionCookie(rec); got != nil {
			t.Fatalf("a header-authenticated request should not be given a cookie: %+v", got)
		}
	})

	t.Run("an unauthenticated request gets no cookie", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/tools", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rec.Code)
		}
		if got := sessionCookie(rec); got != nil {
			t.Fatalf("a rejected request must not be handed a session cookie: %+v", got)
		}
	})
}
