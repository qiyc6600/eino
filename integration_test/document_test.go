package integration_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestIntegration_DocumentLifecycle covers the document RAG API end to end:
// ingest, list, and delete, with the reserved-key storage staying invisible to
// the memory API.
func TestIntegration_DocumentLifecycle(t *testing.T) {
	application := createTestApp(t)
	server := httptest.NewServer(application.Router.Handler())
	defer server.Close()
	sessionID := doLogin(t, server.URL, "admin", testAdminPassword)

	// Ingest.
	resp := doPost(t, server.URL, "/api/documents", sessionID, map[string]string{
		"name":    "运维手册",
		"content": "重启服务前先确认备份完成。\n\n备份失败时不要继续发布。",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for ingest, got %d", resp.StatusCode)
	}
	var doc map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatal(err)
	}
	id, _ := doc["id"].(string)
	if id == "" {
		t.Fatalf("ingest returned no id: %v", doc)
	}
	if doc["name"] != "运维手册" {
		t.Fatalf("ingest returned the wrong name: %v", doc)
	}
	if chunks, _ := doc["chunks"].(float64); chunks < 1 {
		t.Fatalf("ingest reported no chunks: %v", doc)
	}

	// List.
	listResp := doGet(t, server.URL, "/api/documents", sessionID)
	defer listResp.Body.Close()
	var docs []map[string]any
	if err := json.NewDecoder(listResp.Body).Decode(&docs); err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || docs[0]["id"] != id {
		t.Fatalf("expected the ingested document to be listed, got %v", docs)
	}
	// The list must not carry chunk text: it is for management only.
	if _, leaked := docs[0]["content"]; leaked {
		t.Fatalf("the document list leaked chunk text: %v", docs[0])
	}

	// The document must not appear as a memory entry.
	memResp := doGet(t, server.URL, "/api/memory", sessionID)
	defer memResp.Body.Close()
	var memories []map[string]any
	if err := json.NewDecoder(memResp.Body).Decode(&memories); err != nil {
		t.Fatal(err)
	}
	for _, m := range memories {
		if key, _ := m["key"].(string); key != "" && key[0] == '_' {
			t.Fatalf("reserved document entries leaked into the memory list: %v", m)
		}
	}

	// Delete.
	delResp := doDelete(t, server.URL, "/api/documents/"+id, sessionID)
	defer delResp.Body.Close()
	if delResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for delete, got %d", delResp.StatusCode)
	}
	listResp2 := doGet(t, server.URL, "/api/documents", sessionID)
	defer listResp2.Body.Close()
	docs = nil
	json.NewDecoder(listResp2.Body).Decode(&docs)
	if len(docs) != 0 {
		t.Fatalf("document survived deletion: %v", docs)
	}

	// Deleting again reports not found rather than succeeding silently.
	againResp := doDelete(t, server.URL, "/api/documents/"+id, sessionID)
	defer againResp.Body.Close()
	if againResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 on a second delete, got %d", againResp.StatusCode)
	}
}

// TestIntegration_DocumentValidation covers the input checks.
func TestIntegration_DocumentValidation(t *testing.T) {
	application := createTestApp(t)
	server := httptest.NewServer(application.Router.Handler())
	defer server.Close()
	sessionID := doLogin(t, server.URL, "admin", testAdminPassword)

	for _, tc := range []struct{ name, doc, content string }{
		{"empty name", "", "内容"},
		{"empty content", "名称", ""},
	} {
		resp := doPost(t, server.URL, "/api/documents", sessionID, map[string]string{
			"name": tc.doc, "content": tc.content,
		})
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: expected 400, got %d", tc.name, resp.StatusCode)
		}
	}
}

// TestIntegration_DocumentIsolation asserts one user's documents are neither
// visible nor deletable by another.
func TestIntegration_DocumentIsolation(t *testing.T) {
	application := createTestApp(t)
	server := httptest.NewServer(application.Router.Handler())
	defer server.Close()

	adminSession := doLogin(t, server.URL, "admin", testAdminPassword)
	resp := doPost(t, server.URL, "/api/documents", adminSession, map[string]string{
		"name": "管理员的私密文档", "content": "只有管理员能看到的内容。",
	})
	var doc map[string]any
	json.NewDecoder(resp.Body).Decode(&doc)
	resp.Body.Close()
	id, _ := doc["id"].(string)
	if id == "" {
		t.Fatal("ingest returned no id")
	}

	visitorSession := doLogin(t, server.URL, "visitor", "visitor123")

	// The visitor's list must not show it.
	listResp := doGet(t, server.URL, "/api/documents", visitorSession)
	defer listResp.Body.Close()
	var docs []map[string]any
	json.NewDecoder(listResp.Body).Decode(&docs)
	if len(docs) != 0 {
		t.Fatalf("another user's documents are visible: %v", docs)
	}

	// And the visitor must not be able to delete it.
	delResp := doDelete(t, server.URL, "/api/documents/"+id, visitorSession)
	delResp.Body.Close()
	if delResp.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-user delete should report 404, got %d", delResp.StatusCode)
	}
	stillThere := doGet(t, server.URL, "/api/documents", adminSession)
	defer stillThere.Body.Close()
	docs = nil
	json.NewDecoder(stillThere.Body).Decode(&docs)
	if len(docs) != 1 {
		t.Fatalf("the owner's document was removed by another user: %v", docs)
	}
}

// TestIntegration_DocumentEndpointsRequireAuth asserts the routes are behind the
// auth middleware like every other protected resource.
func TestIntegration_DocumentEndpointsRequireAuth(t *testing.T) {
	application := createTestApp(t)
	server := httptest.NewServer(application.Router.Handler())
	defer server.Close()

	body, _ := json.Marshal(map[string]string{"name": "x", "content": "y"})
	post, err := http.Post(server.URL+"/api/documents", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	post.Body.Close()
	if post.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated ingest should be 401, got %d", post.StatusCode)
	}

	get, err := http.Get(server.URL + "/api/documents")
	if err != nil {
		t.Fatal(err)
	}
	get.Body.Close()
	if get.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated list should be 401, got %d", get.StatusCode)
	}
}
