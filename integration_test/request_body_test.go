package integration_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestIntegration_RequestBodyBound drives the real router, because a unit test of
// the middleware cannot notice that the router stopped applying it.
//
// Both a protected route and the public login route are checked: the bound wraps
// the whole mux, and login is the only decode site reachable without a session.
func TestIntegration_RequestBodyBound(t *testing.T) {
	application := createTestApp(t)
	server := httptest.NewServer(application.Router.Handler())
	defer server.Close()
	sessionID := doLogin(t, server.URL, "admin", testAdminPassword)

	// The body is past the bound but every *field* is legal: content is two runes
	// and name has no per-field cap. That shape is what makes this a test of the
	// bound rather than of the document cap — an over-sized content field would
	// come back 413 from maxDocumentChars even with no bound in place, which is
	// exactly how the first version of this test failed to discriminate.
	oversized := func() []byte {
		var b bytes.Buffer
		b.WriteString(`{"name":"`)
		b.Write(bytes.Repeat([]byte("a"), 5<<20))
		b.WriteString(`","content":"ok"}`)
		return b.Bytes()
	}

	post := func(t *testing.T, path, sessionID string, body []byte) *http.Response {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, server.URL+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		if sessionID != "" {
			req.Header.Set("Authorization", "Bearer "+sessionID)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	t.Run("protected route", func(t *testing.T) {
		resp := post(t, "/api/documents", sessionID, oversized())
		defer resp.Body.Close()
		// Without the bound this would be a 200: a document with a 5MB name,
		// stored, because no per-field cap covers the name.
		if resp.StatusCode != http.StatusRequestEntityTooLarge {
			t.Fatalf("expected 413 on an over-sized upload, got %d", resp.StatusCode)
		}
	})

	t.Run("the document cap still applies", func(t *testing.T) {
		// A body inside the bound whose content field exceeds maxDocumentChars:
		// the per-field check is still what rejects it.
		var b bytes.Buffer
		b.WriteString(`{"name":"big","content":"`)
		b.Write(bytes.Repeat([]byte("a"), 250_000))
		b.WriteString(`"}`)
		resp := post(t, "/api/documents", sessionID, b.Bytes())
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusRequestEntityTooLarge {
			t.Fatalf("expected 413 from the document cap, got %d", resp.StatusCode)
		}
	})

	t.Run("public login route", func(t *testing.T) {
		resp := post(t, "/api/auth/login", "", oversized())
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusRequestEntityTooLarge {
			t.Fatalf("expected 413 on an over-sized login body, got %d", resp.StatusCode)
		}
	})

	t.Run("a normal upload still works", func(t *testing.T) {
		body := []byte(`{"name":"small","content":"重启服务前先确认备份完成。"}`)
		resp := post(t, "/api/documents", sessionID, body)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			raw := make([]byte, 200)
			n, _ := resp.Body.Read(raw)
			t.Fatalf("a normal upload must still succeed, got %d: %s", resp.StatusCode, raw[:n])
		}
	})

	t.Run("a malformed body is 400, not 413", func(t *testing.T) {
		resp := post(t, "/api/documents", sessionID, []byte(`{"name":`))
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400 for malformed JSON, got %d", resp.StatusCode)
		}
	})
}
