package memory

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestUpsertPreference_LowerAuthorityCannotDowngradeImportance is the guard for
// the regression that would undermine the core-preference tier: an inference at
// the default importance must not knock a hand-written preference out of the
// tier that guarantees it is injected at all.
func TestUpsertPreference_LowerAuthorityCannotDowngradeImportance(t *testing.T) {
	store := NewInMemoryMemoryStore()
	svc := NewService(store, nil, nil, nil)
	ctx := context.Background()

	// Hand-written: core importance.
	if err := svc.PutPreference(ctx, "u1", "preferred_language", "Go"); err != nil {
		t.Fatal(err)
	}
	seeded, _, _ := store.Get(ctx, "u1", "preferred_language")
	if !isCorePreference(seeded) {
		t.Fatalf("fixture is wrong: a hand-written entry should be core, got importance %d", seeded.EffectiveImportance())
	}

	// The model restates the key at a low importance.
	if err := svc.UpsertPreference(ctx, "u1", "preferred_language", "Python", EntryMeta{
		Type: MemoryTypePreference, Importance: 1, Source: "llm_extracted",
	}); err != nil {
		t.Fatal(err)
	}

	entry, _, _ := store.Get(ctx, "u1", "preferred_language")
	if entry.Value != "Python" {
		t.Fatalf("the value should follow the write, got %q", entry.Value)
	}
	if entry.Source != "llm_extracted" {
		t.Fatalf("the source should record who wrote the current value, got %q", entry.Source)
	}
	if !isCorePreference(entry) {
		t.Fatalf("a weak write downgraded importance to %d and dropped the entry out of the core tier",
			entry.EffectiveImportance())
	}

	// The protection has to survive a second weak write: the user's statement is
	// now only in the history chain, and that is what authority is read from.
	if err := svc.UpsertPreference(ctx, "u1", "preferred_language", "Rust", EntryMeta{
		Type: MemoryTypePreference, Importance: 1, Source: "llm_extracted",
	}); err != nil {
		t.Fatal(err)
	}
	entry, _, _ = store.Get(ctx, "u1", "preferred_language")
	if !isCorePreference(entry) {
		t.Fatalf("protection did not survive a second weak write: importance %d", entry.EffectiveImportance())
	}

	// A source at least as strong may still change it — this is not a one-way door.
	if err := svc.UpsertPreference(ctx, "u1", "preferred_language", "Elixir", EntryMeta{
		Type: MemoryTypePreference, Importance: 2, Source: "user_stated",
	}); err != nil {
		t.Fatal(err)
	}
	entry, _, _ = store.Get(ctx, "u1", "preferred_language")
	if entry.EffectiveImportance() != 2 {
		t.Fatalf("a user-stated write should be able to set importance, got %d", entry.EffectiveImportance())
	}
}

// TestEntryAuthority covers the ranking itself, including that it reads the
// history chain rather than only the current source.
func TestEntryAuthority(t *testing.T) {
	cases := []struct {
		name  string
		entry MemoryEntry
		want  int
	}{
		{"user stated", MemoryEntry{Source: "user_stated"}, 3},
		{"consolidated", MemoryEntry{Source: "consolidated"}, 2},
		{"llm extracted", MemoryEntry{Source: "llm_extracted"}, 1},
		{"episode has no rank", MemoryEntry{Source: "episode"}, 0},
		{"unknown source", MemoryEntry{Source: "something_new"}, 0},
		{
			// The load-bearing case: the current value came from the model, but the
			// user stated the key earlier and that must keep counting.
			name: "history outranks the current source",
			entry: MemoryEntry{Source: "llm_extracted", History: []ValueRevision{
				{Value: "Go", Source: "user_stated"},
			}},
			want: 3,
		},
		{
			name: "history never lowers authority",
			entry: MemoryEntry{Source: "user_stated", History: []ValueRevision{
				{Value: "x", Source: "llm_extracted"},
			}},
			want: 3,
		},
	}
	for _, c := range cases {
		if got := entryAuthority(c.entry); got != c.want {
			t.Errorf("%s: entryAuthority = %d, want %d", c.name, got, c.want)
		}
	}
}

