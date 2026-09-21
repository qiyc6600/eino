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

// corePreferenceImportance is the importance at or above which a preference,
// identity or rule is injected unconditionally, ahead of the competition for
// the budget.
//
// The competition is not a fair fight. A preference's score ceiling is
// importance/5 + recency — about 1.6 at the default importance — while an
// episode that matches the query scores 0.4*0.8 + 2.0 = 2.32. With enough
// query-matching episodes a standing preference was crowded out completely: the
// user asked for a deployment script and the assistant did not even learn which
// language they prefer. Importance is an explicit statement that an entry
// matters regardless of what is being asked, so those entries are placed first
// and do not compete.
const corePreferenceImportance = 4

// isCorePreference reports whether an entry is guaranteed a place in the
// injected context.
func isCorePreference(e MemoryEntry) bool {
	if e.EffectiveImportance() < corePreferenceImportance {
		return false
	}
	switch e.EffectiveType() {
	case MemoryTypePreference, MemoryTypeIdentity, MemoryTypeRule:
		return true
	}
	return false
}

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
// score scale. Real embeddings place a relevant pair around 0.6-0.9; the hash
// fallback is a different order of magnitude entirely — measured, a relevant
// Chinese chunk scores 0.043-0.19 and an unrelated one 0.000, so the 0.3 used for
// real embeddings would discard every relevant match. The application picks a
// value per provider; 0 restores the package default.
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
//
// threadID selects the conversation-scoped entries to inject. Passing "" skips
// them, which is what the read-only UI recount does when no thread is in play.
func (s *Service) RetrieveRelevant(ctx context.Context, userID, threadID, query string, budgetTokens int, reinforce bool) string {
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

	queryTokens := overlapTokens(query)

	// --- Split core preferences from the competing pool ---
	var core []scoredEntry
	scored := make([]scoredEntry, 0, len(entries))
	for _, e := range entries {
		if e.Archived || IsReservedKey(e.Key) {
			continue
		}
		if isCorePreference(e) {
			// Scored without the query: core entries are ranked among themselves by
			// importance and recency, since relevance is not what put them here.
			core = append(core, scoredEntry{entry: e, score: scoreEntry(e, nil, false)})
			continue
		}
		scored = append(scored, scoredEntry{
			entry: e,
			score: scoreEntry(e, queryTokens, query != ""),
		})
	}
	sort.Slice(core, func(i, j int) bool { return core[i].score > core[j].score })
	sort.Slice(scored, func(i, j int) bool { return scored[i].score > scored[j].score })

	// --- Assemble within budget ---
	var threadLines, prefLines, otherLines []string
	var reinforced []MemoryEntry
	used := 0

	// Thread-scoped entries first: the most specific scope wins the budget, and
	// there are only ever a handful of them. They are taken from the entries read
	// above — they are in there already, filtered out of the competing pool by
	// IsReservedKey — rather than scored or re-read: they were stated for this
	// conversation, so relevance is not what qualifies them, and a second read
	// would re-materialise the whole namespace once per turn.
	if threadID != "" {
		for _, e := range sortedByKey(scopedEntriesIn(entries, threadID)) {
			line := formatEntryLine(e)
			cost := contextmgr.CountText(line)
			if used+cost > budgetTokens {
				continue
			}
			used += cost
			threadLines = append(threadLines, line)
			reinforced = append(reinforced, e)
		}
	}

	// Core preferences next, so semantic recall cannot displace them.
	for _, se := range core {
		line := formatEntryLine(se.entry)
		cost := contextmgr.CountText(line)
		if used+cost > budgetTokens {
			continue
		}
		used += cost
		prefLines = append(prefLines, line)
		reinforced = append(reinforced, se.entry)
	}

	// Semantic episodes from the vector store, within what core left behind.
	// Skipped rather than passed 0 when nothing is left: maxTokens=0 means
	// "no limit" in that call, so passing it would defeat the budget entirely.
	vectorText := ""
	if remaining := budgetTokens - used; remaining > 0 {
		vectorText = s.QueryVectorMemory(ctx, userID, query, retrievalVectorTopK, remaining)
		used += contextmgr.CountText(vectorText)
	}

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
	// The thread section is labelled as such on purpose: the model must not read a
	// request made for one conversation as a fact about the user.
	if len(threadLines) > 0 {
		parts = append(parts, threadHeader+strings.Join(threadLines, "\n"))
	}
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

// keywordOverlap counts query tokens present in the entry's key, value or
// excerpt. It uses overlapTokens rather than tokenize: the latter emits whole
// CJK runs, which made this channel return 0 for any natural Chinese query.
func keywordOverlap(e MemoryEntry, queryTokens []string) float64 {
	if len(queryTokens) == 0 {
		return 0
	}
	hay := strings.ToLower(e.Key + " " + e.Value + " " + e.SourceExcerpt)
	haySet := make(map[string]bool)
	for _, t := range overlapTokens(hay) {
		haySet[t] = true
	}
	var hits float64
	for _, t := range queryTokens {
		if haySet[t] {
			hits++
		}
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

// threadHeader labels the conversation-scoped section.
const threadHeader = "本次会话的偏好：\n"

// sortedByKey gives thread-scoped entries a stable order. Map iteration is
// random, and the injected text is part of the prompt — an order that changes
// between identical requests makes the token count and the model input wobble
// for no reason.
func sortedByKey(entries map[string]MemoryEntry) []MemoryEntry {
	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]MemoryEntry, 0, len(keys))
	for _, k := range keys {
		out = append(out, entries[k])
	}
	return out
}
