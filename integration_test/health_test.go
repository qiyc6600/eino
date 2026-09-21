package integration_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPublicHealthEndpoints(t *testing.T) {
	application := createTestApp(t)
	defer application.Close()
	server := httptest.NewServer(application.Router.Handler())
	defer server.Close()

	for path, wantStatus := range map[string]string{"/healthz": "ok", "/readyz": "ready"} {
		resp, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		var body map[string]string
		decodeErr := json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if decodeErr != nil {
			t.Fatalf("decode %s: %v", path, decodeErr)
		}
		if resp.StatusCode != http.StatusOK || body["status"] != wantStatus {
			t.Fatalf("%s: status=%d body=%v", path, resp.StatusCode, body)
		}
	}
}
