package memory

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/example/agent-eino-demo/internal/auth"
	"github.com/example/agent-eino-demo/internal/contextmgr"
)

// VectorResult holds a single result from a vector similarity query.
type VectorResult struct {
	Content  string         `json:"content"`  // 原始文本
	Score    float32        `json:"score"`    // 相似度分数 0-1
	Metadata map[string]any `json:"metadata"` // 元数据 (thread_id, timestamp 等)
}

// VectorStore is the interface for semantic memory retrieval.
// Implementations store text content with embedding vectors and support
// similarity-based queries. All methods are namespaced by userID for isolation.
type VectorStore interface {
	// Store saves a memory entry with its embedding vector under a generated id.
	Store(ctx context.Context, userID, content string, metadata map[string]any) error
	// StoreWithID saves an entry under a caller-supplied id, so it can be
	// removed again later (document chunks are re-indexed and deleted by id).
	StoreWithID(ctx context.Context, userID, id, content string, metadata map[string]any) error
	// Query returns the top-K most similar memories for a given query text.
	Query(ctx context.Context, userID, query string, topK int) ([]VectorResult, error)
	// Delete removes the named entries. Unknown ids are ignored.
	Delete(ctx context.Context, userID string, ids ...string) error
	// DeleteUser removes all vector entries for a user (e.g. on privacy request).
	DeleteUser(ctx context.Context, userID string) error
}

// --- Simple InMemoryVectorStore (no external dependencies) ---
// Uses a hash-based pseudo-embedding for vectors. This is NOT a real semantic
// embedding — it provides deterministic but crude similarity based on word overlap.
// For proper semantic search, use the chromem-go implementation.

type vectorEntry struct {
	id       string
	content  string
	vector   []float32
	metadata map[string]any
	addedAt  time.Time
}

// InMemoryVectorStore implements VectorStore using hash-based pseudo-embeddings.
// Suitable as a zero-dependency fallback when no real embedding service is available.
type InMemoryVectorStore struct {
	mu      sync.RWMutex
	entries map[string][]vectorEntry // userID -> entries
	dim     int                      // vector dimensionality
}

// hashEmbeddingDim is the dimension of the hash embedding.
//
// It has to be large relative to the number of features one chunk contributes,
// because hashed features that collide are indistinguishable. A ~125-rune
// Chinese chunk contributes about 140 features (one per word, one per adjacent
// rune pair), so at the original 128 dimensions it filled 73% of the space and
// collisions dominated: measured on a nine-pair fixture, an unrelated passage
// outscored a relevant one (worst margin -0.28). At 8192 the occupancy is 2% and
// no pair inverted; 16384 measured no better.
//
// The cost is 32KB per indexed entry and an O(dim) cosine per candidate, both of
// which are fine for an in-process index holding a user's own memories.
const hashEmbeddingDim = 8192

// NewInMemoryVectorStore creates a new simple in-memory vector store.
func NewInMemoryVectorStore() *InMemoryVectorStore {
	return &InMemoryVectorStore{
		entries: make(map[string][]vectorEntry),
		dim:     hashEmbeddingDim,
	}
}

// Store saves a text entry with a hash-based pseudo-embedding.
func (s *InMemoryVectorStore) Store(ctx context.Context, userID, content string, metadata map[string]any) error {
	return s.StoreWithID(ctx, userID, fmt.Sprintf("mem_%d", time.Now().UnixNano()), content, metadata)
}

// StoreWithID saves a text entry under the given id, replacing any entry that
// already uses it.
func (s *InMemoryVectorStore) StoreWithID(ctx context.Context, userID, id, content string, metadata map[string]any) error {
	if err := auth.CheckUserScope(ctx, userID); err != nil {
		return err
	}
	if id == "" {
		return fmt.Errorf("vector entry id must not be empty")
	}

	vector := hashEmbed(content, s.dim)

	s.mu.Lock()
	defer s.mu.Unlock()

	// Re-using an id replaces the previous entry rather than duplicating it, so
	// re-indexing a document is idempotent.
	entries := s.entries[userID]
	for i := range entries {
		if entries[i].id == id {
			entries[i] = vectorEntry{id: id, content: content, vector: vector, metadata: metadata, addedAt: time.Now()}
			s.entries[userID] = entries
			return nil
		}
	}
	s.entries[userID] = append(entries, vectorEntry{
		id:       id,
		content:  content,
		vector:   vector,
		metadata: metadata,
		addedAt:  time.Now(),
	})
	return nil
}

