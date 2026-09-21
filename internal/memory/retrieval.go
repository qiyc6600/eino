package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/example/agent-eino-demo/internal/contextmgr"
)

// Retrieval defaults, overridable via Service.SetRetrievalConfig.
//
// The token budgets below are measured with contextmgr.CountText — the same
// estimator the context window and the UI token bar use. That is deliberate: a
// budget expressed in a different unit than the window accounting would let the
// injected memory disagree with what the provider is actually billed for, which
// is exactly what a second, private estimator here used to do.
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

// SetDocumentBudgetTokens overrides the token budget for the document section of
// the injected context. 0 disables document retrieval.
func (s *Service) SetDocumentBudgetTokens(tokens int) {
	if tokens < 0 {
		tokens = 0
	}
	s.documentBudgetTokens = tokens
}

// SetVectorMinScore overrides the relevance cut-off applied to vector results.
//
// It exists because the cut-off is only meaningful relative to the embedder's
// score scale: real embeddings place a relevant pair around 0.6-0.9, while the
// hash fallback compresses everything into roughly 0.15-0.4 — with the default
// 0.3, a genuinely relevant Chinese document (measured at 0.168) is discarded
// while an unrelated one can slip through. 0 restores the default.
func (s *Service) SetVectorMinScore(score float64) {
	if score < 0 {
		score = 0
	}
	s.vectorMinScore = score
}

// MinVectorScore reports the effective cut-off, so callers can log it.
func (s *Service) MinVectorScore() float64 {
	if s.vectorMinScore <= 0 {
		return minVectorRelevance
	}
	return s.vectorMinScore
}

// scoredEntry pairs a memory entry with its retrieval score.
type scoredEntry struct {
	entry MemoryEntry
	score float64
}

// DefaultDocumentBudgetTokens is the token budget for the document section of the
// injected context. It is separate from the memory budget so documents and
// memories cannot crowd each other out; 0 disables document retrieval.
const DefaultDocumentBudgetTokens = 800

// ensureVectorIndex rebuilds the in-process vector index for a user from the KV
// store the first time it is needed.
//
// Both vector backends live in process memory, so without this a restart would
// silently drop semantic recall for documents and episodes even though their text
// is still stored: the KV entries survive, their vectors do not. Rebuilding is
// per user and lazy, so startup pays nothing and idle users are never indexed.
func (s *Service) ensureVectorIndex(ctx context.Context, userID string) {
	s.indexMu.Lock()
	if s.indexedUsers[userID] {
		s.indexMu.Unlock()
		return
	}
	// Marked before the work so concurrent callers do not all rebuild at once.
	s.indexedUsers[userID] = true
	s.indexMu.Unlock()

	if s.vectorStore == nil && s.docStore == nil {
		return
	}
	entries, err := s.store.List(ctx, userID)
	if err != nil {
		// Leave the flag set: a failing store would fail again immediately, and
		// retrieval still works without the vector half.
		return
	}

	names := make(map[string]string)
	for _, e := range entries {
		if !strings.HasPrefix(e.Key, documentKeyPrefix) {
			continue
		}
		var doc Document
		if json.Unmarshal([]byte(e.Value), &doc) == nil {
			names[doc.ID] = doc.Name
		}
	}

	for _, e := range entries {
		switch {
		case strings.HasPrefix(e.Key, chunkKeyPrefix):
			if s.docStore == nil {
				continue
			}
			docID, index, ok := parseChunkKey(e.Key)
			if !ok {
				continue
			}
			_ = s.docStore.StoreWithID(ctx, userID, chunkVectorID(docID, index), e.Value, map[string]any{
				"type":        "document",
				"document_id": docID,
				"name":        names[docID],
				"chunk":       index + 1,
			})
		case e.Type == MemoryTypeEpisode && s.vectorStore != nil:
			_ = s.vectorStore.StoreWithID(ctx, userID, e.Key, e.Value, map[string]any{
				"timestamp": e.UpdatedAt,
				"type":      string(MemoryTypeEpisode),
				"key":       e.Key,
			})
		}
	}
}

// retrieveDocuments returns the document section of the injected context, with a
// citation number per chunk so the model can name its source.
func (s *Service) retrieveDocuments(ctx context.Context, userID, query string, budgetTokens int) string {
	if s.docStore == nil || budgetTokens <= 0 || strings.TrimSpace(query) == "" {
		return ""
	}
	results, err := s.docStore.Query(ctx, userID, query, retrievalVectorTopK)
	if err != nil || len(results) == 0 {
		return ""
	}

	const header = "【文档片段】\n"
	used := contextmgr.CountText(header)
	var lines []string
	for _, r := range results {
		if float64(r.Score) < s.MinVectorScore() {
			continue
		}
		name, _ := r.Metadata["name"].(string)
		if name == "" {
			name = "未命名文档"
		}
		line := fmt.Sprintf("[%d] %s · 片段 %v：%s", len(lines)+1, name, r.Metadata["chunk"], r.Content)
		cost := contextmgr.CountText(line)
		if used+cost > budgetTokens {
			continue
		}
		used += cost
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return ""
	}
	return header + strings.Join(lines, "\n")
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
	// The vector index is in-process, so a restart leaves the stored text without
	// its vectors; rebuild once per user before the first query.
	s.ensureVectorIndex(ctx, userID)

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
	used := contextmgr.CountText(vectorText)

	for _, se := range scored {
		line := formatEntryLine(se.entry)
		cost := contextmgr.CountText(line)
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
	// Documents come last and on their own budget: they answer "what does the
	// material say", which is a different question from "what do I know about
	// this user", and neither should displace the other.
	if docText := s.retrieveDocuments(ctx, userID, query, s.documentBudgetTokens); docText != "" {
		parts = append(parts, docText)
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
