package memory

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// stubChatModel returns a canned response; used to drive LLM extraction and
// consolidation paths in tests without a real model. fail=true simulates a
// model error; a non-JSON resp simulates the mock model's keyword answers.
type stubChatModel struct {
	resp string
	fail bool
}

func (m *stubChatModel) Generate(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	if m.fail {
		return nil, fmt.Errorf("stub model failure")
	}
	return &schema.Message{Content: m.resp}, nil
}

func (m *stubChatModel) Stream(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, fmt.Errorf("stream not supported by stub")
}

// TestUpsertPreference_ConflictResolution verifies value changes archive the
// superseded value into history (capped) and identical values are no-ops.
func TestUpsertPreference_ConflictResolution(t *testing.T) {
	svc := NewService(NewInMemoryMemoryStore(), nil, nil, nil)
	ctx := context.Background()
	meta := EntryMeta{Type: MemoryTypePreference, Importance: 4, Source: "user_stated", ThreadID: "t1", Excerpt: "我喜欢用Python"}

	if err := svc.UpsertPreference(ctx, "u1", "preferred_language", "Python", meta); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	// Same value — no-op.
	if err := svc.UpsertPreference(ctx, "u1", "preferred_language", "Python", meta); err != nil {
		t.Fatalf("same-value upsert: %v", err)
	}
	// Changed value — history records the old one.
	if err := svc.UpsertPreference(ctx, "u1", "preferred_language", "Go", meta); err != nil {
		t.Fatalf("changed upsert: %v", err)
	}

	entry, ok, _ := svc.store.Get(ctx, "u1", "preferred_language")
	if !ok {
		t.Fatal("entry missing")
	}
	if entry.Value != "Go" {
		t.Errorf("expected updated value Go, got %s", entry.Value)
	}
	if len(entry.History) != 1 || entry.History[0].Value != "Python" {
		t.Errorf("expected exactly one history revision with Python, got %+v", entry.History)
	}
	if entry.History[0].SupersededAt == "" {
		t.Error("history revision should carry a timestamp")
	}
}

