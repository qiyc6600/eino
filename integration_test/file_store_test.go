package integration_test

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// TestIntegration_FileStoreBackends wires the whole app with file-persisted
// stores and verifies that sessions and long-term memory survive an app
// restart (new in-memory process on the same data directory).
func TestIntegration_FileStoreBackends(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("SESSION_STORE", "file")
	t.Setenv("SESSION_STORE_PATH", filepath.Join(dataDir, "sessions.json"))
	t.Setenv("CHECKPOINT_STORE", "file")
	t.Setenv("CHECKPOINT_STORE_PATH", filepath.Join(dataDir, "checkpoints.json"))
	t.Setenv("MEMORY_STORE", "file")
	t.Setenv("MEMORY_STORE_PATH", filepath.Join(dataDir, "memory.json"))

	// --- First app instance: login + write a memory entry ---
	application := createTestApp(t)
	server := httptest.NewServer(application.Router.Handler())

	sessionID := doLogin(t, server.URL, "admin", testAdminPassword)

	resp := doPost(t, server.URL, "/api/memory", sessionID, map[string]string{
		"key":   "language",
		"value": "Python",
	})
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 for memory put, got %d", resp.StatusCode)
	}
	server.Close()

	// Data files must exist on disk.
	for _, name := range []string{"sessions.json", "memory.json"} {
		if _, err := os.Stat(filepath.Join(dataDir, name)); err != nil {
			t.Errorf("expected %s to exist after writes: %v", name, err)
		}
	}

	// --- Second app instance: same data dir simulates a process restart ---
	application2 := createTestApp(t)
	server2 := httptest.NewServer(application2.Router.Handler())
	defer server2.Close()

	// The pre-restart session is still valid.
	resp2 := doGet(t, server2.URL, "/api/auth/me", sessionID)
	defer resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Fatalf("session should survive app restart with file backends, got %d", resp2.StatusCode)
	}
	var me map[string]any
	json.NewDecoder(resp2.Body).Decode(&me)
	if me["sessionId"] != sessionID {
		t.Errorf("expected same sessionId after restart, got %v", me["sessionId"])
	}

	// The memory entry is readable.
	resp3 := doGet(t, server2.URL, "/api/memory", sessionID)
	defer resp3.Body.Close()
	var entries []map[string]any
	json.NewDecoder(resp3.Body).Decode(&entries)
	found := false
	for _, e := range entries {
		if e["key"] == "language" && e["value"] == "Python" {
			found = true
		}
	}
	if !found {
		t.Errorf("memory entry should survive app restart, got %v", entries)
	}
}
