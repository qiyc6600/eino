package integration_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func containsString(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// currentUserID reads the caller's own user id, which the role route addresses.
func currentUserID(t *testing.T, serverURL, sessionID string) string {
	t.Helper()
	resp := doGet(t, serverURL, "/api/auth/me", sessionID)
	defer resp.Body.Close()
	var me struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&me); err != nil {
		t.Fatal(err)
	}
	if me.User.ID == "" {
		t.Fatal("no user id in /api/auth/me")
	}
	return me.User.ID
}

// TestIntegration_AdminOnlyEndpoints covers the guard on the administrative
// routes.
//
// These were authenticated but not authorised: POST /api/users takes an arbitrary
// role list, so any signed-in account — a visitor included — could mint an
// administrator or promote itself. The tool-level ACL was enforced throughout,
// which is what made the gap easy to overlook: the framework protected what the
// model may call while the routes that hand out roles were open.
func TestIntegration_AdminOnlyEndpoints(t *testing.T) {
	application := createTestApp(t)
	server := httptest.NewServer(application.Router.Handler())
	defer server.Close()

	admin := doLogin(t, server.URL, "admin", testAdminPassword)
	// createTestApp seeds this account; it is the one that must not be able to
	// reach the administrative routes.
	visitor := doLogin(t, server.URL, "visitor", "visitor123")
	visitorID := currentUserID(t, server.URL, visitor)

	post := func(t *testing.T, path, sessionID string, body any) *http.Response {
		t.Helper()
		raw, _ := json.Marshal(body)
		req, err := http.NewRequest(http.MethodPost, server.URL+path, bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+sessionID)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	put := func(t *testing.T, path, sessionID string, body any) *http.Response {
		t.Helper()
		raw, _ := json.Marshal(body)
		req, err := http.NewRequest(http.MethodPut, server.URL+path, bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+sessionID)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	// The escalation itself: a visitor creating an administrator.
	t.Run("a visitor cannot create a user", func(t *testing.T) {
		resp := post(t, "/api/users", visitor, map[string]any{
			"username": "sneaky", "password": "long-enough-password", "roles": []string{"admin"},
		})
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("a visitor creating an admin must be refused, got %d", resp.StatusCode)
		}
		// And the account must not exist afterwards.
		if _, err := application.AuthSvc.Login(nil, "sneaky", "long-enough-password"); err == nil {
			t.Fatal("the refused account was created anyway")
		}
	})

	t.Run("a visitor cannot promote itself", func(t *testing.T) {
		resp := put(t, "/api/users/"+visitorID+"/roles", visitor, map[string]any{"roles": []string{"admin"}})
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("a visitor promoting itself must be refused, got %d", resp.StatusCode)
		}
	})

	t.Run("a visitor cannot switch the model", func(t *testing.T) {
		resp := post(t, "/api/models/switch", visitor, map[string]any{"profile_id": "mock"})
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("the model is shared by every user, so switching it is administration; got %d", resp.StatusCode)
		}
	})

	t.Run("a visitor cannot change the context budget", func(t *testing.T) {
		resp := put(t, "/api/context/settings", visitor, map[string]any{"max_tokens": 9000})
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("the context window is shared, so changing it is administration; got %d", resp.StatusCode)
		}
		// Reading it stays open: the token bar has to be able to explain itself.
		read := doGet(t, server.URL, "/api/context/settings", visitor)
		defer read.Body.Close()
		if read.StatusCode != http.StatusOK {
			t.Fatalf("a visitor should be able to read the context settings, got %d", read.StatusCode)
		}
	})

	t.Run("an administrator can do all of it", func(t *testing.T) {
		created := post(t, "/api/users", admin, map[string]any{
			"username": "made_by_admin", "password": "long-enough-password", "roles": []string{"visitor"},
		})
		defer created.Body.Close()
		if created.StatusCode != http.StatusCreated {
			t.Fatalf("an administrator must be able to create a user, got %d", created.StatusCode)
		}
		switched := post(t, "/api/models/switch", admin, map[string]any{"profile_id": "mock"})
		defer switched.Body.Close()
		if switched.StatusCode != http.StatusOK {
			t.Fatalf("an administrator must be able to switch the model, got %d", switched.StatusCode)
		}
	})
}

