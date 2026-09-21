package memory

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"
)

// Retrieval defaults, overridable via Service.SetRetrievalConfig.
const (
	DefaultBudgetTokens      = 400
	DefaultConsolidateThresh = 30
	recencyHalfLifeHours     = 168.0 // one week
	accessBoostPerHit        = 0.05
	accessBoostCap           = 0.5
	minVectorRelevance       = 0.3
	retrievalVectorTopK      = 5
)

// SetRetrievalConfig overrides the token budget used by RetrieveRelevant and
// the active-entry count at which consolidation becomes worthwhile.
func (s *Service) SetRetrievalConfig(budgetTokens, consolidateThreshold int) {
	if budgetTokens > 0 {
		s.budgetTokens = budgetTokens
	}
	if consolidateThreshold > 0 {
		s.consolidateThreshold = consolidateThreshold
	}
}

// scoredEntry pairs a memory entry with its retrieval score.
type scoredEntry struct {
	entry MemoryEntry
	score float64
}

// RetrieveRelevant is the single memory-injection entry point. It scores the
// user's non-archived entries by keyword overlap with the query, importance,
// recency, and access frequency; merges vector-retrieved episodes; and
// renders the top entries within a token budget. When reinforce is true a
// selected entry is refreshed (access count + timestamp) so frequently used
// memories rank higher over time ("use strengthens memory"); pass false for
// read-only recounts (e.g. UI display) that must not skew the statistics.
// An empty query scores on importance/recency/access only.
func (s *Service) RetrieveRelevant(ctx context.Context, userID, query string, budgetTokens int, reinforce bool) string {
	// The switch is enforced here rather than at each call site, so no caller can
	// inject memories for a user who turned them off.
	if !s.MemoryEnabled(ctx, userID) {
		return ""
	}
	if budgetTokens <= 0 {
		budgetTokens = s.budgetTokens
	}

	entries, err := s.store.List(ctx, userID)
	if err != nil {
		return ""
	}
	// Note: an empty KV store is not a reason to stop. Vector-recalled episodes
	// are a separate source, and returning early here would silently disable
	// recall for a user whose KV entries were removed while their episodes
	// remained. The cost is one embedding call for a user with no memories yet.

	queryTokens := tokenize(query)

	// --- Score KV entries ---
	scored := make([]scoredEntry, 0, len(entries))
	for _, e := range entries {
		if e.Archived || IsReservedKey(e.Key) {
			continue
		}
		scored = append(scored, scoredEntry{
			entry: e,
			score: scoreEntry(e, queryTokens, query != ""),
		})
	}
	sort.Slice(scored, func(i, j int) bool { return scored[i].score > scored[j].score })

	// --- Semantic episodes from the vector store ---
	// The recalled text is capped at the budget so it can never exceed the
	// injected memory budget on its own; the KV entries below use the remainder.
	vectorText := s.QueryVectorMemory(ctx, userID, query, retrievalVectorTopK, budgetTokens)

	// --- Assemble within budget ---
	var prefLines, otherLines []string
	var reinforced []MemoryEntry
	used := estimateTokens(vectorText)

	for _, se := range scored {
		line := formatEntryLine(se.entry)
		cost := estimateTokens(line)
		if used+cost > budgetTokens {
			continue
		}
		used += cost
		switch se.entry.EffectiveType() {
		case MemoryTypePreference, MemoryTypeIdentity, MemoryTypeRule:
			prefLines = append(prefLines, line)
		default: // fact, episode
			otherLines = append(otherLines, line)
		}
		reinforced = append(reinforced, se.entry)
	}

	// "Use strengthens memory": refresh access stats for what was injected.
	// Read-only recounts (reinforce=false) leave the statistics untouched.
	if reinforce {
		now := time.Now().Format(time.RFC3339)
		for _, e := range reinforced {
			e.AccessCount++
			e.LastAccessedAt = now
			_ = s.store.Put(ctx, e)
		}
	}

	var parts []string
	if len(prefLines) > 0 {
		parts = append(parts, "用户记忆（确定性）：\n"+strings.Join(prefLines, "\n"))
	}
	if vectorText != "" {
		parts = append(parts, vectorText)
	}
	if len(otherLines) > 0 {
		parts = append(parts, "相关历史记忆：\n"+strings.Join(otherLines, "\n"))
	}
	return strings.Join(parts, "\n\n")
}

// scoreEntry computes the retrieval score for one entry.
// overlap: keyword hits against the query (query-aware channel);
// importance: explicit weight; recency: exponential decay with one-week
// half-life; access: mild boost from repeated retrieval hits.
func scoreEntry(e MemoryEntry, queryTokens []string, queryAware bool) float64 {
	score := float64(e.EffectiveImportance()) / 5.0

	if queryAware {
		score += keywordOverlap(e, queryTokens) * 2.0
	}

	// Recency decay from the last touch (access or update).
	ref := e.UpdatedAt
	if e.LastAccessedAt != "" {
		ref = e.LastAccessedAt
	}
	if t, err := time.Parse(time.RFC3339, ref); err == nil {
		hours := time.Since(t).Hours()
		if hours < 0 {
			hours = 0
		}
		score += math.Exp(-hours / recencyHalfLifeHours)
	}

	// Access boost, capped so a noisy entry cannot outrank importance.
	boost := float64(e.AccessCount) * accessBoostPerHit
	if boost > accessBoostCap {
		boost = accessBoostCap
	}
	score += boost

	// Episodes/facts are contextual — slightly below core preferences at
	// equal weight when the query carries no signal.
	if e.EffectiveType() == MemoryTypeEpisode {
		score *= 0.8
	}
	return score
}

// keywordOverlap counts query tokens present in the entry's key or value.
func keywordOverlap(e MemoryEntry, queryTokens []string) float64 {
	if len(queryTokens) == 0 {
		return 0
	}
	hay := strings.ToLower(e.Key + " " + e.Value + " " + e.SourceExcerpt)
	hayTokens := tokenize(hay)
	haySet := make(map[string]bool, len(hayTokens))
	for _, t := range hayTokens {
		haySet[t] = true
	}
	var hits float64
	for _, t := range queryTokens {
		if haySet[t] {
			hits++
		}
	}
	if len(queryTokens) == 0 {
		return 0
	}
	return hits / float64(len(queryTokens))
}

// formatEntryLine renders one entry for prompt injection.
func formatEntryLine(e MemoryEntry) string {
	var b strings.Builder
	b.WriteString("- ")
	b.WriteString(e.Key)
	b.WriteString(": ")
	b.WriteString(e.Value)
	if n := len(e.History); n > 0 {
		b.WriteString(fmt.Sprintf("（已更新 %d 次）", n))
	}
	return b.String()
}

// estimateTokens approximates token usage: CJK runes ≈ 1 token, other
// scripts ≈ 1 token per 4 characters (word-ish granularity).
func estimateTokens(text string) int {
	if text == "" {
		return 0
	}
	cjk, other := 0, 0
	for _, r := range text {
		if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) {
			cjk++
		} else {
			other++
		}
	}
	return cjk + (other+3)/4
}
