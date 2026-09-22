package integration_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/example/agent-eino-demo/web"
)

// TestIntegration_BuildVersionReachesThePage covers the mechanism that lets an open
// tab notice the server has been upgraded under it.
//
// No-cache stops the browser from reusing a stale copy, but nothing stops a running
// page from executing the JS it already loaded — so a fix deployed server-side can
// look unfixed in a tab that was open across it. The page therefore has to know
// which build served it, and be able to ask what the server is serving now.
func TestIntegration_BuildVersionReachesThePage(t *testing.T) {
	application := createTestApp(t)
	server := httptest.NewServer(application.Router.Handler())
	defer server.Close()

	version := web.AssetVersion()
	if version == "" {
		t.Fatal("the server has no asset version")
	}

	t.Run("the page carries its build", func(t *testing.T) {
		resp, err := http.Get(server.URL + "/")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		page := string(body)

		if strings.Contains(page, web.AssetVersionPlaceholder) {
			t.Fatalf("the placeholder was served unsubstituted; the page cannot read its build")
		}
		if !strings.Contains(page, `name="app-version" content="`+version+`"`) {
			t.Fatalf("the page does not carry the server's version %q", version)
		}
		// The asset URLs are versioned from the same hash, so a hand-maintained
		// number cannot drift from the files being served.
		for _, asset := range []string{"app.js", "styles.css"} {
			if !strings.Contains(page, "/assets/"+asset+"?v="+version) {
				t.Errorf("%s is not requested at the current version", asset)
			}
		}
		if got := resp.Header.Get("X-App-Version"); got != version {
			t.Errorf("X-App-Version header = %q, want %q", got, version)
		}
	})

	t.Run("healthz reports what the server serves", func(t *testing.T) {
		// Unauthenticated and already polled by nothing else, which is why the
		// check rides on it rather than adding a route.
		resp, err := http.Get(server.URL + "/healthz")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var body map[string]string
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["status"] != "ok" {
			t.Errorf("status = %q, want ok", body["status"])
		}
		if body["assets"] != version {
			t.Errorf("healthz reports assets=%q, want %q; an open tab compares this "+
				"against its own build to detect an upgrade", body["assets"], version)
		}
		if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
			t.Errorf("healthz must not be cached (got %q), or a poll would keep seeing "+
				"the old version", cc)
		}
	})

	t.Run("index.html is served as html, not by the file server", func(t *testing.T) {
		// The substitution path bypasses the file server, so the content type has
		// to be set explicitly or the browser renders the page as plain text.
		resp, err := http.Get(server.URL + "/index.html")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("index.html served as %q", ct)
		}
		body, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(body), `name="app-version" content="`+version+`"`) {
			t.Error("the explicit /index.html path was not substituted")
		}
	})
}
