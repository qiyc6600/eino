package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthMiddleware_ValidBearer(t *testing.T) {
	store := NewInMemorySessionStore()
	rbac := NewRBACManager()
	svc := newTestService(t, store, rbac)

	// Login to get a valid session
	resp, _ := svc.Login(nil, "admin", "admin123")

	// Create a protected handler that reads AuthContext
	protected := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ac := FromContext(r.Context())
		if ac == nil {
			t.Error("expected AuthContext in request context")
			http.Error(w, "no auth context", 500)
			return
		}
		if ac.Username != "admin" {
			t.Errorf("expected username=admin, got %s", ac.Username)
		}
		w.WriteHeader(http.StatusOK)
	})

	handler := AuthMiddleware(svc)(protected)

	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("Authorization", "Bearer "+resp.SessionID)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestAuthMiddleware_MissingSession(t *testing.T) {
	store := NewInMemorySessionStore()
	rbac := NewRBACManager()
	svc := newTestService(t, store, rbac)

	protected := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("protected handler should not be called without session")
	})

	handler := AuthMiddleware(svc)(protected)

	req := httptest.NewRequest("GET", "/api/test", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestAuthMiddleware_InvalidSession(t *testing.T) {
	store := NewInMemorySessionStore()
	rbac := NewRBACManager()
	svc := newTestService(t, store, rbac)

	protected := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("protected handler should not be called with invalid session")
	})

	handler := AuthMiddleware(svc)(protected)

	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("Authorization", "Bearer fake_session_id")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

// TestAuthMiddleware_QueryParamRejected verifies the security policy: a
// session ID passed as a URL query parameter must NOT authenticate the
// request — credentials in URLs leak via history, logs and Referer.
func TestAuthMiddleware_QueryParamRejected(t *testing.T) {
	store := NewInMemorySessionStore()
	rbac := NewRBACManager()
	svc := newTestService(t, store, rbac)

	resp, _ := svc.Login(nil, "visitor", "visitor123")

	protected := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler must not be reached with a query-param session")
		w.WriteHeader(http.StatusOK)
	})

	handler := AuthMiddleware(svc)(protected)

	req := httptest.NewRequest("GET", "/api/test?sessionId="+resp.SessionID, nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for query-param session, got %d", w.Code)
	}
}