// Delete removes the named entries for a user. Unknown ids are ignored.
func (s *InMemoryVectorStore) Delete(ctx context.Context, userID string, ids ...string) error {
	if err := auth.CheckUserScope(ctx, userID); err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}

	drop := make(map[string]bool, len(ids))
	for _, id := range ids {
		drop[id] = true
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	entries := s.entries[userID]
	kept := entries[:0]
	for _, e := range entries {
		if !drop[e.id] {
			kept = append(kept, e)
		}
	}
	s.entries[userID] = kept
	return nil
}

// Query returns the top-K most similar entries for a query string.
func (s *InMemoryVectorStore) Query(ctx context.Context, userID, query string, topK int) ([]VectorResult, error) {
	if err := auth.CheckUserScope(ctx, userID); err != nil {
		return nil, err
	}

	queryVec := hashEmbed(query, s.dim)

	s.mu.RLock()
	entries := s.entries[userID]
	s.mu.RUnlock()

	if len(entries) == 0 {
		return nil, nil
	}

	type scored struct {
		entry vectorEntry
		score float32
	}

	results := make([]scored, 0, len(entries))
	for _, e := range entries {
		score := cosineSimilarity(queryVec, e.vector)
		results = append(results, scored{entry: e, score: score})
	}

	// Sort by score descending
	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})

	// Return top-K
	if topK > len(results) {
		topK = len(results)
	}

	out := make([]VectorResult, 0, topK)
	for i := 0; i < topK; i++ {
		r := results[i]
		meta := r.entry.metadata
		if meta == nil {
			meta = map[string]any{}
		}
		out = append(out, VectorResult{
			Content:  r.entry.content,
			Score:    r.score,
			Metadata: meta,
		})
	}
	return out, nil
}

// DeleteUser removes all vector entries for a user.
func (s *InMemoryVectorStore) DeleteUser(ctx context.Context, userID string) error {
	if err := auth.CheckUserScope(ctx, userID); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, userID)
	return nil
}

// --- Hash-based pseudo-embedding ---

// hashEmbed generates a deterministic pseudo-embedding vector from text.
// It uses FNV-1a hash on word-level n-grams to create a sparse vector.
// This is NOT a semantic embedding — similar text gets similar vectors
// only if they share words. It serves as a zero-dependency fallback.
func hashEmbed(text string, dim int) []float32 {
	vec := make([]float32, dim)

	// Simple word-level hashing: each word influences several dimensions
	words := tokenize(text)
	for _, word := range words {
		if len(word) == 0 {
			continue
		}
		// Hash the word to get dimension indices
		h := fnvHash(word)
		// Use 3 different positions per word for better coverage
		for i := 0; i < 3; i++ {
			idx := int((h + uint32(i)*7919) % uint32(dim))
			vec[idx] += 1.0
		}
	}

	// Also hash character bigrams for partial word matches
	runes := []rune(text)
	for i := 0; i < len(runes)-1; i++ {
		bigram := string(runes[i]) + string(runes[i+1])
		h := fnvHash(bigram)
		idx := int(h % uint32(dim))
		vec[idx] += 0.5
	}

	// Normalize to unit vector
	normalize(vec)
	return vec
}

func tokenize(text string) []string {
	var words []string
	var current []rune
	for _, r := range text {
		if isWordChar(r) {
			current = append(current, r)
		} else {
			if len(current) > 0 {
				words = append(words, string(current))
				current = nil
			}
		}
	}
	if len(current) > 0 {
		words = append(words, string(current))
	}
	return words
}

// isWordChar reports whether a rune belongs to a word for the hash embedder.
//
// It used to be `... || r > 0x4e00`, which is wrong in both directions:
// U+4E00 is 一, so `>` excluded it and a document containing 一 lost that
// character ("一二三" tokenised to just ["二三"]); and every rune above the
// threshold counted as a word character, so fullwidth punctuation was glued into
// tokens ("写，注意先切流量" became a single word). unicode.IsLetter/IsDigit is
// what the context manager's counter already uses, so the two now agree.
func isWordChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

