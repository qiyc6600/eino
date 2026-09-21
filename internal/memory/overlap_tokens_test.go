package memory

import (
	"context"
	"strings"
	"testing"
)

// TestKeywordOverlap_Chinese pins the lexical channel for Chinese.
//
// tokenize emits a whole CJK run as one token, so a Chinese query could only
// match an entry containing that exact run — measured overlap for a natural
// query was 0, which made the query-aware half of the scoring dead for the
// language most of this project's data is in. overlapTokens emits character
// unigrams and bigrams instead.
func TestKeywordOverlap_Chinese(t *testing.T) {
	query := overlapTokens("帮我写一个部署脚本")

	relevant := MemoryEntry{Key: "note_deploy", Value: "部署脚本要先切流量再回退版本"}
	unrelated := MemoryEntry{Key: "note_lunch", Value: "午饭吃拉面还是盖饭"}

	rel := keywordOverlap(relevant, query)
	unrel := keywordOverlap(unrelated, query)

	if rel <= 0 {
		t.Fatalf("a Chinese query must overlap a Chinese entry, got %v", rel)
	}
	if unrel >= rel {
		t.Fatalf("the unrelated entry scored %v against the relevant one's %v", unrel, rel)
	}
}

// TestTokenize_StaysStableForCJK protects the thing that was deliberately NOT
// changed. tokenize feeds hashEmbed, so its output shape is load-bearing: a
// different tokenization would change every stored hash vector and invalidate
// the VECTOR_MIN_SCORE calibration measured against them. Retrieval scoring uses
// overlapTokens instead, precisely so tokenize could be left alone.
func TestTokenize_StaysStableForCJK(t *testing.T) {
	if got := tokenize("部署脚本"); len(got) != 1 {
		t.Fatalf("tokenize must keep emitting a whole CJK run as one token, got %v", got)
	}
	if got := tokenize("deployment script"); len(got) != 2 {
		t.Fatalf("tokenize must keep splitting ASCII words, got %v", got)
	}
	// And the two must actually differ, or the separation above is pointless.
	if len(overlapTokens("部署脚本")) <= len(tokenize("部署脚本")) {
		t.Fatal("overlapTokens should decompose CJK runs further than tokenize does")
	}
}

// TestRetrieveRelevant_ChineseQueryRanksByRelevance is the end-to-end form: with
// equal importance and age, only relevance can separate two entries, so a
// Chinese question must rank the entry that answers it first.
func TestRetrieveRelevant_ChineseQueryRanksByRelevance(t *testing.T) {
	store := NewInMemoryMemoryStore()
	svc := NewService(store, nil, nil, nil)
	ctx := context.Background()

	// Facts, not core preferences, so both compete on score rather than one
	// taking a guaranteed slot.
	if err := svc.UpsertPreference(ctx, "u1", "note_deploy", "部署脚本要先切流量再回退版本",
		EntryMeta{Type: MemoryTypeFact, Importance: 3}); err != nil {
		t.Fatal(err)
	}
	if err := svc.UpsertPreference(ctx, "u1", "note_lunch", "午饭吃拉面还是盖饭",
		EntryMeta{Type: MemoryTypeFact, Importance: 3}); err != nil {
		t.Fatal(err)
	}

	out := svc.RetrieveRelevant(ctx, "u1", "帮我写一个部署脚本", 400, false)
	rel := strings.Index(out, "note_deploy")
	unrel := strings.Index(out, "note_lunch")
	if rel < 0 {
		t.Fatalf("the relevant entry was not injected:\n%s", out)
	}
	if unrel >= 0 && unrel < rel {
		t.Fatalf("the unrelated entry outranked the relevant one:\n%s", out)
	}
}