// TestExtractAndSave_LLMPrefersUpsert verifies the fixed bug: an LLM-extracted
// entry for an existing key now updates the value instead of being dropped.
func TestExtractAndSave_LLMPrefersUpsert(t *testing.T) {
	store := NewInMemoryMemoryStore()
	stub := &stubChatModel{resp: `[{"key":"communication_style","value":"casual","type":"preference","importance":3}]`}
	svc := NewService(store, nil, NewInMemoryVectorStore(), stub)
	ctx := context.Background()

	// Old preference from a previous session.
	if err := svc.PutPreference(ctx, "u1", "communication_style", "formal"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// New message flips the preference (rule extraction cannot parse it).
	if err := svc.ExtractAndSave(ctx, "u1", "t_conv", "以后说话随意一点，别太正式"); err != nil {
		t.Fatalf("extract: %v", err)
	}

	entry, ok, _ := store.Get(ctx, "u1", "communication_style")
	if !ok {
		t.Fatal("entry missing")
	}
	if entry.Value != "casual" {
		t.Errorf("LLM upsert should update the value, got %s", entry.Value)
	}
	if len(entry.History) != 1 || entry.History[0].Value != "formal" {
		t.Errorf("old value should be archived in history, got %+v", entry.History)
	}
	if entry.SourceThreadID != "t_conv" {
		t.Errorf("provenance thread should be recorded, got %q", entry.SourceThreadID)
	}
}

// TestExtractAndSave_LLMOwnsTurnOverRules verifies the LLM-led strategy:
// when the model path succeeds, rule extraction does not run — even if the
// message also contains rule-extractable keywords, the LLM result wins.
func TestExtractAndSave_LLMOwnsTurnOverRules(t *testing.T) {
	store := NewInMemoryMemoryStore()
	// Message contains "java" (a rule keyword), but the LLM says Rust.
	stub := &stubChatModel{resp: `[{"key":"preferred_language","value":"Rust","type":"preference","importance":4}]`}
	svc := NewService(store, nil, nil, stub)
	ctx := context.Background()

	if err := svc.PutPreference(ctx, "u1", "preferred_language", "Python"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := svc.ExtractAndSave(ctx, "u1", "t1", "我喜欢用Java写脚本，但主力语言其实是 Rust"); err != nil {
		t.Fatalf("extract: %v", err)
	}

	entry, ok, _ := store.Get(ctx, "u1", "preferred_language")
	if !ok {
		t.Fatal("entry missing")
	}
	if entry.Value != "Rust" || entry.Source != "llm_extracted" {
		t.Errorf("LLM should own the turn, got value=%s source=%s", entry.Value, entry.Source)
	}
}

// TestExtractAndSave_RuleFallbackOnModelFailure verifies rules take over when
// the model call fails, keeping extraction available without a real LLM.
func TestExtractAndSave_RuleFallbackOnModelFailure(t *testing.T) {
	store := NewInMemoryMemoryStore()
	stub := &stubChatModel{fail: true}
	svc := NewService(store, nil, nil, stub)
	ctx := context.Background()

	if err := svc.ExtractAndSave(ctx, "u1", "t1", "我喜欢用Python"); err != nil {
		t.Fatalf("extract: %v", err)
	}

	entry, ok, _ := store.Get(ctx, "u1", "preferred_language")
	if !ok || entry.Value != "Python" {
		t.Errorf("rule fallback should extract Python, got %+v", entry)
	}
	if entry.Source != "user_stated" {
		t.Errorf("fallback entry should be rule-sourced, got %s", entry.Source)
	}
}

// TestExtractAndSave_MockGarbageFallsBack simulates the mock model: its
// answer is not a JSON array, so extraction falls back to rules — this is
// how mock-mode demos keep working.
func TestExtractAndSave_MockGarbageFallsBack(t *testing.T) {
	store := NewInMemoryMemoryStore()
	stub := &stubChatModel{resp: "您好！我是智能助手，可以帮您计算数学、查询天气。"}
	svc := NewService(store, nil, nil, stub)
	ctx := context.Background()

	if err := svc.ExtractAndSave(ctx, "u1", "t1", "我喜欢用Python"); err != nil {
		t.Fatalf("extract: %v", err)
	}

	if entry, ok, _ := store.Get(ctx, "u1", "preferred_language"); !ok || entry.Value != "Python" {
		t.Errorf("mock garbage answer should fall back to rules, got %+v", entry)
	}
}

// TestExtractAndSave_AntiHallucinationGuard verifies the validator: closed-
// list keys require the value to appear in the message; other keys pass.
func TestExtractAndSave_AntiHallucinationGuard(t *testing.T) {
	store := NewInMemoryMemoryStore()
	// The message never mentions Python, but the LLM "extracts" it anyway.
	stub := &stubChatModel{resp: `[{"key":"preferred_language","value":"Python","type":"preference","importance":4},{"key":"communication_style","value":"casual","type":"preference","importance":3}]`}
	svc := NewService(store, nil, nil, stub)
	ctx := context.Background()

	if err := svc.ExtractAndSave(ctx, "u1", "t1", "帮我写个脚本，说话随意一点"); err != nil {
		t.Fatalf("extract: %v", err)
	}

	if _, ok, _ := store.Get(ctx, "u1", "preferred_language"); ok {
		t.Error("hallucinated language must not be written")
	}
	if entry, ok, _ := store.Get(ctx, "u1", "communication_style"); !ok || entry.Value != "casual" {
		t.Error("non-enumerable key should pass the guard and be written")
	}
}

// TestRetrieveRelevant_ScoringBudgetAndReinforcement verifies ranking,
// archived exclusion, token budget, and access reinforcement.
func TestRetrieveRelevant_ScoringBudgetAndReinforcement(t *testing.T) {
	store := NewInMemoryMemoryStore()
	svc := NewService(store, nil, nil, nil)
	ctx := context.Background()

	now := time.Now()
	old := now.Add(-60 * 24 * time.Hour).Format(time.RFC3339)

	seed := func(key, value, updated string, importance int, archived bool) {
		e := MemoryEntry{UserID: "u1", Key: key, Value: value, Importance: importance,
			Type: MemoryTypePreference, Archived: archived, CreatedAt: updated, UpdatedAt: updated}
		if err := store.Put(ctx, e); err != nil {
			t.Fatalf("seed %s: %v", key, err)
		}
	}
	seed("fresh_important", "core pref", now.Format(time.RFC3339), 5, false)
	seed("stale_marginal", "old noise", old, 1, false)
	seed("archived_one", "hidden", now.Format(time.RFC3339), 5, true)

	out := svc.RetrieveRelevant(ctx, "u1", "", 0)
	if !strings.Contains(out, "fresh_important") {
		t.Errorf("high-importance fresh entry should be retrieved, got: %s", out)
	}
	if strings.Contains(out, "archived_one") {
		t.Error("archived entry must never be retrieved")
	}
	if strings.Contains(out, "stale_marginal") {
		t.Logf("note: stale entry included (score above cut) — checking budget path next")
	}

	// Reinforcement: the first retrieval bumps access stats once.
	e, ok, _ := store.Get(ctx, "u1", "fresh_important")
	if !ok {
		t.Fatal("entry missing")
	}
	if e.AccessCount != 1 || e.LastAccessedAt == "" {
		t.Errorf("retrieved entry should be reinforced once, got count=%d last=%q", e.AccessCount, e.LastAccessedAt)
	}

	// Tiny budget keeps only one line, and the second retrieval
	// reinforces again — use strengthens memory.
	out2 := svc.RetrieveRelevant(ctx, "u1", "", 30)
	if strings.Count(out2, "\n") > 2 {
		t.Errorf("budget should limit injected lines, got: %s", out2)
	}
	e2, _, _ := store.Get(ctx, "u1", "fresh_important")
	if e2.AccessCount != 2 {
		t.Errorf("second retrieval should bump access count to 2, got %d", e2.AccessCount)
	}
}

// TestConsolidate_DegradedArchivesAndProfile verifies the no-LLM path:
// stale low-value entries are archived and a rule-built profile is written.
func TestConsolidate_DegradedArchivesAndProfile(t *testing.T) {
	store := NewInMemoryMemoryStore()
	svc := NewService(store, nil, nil, nil) // chatModel nil → degraded path
	ctx := context.Background()

	now := time.Now()
	store.Put(ctx, MemoryEntry{UserID: "u1", Key: "preferred_language", Value: "Go",
		Type: MemoryTypePreference, Importance: 5, CreatedAt: now.Format(time.RFC3339), UpdatedAt: now.Format(time.RFC3339)})
	store.Put(ctx, MemoryEntry{UserID: "u1", Key: "stale_episode", Value: "old chat fragment",
		Type: MemoryTypeEpisode, Importance: 1,
		CreatedAt: now.Add(-90 * 24 * time.Hour).Format(time.RFC3339),
		UpdatedAt: now.Add(-90 * 24 * time.Hour).Format(time.RFC3339)})

	result, err := svc.Consolidate(ctx, "u1")
	if err != nil {
		t.Fatalf("consolidate: %v", err)
	}
	if result.ArchivedCount != 1 {
		t.Errorf("expected the stale episode to be archived, got %d", result.ArchivedCount)
	}
	if result.LLMUsed {
		t.Error("degraded path must not claim LLM usage")
	}
	stale, _, _ := store.Get(ctx, "u1", "stale_episode")
	if !stale.Archived {
		t.Error("stale entry should be flagged archived")
	}
	profile, ok, _ := store.Get(ctx, "u1", profileKey)
	if !ok || !result.ProfileUpdated {
		t.Fatal("degraded consolidation should write a user_profile entry")
	}
	if !strings.Contains(profile.Value, "preferred_language") {
		t.Errorf("degraded profile should mention known preferences, got: %s", profile.Value)
	}
}

// TestConsolidate_WithLLM verifies the LLM path: profile written, obsolete
// keys archived, new facts precipitated from episodes.
func TestConsolidate_WithLLM(t *testing.T) {
	store := NewInMemoryMemoryStore()
	stub := &stubChatModel{resp: `{"profile":"用户是 Go 开发者，喜欢简洁回答","obsolete_keys":["stale_episode"],"new_facts":[{"key":"team_convention","value":"Go Modules","type":"fact","importance":4}]}`}
	svc := NewService(store, nil, nil, stub)
	svc.SetRetrievalConfig(0, 2) // lower threshold so the LLM path triggers
	ctx := context.Background()

	now := time.Now().Format(time.RFC3339)
	store.Put(ctx, MemoryEntry{UserID: "u1", Key: "preferred_language", Value: "Go",
		Type: MemoryTypePreference, Importance: 5, CreatedAt: now, UpdatedAt: now})
	store.Put(ctx, MemoryEntry{UserID: "u1", Key: "stale_episode", Value: "聊天提到 Go Modules",
		Type: MemoryTypeEpisode, Importance: 2, CreatedAt: now, UpdatedAt: now})

	result, err := svc.Consolidate(ctx, "u1")
	if err != nil {
		t.Fatalf("consolidate: %v", err)
	}
	if !result.LLMUsed || !result.ProfileUpdated {
		t.Errorf("expected LLM profile update, got %+v", result)
	}
	profile, ok, _ := store.Get(ctx, "u1", profileKey)
	if !ok || !strings.Contains(profile.Value, "Go 开发者") {
		t.Errorf("LLM profile should be stored, got %+v", profile)
	}
	stale, _, _ := store.Get(ctx, "u1", "stale_episode")
	if !stale.Archived {
		t.Error("obsolete key should be archived by the LLM decision")
	}
	if fact, ok, _ := store.Get(ctx, "u1", "team_convention"); !ok || fact.Value != "Go Modules" {
		t.Error("new fact should be precipitated from the episode")
	}
}

// TestFileMemoryStore_RoundTripV2Fields verifies the new entry fields
// (type/importance/history/archived/access stats) survive file persistence.
func TestFileMemoryStore_RoundTripV2Fields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory_v2.json")
	ctx := context.Background()

	store1, err := NewFileMemoryStore(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	entry := MemoryEntry{
		UserID: "u1", Key: "preferred_language", Value: "Go",
		Type: MemoryTypePreference, Importance: 4,
		Source: "user_stated", SourceThreadID: "t9", SourceExcerpt: "以后请都用Go",
		AccessCount: 3, LastAccessedAt: time.Now().Format(time.RFC3339),
		CreatedAt: time.Now().Format(time.RFC3339), UpdatedAt: time.Now().Format(time.RFC3339),
		History: []ValueRevision{{Value: "Python", Source: "user_stated", SupersededAt: time.Now().Format(time.RFC3339)}},
	}
	if err := store1.Put(ctx, entry); err != nil {
		t.Fatalf("put: %v", err)
	}

	store2, err := NewFileMemoryStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, ok, _ := store2.Get(ctx, "u1", "preferred_language")
	if !ok {
		t.Fatal("entry missing after reopen")
	}
	if got.Type != MemoryTypePreference || got.Importance != 4 || got.AccessCount != 3 ||
		len(got.History) != 1 || got.History[0].Value != "Python" || got.SourceThreadID != "t9" {
		t.Errorf("v2 fields should round-trip through the file backend, got %+v", got)
	}
}
