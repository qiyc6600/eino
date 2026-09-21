package memory

import (
	"context"
	"strings"
	"testing"

	"github.com/example/agent-eino-demo/internal/contextmgr"
)

// TestRetrieveRelevant_CorePreferenceIsNotCrowdedOut is the guard for the
// asymmetry between the two scoring channels.
//
// A preference's ceiling is importance/5 + recency (≈1.6 at the default
// importance), while an episode that matches the query reaches
// 0.4*0.8 + 2.0 = 2.32. Selecting purely by score therefore let query-matching
// episodes push a standing preference out of the context entirely — the user
// asked for a deployment script and the assistant never learned which language
// they prefer. Core preferences are now placed before the competition.
//
// English is used deliberately: the keyword-overlap channel fires there, which
// is what makes the episodes outrank the preference. With a Chinese query the
// overlap channel contributed nothing and the preference happened to survive.
func TestRetrieveRelevant_CorePreferenceIsNotCrowdedOut(t *testing.T) {
	store := NewInMemoryMemoryStore()
	svc := NewService(store, nil, nil, nil)
	ctx := context.Background()

	// PutPreference writes at core importance.
	if err := svc.PutPreference(ctx, "u1", "preferred_language", "Go"); err != nil {
		t.Fatal(err)
	}
	// Episodes sharing tokens with the query, which is what gives them the
	// overlap bonus.
	const text = "the deployment script must cut traffic first, check the deployment script twice"
	for i := 0; i < 8; i++ {
		if err := svc.UpsertPreference(ctx, "u1", "ep_00"+string(rune('a'+i)), text,
			EntryMeta{Type: MemoryTypeEpisode, Importance: 2, Source: "episode"}); err != nil {
			t.Fatal(err)
		}
	}

	const budget = 120
	out := svc.RetrieveRelevant(ctx, "u1", "", "write a deployment script", budget, false)
	if out == "" {
		t.Fatal("expected something to be injected")
	}
	if !strings.Contains(out, "preferred_language") {
		t.Fatalf("the core preference was crowded out by query-matching episodes:\n%s", out)
	}
	if cost := contextmgr.CountText(out); cost > budget {
		t.Fatalf("injected memory exceeds the budget: %d > %d", cost, budget)
	}
	// The episodes should still get the remainder — the tier reserves a place,
	// it does not silence the other channel.
	if !strings.Contains(out, "相关历史记忆") {
		t.Fatalf("episodes should still be recalled with the remaining budget:\n%s", out)
	}
}

// TestIsCorePreference pins the classification, including the boundaries that
// make it meaningful: type matters as much as importance.
func TestIsCorePreference(t *testing.T) {
	cases := []struct {
		name  string
		entry MemoryEntry
		want  bool
	}{
		{"preference at core importance", MemoryEntry{Type: MemoryTypePreference, Importance: 4}, true},
		{"identity at core importance", MemoryEntry{Type: MemoryTypeIdentity, Importance: 5}, true},
		{"rule at core importance", MemoryEntry{Type: MemoryTypeRule, Importance: 4}, true},
		{"preference below core importance", MemoryEntry{Type: MemoryTypePreference, Importance: 3}, false},
		// A high-importance episode is still contextual: it should compete on
		// relevance rather than take a guaranteed slot.
		{"episode even at max importance", MemoryEntry{Type: MemoryTypeEpisode, Importance: 5}, false},
		{"fact even at max importance", MemoryEntry{Type: MemoryTypeFact, Importance: 5}, false},
		// The zero value resolves to the default type and importance.
		{"zero value", MemoryEntry{}, false},
	}
	for _, c := range cases {
		if got := isCorePreference(c.entry); got != c.want {
			t.Errorf("%s: isCorePreference = %v, want %v", c.name, got, c.want)
		}
	}
}