// overlapTokens splits text for the lexical-overlap channel used by retrieval
// scoring.
//
// It is deliberately NOT tokenize. tokenize feeds hashEmbed, and the hash-mode
// relevance cut-off (VECTOR_MIN_SCORE = 0.1) was measured against the score
// distribution the current tokenization produces — changing it would move that
// distribution, so the cut-off would have to be re-measured. Nothing persists
// vectors (both backends are in-process and rebuilt from KV text), so the cost
// of changing it is the calibration, not a migration.
//
// The difference is CJK. tokenize emits a whole Chinese run as a single token,
// so a Chinese query can only match an entry containing that exact run: measured
// overlap for a natural Chinese query was 0, which made the query-aware half of
// the scoring dead for the language most of this project's data is in. Here CJK
// runs contribute character unigrams and bigrams — the standard cheap approach
// for CJK retrieval — so "帮我写个部署脚本" and "部署脚本要用 Go 写" share
// 部署 / 署脚 / 脚本.
func overlapTokens(text string) []string {
	runes := []rune(strings.ToLower(text))
	out := make([]string, 0, len(runes))
	var word []rune
	flush := func() {
		if len(word) > 0 {
			out = append(out, string(word))
			word = nil
		}
	}
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case isASCIIWordRune(r):
			word = append(word, r)
		case isCJKRune(r):
			flush()
			out = append(out, string(r))
			if i+1 < len(runes) && isCJKRune(runes[i+1]) {
				out = append(out, string(runes[i:i+2]))
			}
		default:
			flush()
		}
	}
	flush()
	return out
}

func isASCIIWordRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_'
}

func isCJKRune(r rune) bool {
	return unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r)
}

func fnvHash(s string) uint32 {
	h := uint32(2166136261)
	for _, b := range []byte(s) {
		h ^= uint32(b)
		h *= 16777619
	}
	return h
}

func normalize(vec []float32) {
	var norm float64
	for _, v := range vec {
		norm += float64(v) * float64(v)
	}
	if norm == 0 {
		return
	}
	norm = math.Sqrt(norm)
	for i := range vec {
		vec[i] = float32(float64(vec[i]) / norm)
	}
}

func cosineSimilarity(a, b []float32) float32 {
	if len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := 0; i < len(a); i++ {
		va := float64(a[i])
		vb := float64(b[i])
		dot += va * vb
		normA += va * va
		normB += vb * vb
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return float32(dot / (math.Sqrt(normA) * math.Sqrt(normB)))
}

// Ensure InMemoryVectorStore implements VectorStore
var _ VectorStore = (*InMemoryVectorStore)(nil)

// FormatVectorResults formats vector query results as readable text for system prompt injection.
func FormatVectorResults(results []VectorResult) string {
	return FormatVectorResultsWithin(results, 0, 0)
}

// FormatVectorResultsWithin formats vector results for injection, skipping the
// entries that would push the text past maxTokens (0 = no limit) or that score
// below minScore (0 = use the default threshold).
//
// The threshold has to match the embedder's score scale: real embeddings put a
// relevant pair around 0.6-0.9, while the hash fallback compresses everything
// into roughly 0.15-0.4, so one fixed cut-off cannot serve both.
//
// Whole entries are skipped rather than truncating the text: a recalled episode
// cut in half reads as a different fact. Results arrive ranked, so higher-ranked
// entries claim the budget first; an oversized entry is skipped in favour of
// shorter ones instead of wasting the remaining budget.
func FormatVectorResultsWithin(results []VectorResult, maxTokens int, minScore float64) string {
	if len(results) == 0 {
		return ""
	}
	if minScore <= 0 {
		minScore = minVectorRelevance
	}

	const header = "用户历史相关记忆：\n"
	used := 0
	if maxTokens > 0 {
		used = contextmgr.CountText(header)
	}

	var lines []string
	for _, r := range results {
		if float64(r.Score) < minScore {
			continue
		}
		ts := ""
		if t, ok := r.Metadata["timestamp"]; ok {
			ts = fmt.Sprintf(" (%v)", t)
		}
		line := fmt.Sprintf("- %s%s [相关度: %.0f%%]", r.Content, ts, r.Score*100)
		if maxTokens > 0 {
			cost := contextmgr.CountText(line)
			if used+cost > maxTokens {
				continue
			}
			used += cost
		}
		lines = append(lines, line)
	}

	if len(lines) == 0 {
		return ""
	}

	return header + strings.Join(lines, "\n")
}