// TestThreadScopedPreference_IsConfinedToItsThread is the core scope guard: an
// entry stated for one conversation must be injected there and nowhere else.
func TestThreadScopedPreference_IsConfinedToItsThread(t *testing.T) {
	store := NewInMemoryMemoryStore()
	svc := NewService(store, nil, nil, nil)
	ctx := context.Background()

	if err := svc.UpsertPreference(ctx, "u1", "answer_style", "detailed", EntryMeta{
		Type: MemoryTypePreference, Importance: 3, Source: "user_stated",
		ThreadID: "t_a", Scope: ScopeThread,
	}); err != nil {
		t.Fatal(err)
	}

	inThread := svc.RetrieveRelevant(ctx, "u1", "t_a", "帮我看看这段代码", 400, false)
	if !strings.Contains(inThread, "本次会话的偏好") || !strings.Contains(inThread, "answer_style: detailed") {
		t.Fatalf("a thread-scoped entry must be injected in its own thread:\n%s", inThread)
	}

	otherThread := svc.RetrieveRelevant(ctx, "u1", "t_b", "帮我看看这段代码", 400, false)
	if strings.Contains(otherThread, "answer_style") {
		t.Fatalf("a thread-scoped entry leaked into another thread:\n%s", otherThread)
	}

	// And it must not read as a fact about the user: the memory list and the
	// user-wide retrieval both exclude it.
	listed, err := svc.ListPreferences(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range listed {
		if e.Key == "answer_style" {
			t.Fatal("a thread-scoped entry appeared in the user's long-term memory list")
		}
	}
	if strings.Contains(svc.RetrieveRelevant(ctx, "u1", "", "", 400, false), "answer_style") {
		t.Fatal("a thread-scoped entry was injected without a thread in play")
	}
}

// TestThreadScopedPreference_DiesWithItsThread covers the two removal paths. A
// scoped entry nothing removes would sit unreachable and still count as the
// user's data.
func TestThreadScopedPreference_DiesWithItsThread(t *testing.T) {
	store := NewInMemoryMemoryStore()
	svc := NewService(store, nil, nil, nil)
	ctx := context.Background()

	seed := func(t *testing.T, threadID string) {
		t.Helper()
		if err := svc.UpsertPreference(ctx, "u1", "answer_style", "concise", EntryMeta{
			Type: MemoryTypePreference, Importance: 3, Source: "user_stated",
			ThreadID: threadID, Scope: ScopeThread,
		}); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("explicit deletion", func(t *testing.T) {
		seed(t, "t_a")
		if err := svc.DeleteThreadPreferences(ctx, "u1", "t_a"); err != nil {
			t.Fatal(err)
		}
		if got, _ := svc.ListThreadPreferences(ctx, "u1", "t_a"); len(got) != 0 {
			t.Fatalf("scoped entries survived deletion: %v", got)
		}
	})

	t.Run("retention pruning", func(t *testing.T) {
		seed(t, "t_old")
		seed(t, "t_fresh")

		// Age the first thread's entry past the cutoff.
		stale, _, _ := store.Get(ctx, "u1", threadScopedKey("t_old", "answer_style"))
		stale.UpdatedAt = time.Now().Add(-48 * time.Hour).Format(time.RFC3339)
		if err := store.Put(ctx, stale); err != nil {
			t.Fatal(err)
		}
		// A user-wide entry must be untouched by thread pruning.
		if err := svc.PutPreference(ctx, "u1", "preferred_language", "Go"); err != nil {
			t.Fatal(err)
		}

		removed, err := svc.PruneThreadPreferences(ctx, "u1", time.Now().Add(-24*time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		if removed != 1 {
			t.Fatalf("expected exactly the stale entry to be pruned, got %d", removed)
		}
		if got, _ := svc.ListThreadPreferences(ctx, "u1", "t_old"); len(got) != 0 {
			t.Fatal("the stale scoped entry survived pruning")
		}
		if got, _ := svc.ListThreadPreferences(ctx, "u1", "t_fresh"); len(got) != 1 {
			t.Fatalf("a fresh scoped entry was pruned: %v", got)
		}
		if _, ok, _ := svc.GetPreference(ctx, "u1", "preferred_language"); !ok {
			t.Fatal("thread pruning must not touch user-wide entries")
		}
	})
}

// TestPruneThreadPreferences_UsesLastTouchNotCreation is the correctness guard
// for the prune's notion of age.
//
// Injection refreshes LastAccessedAt without touching UpdatedAt, so keying age on
// UpdatedAt deletes the scoped preferences of a conversation that is in active
// use — silently, and exactly the conversations the user is relying on.
// scoreEntry already reads age from the last touch; the prune must agree with it.
func TestPruneThreadPreferences_UsesLastTouchNotCreation(t *testing.T) {
	store := NewInMemoryMemoryStore()
	svc := NewService(store, nil, nil, nil)
	svc.SetThreadPruneInterval(0)
	ctx := context.Background()

	if err := svc.UpsertPreference(ctx, "u1", "answer_style", "concise", EntryMeta{
		Type: MemoryTypePreference, Importance: 3, Source: "user_stated",
		ThreadID: "t_a", Scope: ScopeThread,
	}); err != nil {
		t.Fatal(err)
	}

	// Created long ago, touched just now.
	stored, _, _ := store.Get(ctx, "u1", threadScopedKey("t_a", "answer_style"))
	stored.UpdatedAt = time.Now().Add(-720 * time.Hour).Format(time.RFC3339)
	stored.LastAccessedAt = time.Now().Format(time.RFC3339)
	if err := store.Put(ctx, stored); err != nil {
		t.Fatal(err)
	}

	removed, err := svc.PruneThreadPreferences(ctx, "u1", time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if removed != 0 {
		t.Fatalf("an entry in active use was pruned for its creation time (%d removed)", removed)
	}
	if _, ok, _ := store.Get(ctx, "u1", threadScopedKey("t_a", "answer_style")); !ok {
		t.Fatal("the actively used entry is gone")
	}
}

// TestPruneThreadPreferences_Throttled covers the throttle: retention is measured
// in hours or days, so a prune on every turn would re-read the whole namespace to
// delete entries that cannot have aged past the window yet.
func TestPruneThreadPreferences_Throttled(t *testing.T) {
	store := NewInMemoryMemoryStore()
	svc := NewService(store, nil, nil, nil)
	ctx := context.Background()
	cutoff := time.Now().Add(-24 * time.Hour)

	seedStale := func(t *testing.T, key string) {
		t.Helper()
		if err := svc.UpsertPreference(ctx, "u1", key, "concise", EntryMeta{
			Type: MemoryTypePreference, Importance: 3, Source: "user_stated",
			ThreadID: "t_a", Scope: ScopeThread,
		}); err != nil {
			t.Fatal(err)
		}
		e, _, _ := store.Get(ctx, "u1", threadScopedKey("t_a", key))
		e.UpdatedAt = time.Now().Add(-720 * time.Hour).Format(time.RFC3339)
		if err := store.Put(ctx, e); err != nil {
			t.Fatal(err)
		}
	}

	// The default interval allows the first pass...
	seedStale(t, "answer_style")
	removed, err := svc.PruneThreadPreferences(ctx, "u1", cutoff)
	if err != nil || removed != 1 {
		t.Fatalf("first pass should prune the stale entry: removed=%d err=%v", removed, err)
	}
	// ...and suppresses the second, even with a fresh stale entry to find.
	seedStale(t, "output_format")
	removed, err = svc.PruneThreadPreferences(ctx, "u1", cutoff)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 0 {
		t.Fatalf("the second pass should have been throttled, removed %d", removed)
	}
	if _, ok, _ := store.Get(ctx, "u1", threadScopedKey("t_a", "output_format")); !ok {
		t.Fatal("the throttled pass deleted anyway")
	}

	// Zero disables the throttle, so an operator or a test can force a pass.
	svc.SetThreadPruneInterval(0)
	removed, err = svc.PruneThreadPreferences(ctx, "u1", cutoff)
	if err != nil || removed != 1 {
		t.Fatalf("with the throttle off the entry should be pruned: removed=%d err=%v", removed, err)
	}
}
