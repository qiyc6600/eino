package memory

import (
	"context"
	"fmt"
	"testing"

	"github.com/example/agent-eino-demo/internal/contextmgr"
)

// TestMemoryBudgetUsesTheWindowEstimator pins the invariant that the memory
// budget is spent in the same unit the context window is measured in.
//
// The package used to carry its own private estimator, which counted CJK at
// one token per character while contextmgr.CountText counts them at roughly
// 1/1.5. The two disagreed by about a quarter, so a 400-token memory budget
// actually injected 305 tokens of context: the section silently under-delivered,
// and the number shown in the UI token bar could not be reconciled with the
// configured budget. Restoring the private estimator for the budget accounting
// reproduces exactly that number.
//
// Asserting only "the output fits the budget" would not have caught that — the
// mismatch made the output smaller, not larger. The second assertion below is
// the one that fails when the unit drifts: with plenty of material to choose
// from, the budget must actually be spent, not merely respected.
func TestMemoryBudgetUsesTheWindowEstimator(t *testing.T) {
	store := NewInMemoryMemoryStore()
	svc := NewService(store, nil, nil, nil)
	ctx := context.Background()

	// Entries are sized so the material comfortably exceeds the budget: filling
	// is then limited by the budget, not by the supply. Many tiny entries would
	// leave a legitimate gap, since an entry that does not fit is dropped whole.
	const value = "这是一段用于填充预算的偏好值"
	const entries = 60
	for i := 0; i < entries; i++ {
		if err := svc.PutPreference(ctx, "u1", fmt.Sprintf("pref_%02d", i), value); err != nil {
			t.Fatal(err)
		}
	}

	const budget = 400
	out := svc.RetrieveRelevant(ctx, "u1", "", budget, false)
	if out == "" {
		t.Fatal("expected the preferences to be injected")
	}

	// The fixture must be able to overflow the budget, or the fill assertion
	// below would pass without the budget doing any work at all.
	supply := 0
	for i := 0; i < entries; i++ {
		supply += contextmgr.CountText(formatEntryLine(MemoryEntry{Key: fmt.Sprintf("pref_%02d", i), Value: value}))
	}
	if supply <= budget {
		t.Fatalf("test setup is wrong: the %d entries total %d tokens, which cannot overflow the %d budget", entries, supply, budget)
	}

	cost := contextmgr.CountText(out)
	if cost > budget {
		t.Fatalf("injected memory exceeds the budget: %d > %d tokens (measured with contextmgr)", cost, budget)
	}
	// 85% leaves room for the per-entry granularity: the last entry that does not
	// fit is dropped whole, which is a few percent, not a third.
	if min := budget * 85 / 100; cost < min {
		t.Fatalf("the budget is measured in a different unit than the window: "+
			"injected %d tokens against a %d budget (<%d). "+
			"One estimator must serve both, or the section under-delivers silently", cost, budget, min)
	}
}
