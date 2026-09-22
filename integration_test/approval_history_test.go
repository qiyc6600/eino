package integration_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestIntegration_ApprovalHistory covers the other half of the approvals panel.
//
// An approval leaves the pending list the moment it is decided, so the panel went
// blank as soon as the user handled anything: it was a to-do list that forgot what
// had been done. The history is what keeps it a record.
func TestIntegration_ApprovalHistory(t *testing.T) {
	application := createTestApp(t)
	server := httptest.NewServer(application.Router.Handler())
	defer server.Close()
	sessionID := doLogin(t, server.URL, "admin", testAdminPassword)

	type historyRow struct {
		InterruptID string `json:"InterruptID"`
		ToolName    string `json:"ToolName"`
		Status      string `json:"Status"`
		DecidedAt   string `json:"DecidedAt"`
		Decision    *struct {
			Approved bool   `json:"Approved"`
			Reason   string `json:"Reason"`
		} `json:"Decision"`
		State  []byte `json:"State"`
		Result []byte `json:"Result"`
	}
	history := func(t *testing.T) []historyRow {
		t.Helper()
		resp := doGet(t, server.URL, "/api/approvals/history", sessionID)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("history returned %d", resp.StatusCode)
		}
		var rows []historyRow
		if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
			t.Fatal(err)
		}
		return rows
	}
	pending := func(t *testing.T) []map[string]any {
		t.Helper()
		resp := doGet(t, server.URL, "/api/approvals", sessionID)
		defer resp.Body.Close()
		var rows []map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
			t.Fatal(err)
		}
		return rows
	}
	// raiseInterrupt drives a destructive tool, which pauses for approval.
	raiseInterrupt := func(t *testing.T, thread string) string {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"threadId": thread, "message": "删除订单A-1001"})
		req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/agent/chat", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+sessionID)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var result struct {
			Status    string `json:"status"`
			Interrupt *struct {
				InterruptID string `json:"interrupt_id"`
			} `json:"interrupt"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		if result.Status != "interrupted" || result.Interrupt == nil {
			t.Fatalf("expected an interrupt, got %+v", result)
		}
		return result.Interrupt.InterruptID
	}
	decide := func(t *testing.T, id string, approved bool, reason string) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"approved": approved, "reason": reason})
		req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/approvals/"+id+"/decision", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+sessionID)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("decision returned %d", resp.StatusCode)
		}
	}

	if rows := history(t); len(rows) != 0 {
		t.Fatalf("expected an empty history before anything was decided, got %d", len(rows))
	}

	rejectedID := raiseInterrupt(t, "t_hist_a")
	decide(t, rejectedID, false, "不要删")
	approvedID := raiseInterrupt(t, "t_hist_b")
	decide(t, approvedID, true, "确认删除")

	rows := history(t)
	if len(rows) != 2 {
		t.Fatalf("expected both decisions in the history, got %d: %+v", len(rows), rows)
	}

	// A decided approval must have left the pending list.
	if pending := pending(t); len(pending) != 0 {
		t.Fatalf("a decided approval is still pending: %+v", pending)
	}

	byID := map[string]historyRow{}
	for _, r := range rows {
		byID[r.InterruptID] = r
	}
	rejected, ok := byID[rejectedID]
	if !ok {
		t.Fatalf("the rejected approval is missing from the history: %+v", rows)
	}
	if rejected.Status != "rejected" {
		t.Errorf("status = %q, want rejected", rejected.Status)
	}
	if rejected.Decision == nil || rejected.Decision.Reason != "不要删" {
		t.Errorf("the reason must survive into the history: %+v", rejected.Decision)
	}
	if rejected.DecidedAt == "" {
		t.Error("a history row must carry when it was decided")
	}
	approved, ok := byID[approvedID]
	if !ok || approved.Status != "approved" {
		t.Errorf("the approved approval is missing or wrong: %+v", approved)
	}
	// Newest first: the approved one was decided last.
	if rows[0].InterruptID != approvedID {
		t.Errorf("history should be newest first, got %s then %s", rows[0].InterruptID, rows[1].InterruptID)
	}
	// The payloads are large and irrelevant to a history row.
	for _, r := range rows {
		if len(r.State) > 0 || len(r.Result) > 0 {
			t.Errorf("history rows must not carry State/Result: %+v", r)
		}
	}
}

// TestIntegration_ApprovalHistoryIsPerUser pins the isolation: one user's decisions
// must not appear in another's panel.
func TestIntegration_ApprovalHistoryIsPerUser(t *testing.T) {
	application := createTestApp(t)
	server := httptest.NewServer(application.Router.Handler())
	defer server.Close()

	admin := doLogin(t, server.URL, "admin", testAdminPassword)
	visitor := doLogin(t, server.URL, "visitor", "visitor123")

	// The admin raises and rejects an approval.
	body, _ := json.Marshal(map[string]any{"threadId": "t_iso", "message": "删除订单A-1001"})
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/agent/chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+admin)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Interrupt *struct {
			InterruptID string `json:"interrupt_id"`
		} `json:"interrupt"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&result)
	resp.Body.Close()
	if result.Interrupt == nil {
		t.Fatal("no interrupt to decide")
	}
	dec, _ := json.Marshal(map[string]any{"approved": false, "reason": "no"})
	dreq, _ := http.NewRequest(http.MethodPost, server.URL+"/api/approvals/"+result.Interrupt.InterruptID+"/decision", bytes.NewReader(dec))
	dreq.Header.Set("Content-Type", "application/json")
	dreq.Header.Set("Authorization", "Bearer "+admin)
	dresp, err := http.DefaultClient.Do(dreq)
	if err != nil {
		t.Fatal(err)
	}
	dresp.Body.Close()

	// The visitor's panel must be empty.
	vre := doGet(t, server.URL, "/api/approvals/history", visitor)
	defer vre.Body.Close()
	var visitorRows []map[string]any
	if err := json.NewDecoder(vre.Body).Decode(&visitorRows); err != nil {
		t.Fatal(err)
	}
	if len(visitorRows) != 0 {
		t.Fatalf("another user's decisions leaked into the panel: %+v", visitorRows)
	}

	// And the admin still sees their own.
	are := doGet(t, server.URL, "/api/approvals/history", admin)
	defer are.Body.Close()
	var adminRows []map[string]any
	if err := json.NewDecoder(are.Body).Decode(&adminRows); err != nil {
		t.Fatal(err)
	}
	if len(adminRows) != 1 {
		t.Fatalf("the admin should see their own decision, got %d", len(adminRows))
	}
}
