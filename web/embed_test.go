package web

import (
	"strings"
	"testing"
)

// TestAssetVersion_IsStableAndContentDerived covers the property the staleness
// check rests on: the same files must always hash to the same version, or every
// page load would claim the server had been upgraded.
func TestAssetVersion_IsStableAndContentDerived(t *testing.T) {
	first := AssetVersion()
	if first == "" {
		t.Fatal("AssetVersion returned an empty string")
	}
	// Repeated calls must agree: it is memoised, and a value that drifted would
	// make every open tab show the reload notice.
	for i := 0; i < 3; i++ {
		if got := AssetVersion(); got != first {
			t.Fatalf("AssetVersion is not stable: %q then %q", first, got)
		}
	}
	// A hex prefix of a sha256, not a timestamp or a counter.
	if len(first) != 12 {
		t.Errorf("expected a 12-character hash prefix, got %q", first)
	}
	for _, r := range first {
		if !strings.ContainsRune("0123456789abcdef", r) {
			t.Errorf("version %q is not a hex string", first)
		}
	}
}

// TestAssetVersion_CoversEveryAsset is the guard against a version that does not
// change when the thing that changed is the page's script.
func TestAssetVersion_CoversEveryAsset(t *testing.T) {
	// The hash walks the whole embedded tree; assert the tree actually holds the
	// three files whose change should invalidate a running page.
	for _, name := range []string{"index.html", "assets/app.js", "assets/styles.css"} {
		if _, err := StaticFS.ReadFile(name); err != nil {
			t.Fatalf("%s is not embedded: %v", name, err)
		}
	}
}

// TestIndexHTML_HasTheVersionPlaceholders pins the contract between the template
// and the server: the meta tag the page reads, and the asset URLs, must all carry
// the placeholder — a page that cannot read its own build cannot detect that it is
// stale.
func TestIndexHTML_HasTheVersionPlaceholders(t *testing.T) {
	raw, err := StaticFS.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(raw)

	if n := strings.Count(page, AssetVersionPlaceholder); n < 3 {
		t.Errorf("index.html carries the placeholder %d time(s), want at least 3 "+
			"(the meta tag and both asset URLs); a hand-written version number would "+
			"drift from the served files", n)
	}
	if !strings.Contains(page, `name="app-version"`) {
		t.Error("index.html has no app-version meta tag; the page cannot know which build served it")
	}
	// The hand-maintained query params are gone: the content hash replaces them.
	for _, stale := range []string{"?v=13", "?v=26"} {
		if strings.Contains(page, stale) {
			t.Errorf("index.html still pins %s; the version now comes from the content hash", stale)
		}
	}
}
