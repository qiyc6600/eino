package integration_test

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// TestIntegration_ThreadTokens verifies the per-thread context token endpoint
// used by the UI token bar: it returns current/threshold/max numbers scoped
// to the calling user's own thread.
func TestIntegration_ThreadTokens(t *testing.T) {
	application := createTestApp(t)
	server := httptest.NewServer(application.Router.Handler())
	defer server.Close()

	sessionID := doLogin(t, server.URL, "admin", testAdminPassword)

	resp := doGet(t, server.URL, "/api/chat/t_default/tokens", sessionID)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 for thread tokens, got %d", resp.StatusCode)
	}

	var info map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		t.Fatalf("decode token info failed: %v", err)
	}
	for _, key := range []string{"current", "threshold", "max"} {
		if _, ok := info[key].(float64); !ok {
			t.Errorf("token info should contain numeric %q, got %v", key, info)
		}
	}
	if max, _ := info["max"].(float64); max <= 0 {
		t.Errorf("max should be positive, got %v", info["max"])
	}

	// Unknown thread (never created for this user) still returns a valid
	// empty-usage estimate rather than an error.
	resp2 := doGet(t, server.URL, "/api/chat/t_nonexistent/tokens", sessionID)
	defer resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Errorf("expected 200 for unknown thread (empty history), got %d", resp2.StatusCode)
	}

	// Unauthenticated request must be rejected.
	resp3 := doGet(t, server.URL, "/api/chat/t_default/tokens", "")
	defer resp3.Body.Close()
	if resp3.StatusCode != 401 {
		t.Errorf("expected 401 without session, got %d", resp3.StatusCode)
	}
}