// TestIntegration_ContextBudgetIsAdjustableAtRuntime covers the settings endpoint:
// the two knobs move, the derived threshold follows, and a value outside the
// working range is reported rather than silently applied.
func TestIntegration_ContextBudgetIsAdjustableAtRuntime(t *testing.T) {
	application := createTestApp(t)
	server := httptest.NewServer(application.Router.Handler())
	defer server.Close()
	sessionID := doLogin(t, server.URL, "admin", testAdminPassword)

	type settings struct {
		MaxTokens           int      `json:"max_tokens"`
		ThresholdRatio      float64  `json:"threshold_ratio"`
		SummaryTargetTokens int      `json:"summary_target_tokens"`
		Overhead            int      `json:"overhead"`
		Usable              int      `json:"usable"`
		Threshold           int      `json:"threshold"`
		Clamped             []string `json:"clamped"`
	}
	put := func(t *testing.T, body map[string]any) settings {
		t.Helper()
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequest(http.MethodPut, server.URL+"/api/context/settings", bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+sessionID)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out settings
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	get := func(t *testing.T) settings {
		t.Helper()
		resp := doGet(t, server.URL, "/api/context/settings", sessionID)
		defer resp.Body.Close()
		var out settings
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	t.Run("the threshold follows the two knobs", func(t *testing.T) {
		got := put(t, map[string]any{"max_tokens": 4000, "threshold_ratio": 0.5})
		want := got.Overhead + int(float64(got.Usable)*0.5)
		if got.Threshold != want {
			t.Fatalf("threshold = %d, want overhead + usable×ratio = %d (%+v)", got.Threshold, want, got)
		}
		if got.MaxTokens != 4000 {
			t.Fatalf("max_tokens = %d, want 4000", got.MaxTokens)
		}
		// The reported threshold must be the one the token bar draws, or the user
		// would be adjusting a number different from the one they see.
		info := doGet(t, server.URL, "/api/chat/t_default/tokens", sessionID)
		defer info.Body.Close()
		var tokenInfo struct {
			Max       int `json:"max"`
			Threshold int `json:"threshold"`
		}
		if err := json.NewDecoder(info.Body).Decode(&tokenInfo); err != nil {
			t.Fatal(err)
		}
		if tokenInfo.Max != got.MaxTokens || tokenInfo.Threshold != got.Threshold {
			t.Fatalf("the token bar reports max=%d threshold=%d, the settings endpoint %d/%d — "+
				"they must agree", tokenInfo.Max, tokenInfo.Threshold, got.MaxTokens, got.Threshold)
		}
	})

	t.Run("the change survives to the next read", func(t *testing.T) {
		put(t, map[string]any{"threshold_ratio": 0.25})
		if got := get(t).ThresholdRatio; got != 0.25 {
			t.Fatalf("threshold_ratio = %v, want 0.25 — the setting is on the server, not in the page", got)
		}
	})

	t.Run("a value out of range is clamped and reported", func(t *testing.T) {
		// Above 1 the trigger sits past the window and compaction would never fire,
		// which nothing else would report.
		got := put(t, map[string]any{"threshold_ratio": 5})
		if got.ThresholdRatio != 1 {
			t.Fatalf("ratio = %v, want it clamped to 1", got.ThresholdRatio)
		}
		if len(got.Clamped) == 0 || got.Clamped[0] != "threshold_ratio" {
			t.Fatalf("the response must say the value was changed, got clamped=%v", got.Clamped)
		}

		// A window below the floor cannot be honoured. It is floored rather than
		// stored as asked: reporting 10 while the runner uses 256 would be a lie,
		// and the response says which field it changed.
		got = put(t, map[string]any{"max_tokens": 10})
		if got.MaxTokens < 256 {
			t.Fatalf("max_tokens = %d, want the floor of 256", got.MaxTokens)
		}
		if !containsString(got.Clamped, "max_tokens") {
			t.Fatalf("the response must say max_tokens was changed, got clamped=%v", got.Clamped)
		}
		if got.Usable <= 0 {
			t.Fatalf("usable = %d, want a positive floor", got.Usable)
		}
		if got.Threshold > got.MaxTokens {
			t.Fatalf("threshold %d should stay inside the window %d", got.Threshold, got.MaxTokens)
		}
	})

	t.Run("a partial update leaves the other knobs alone", func(t *testing.T) {
		put(t, map[string]any{"max_tokens": 6000, "threshold_ratio": 0.5, "summary_target_tokens": 300})
		before := get(t)
		after := put(t, map[string]any{"summary_target_tokens": 120})
		if after.MaxTokens != before.MaxTokens || after.ThresholdRatio != before.ThresholdRatio {
			t.Fatalf("only summary_target_tokens was sent, but %+v changed to %+v", before, after)
		}
		if after.SummaryTargetTokens != 120 {
			t.Fatalf("summary_target_tokens = %d, want 120", after.SummaryTargetTokens)
		}
	})
}
