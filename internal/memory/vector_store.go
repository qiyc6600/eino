package memory

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"
)

// VectorResult holds a single result from a vector similarity query.
type VectorResult struct {
	Content  string         `json:"content"`   // 原始文本
	Score    float32        `json:"score"`     // 相似度分数 0-1
	Metadata map[string]any `json:"metadata"`  // 元数据 (thread_id, timestamp 等)
}

// VectorStore is the interface for semantic memory retrieval.
// Implementations store text content with embedding vectors and support
// similarity-based queries. All methods are namespaced by userID for isolation.
type VectorStore interface {
	// Store saves a memory entry with its embedding vector.
	Store(ctx context.Context, userID, content string, metadata map[string]any) error
	// Query returns the top-K most similar memories for a given query text.
	Query(ctx context.Context, userID, query string, topK int) ([]VectorResult, error)
	// Delete removes all vector entries for a user (e.g. on privacy request).
	DeleteUser(ctx context.Context, userID string) error
}

// --- Simple InMemoryVectorStore (no external dependencies) ---
// Uses a hash-based pseudo-embedding for vectors. This is NOT a real semantic
// embedding — it provides deterministic but crude similarity based on word overlap.
// For proper semantic search, use the chromem-go implementation.

type vectorEntry struct {
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

// NewInMemoryVectorStore creates a new simple in-memory vector store.
func NewInMemoryVectorStore() *InMemoryVectorStore {
	return &InMemoryVectorStore{
		entries: make(map[string][]vectorEntry),
		dim:     128, // hash embedding dimension
	}
}

// Store saves a text entry with a hash-based pseudo-embedding.
func (s *InMemoryVectorStore) Store(ctx context.Context, userID, content string, metadata map[string]any) error {
	vector := hashEmbed(content, s.dim)

	s.mu.Lock()
	defer s.mu.Unlock()

	s.entries[userID] = append(s.entries[userID], vectorEntry{
		content:  content,
		vector:   vector,
		metadata: metadata,
		addedAt:  time.Now(),
	})
	return nil
}

// Query returns the top-K most similar entries for a query string.
func (s *InMemoryVectorStore) Query(ctx context.Context, userID, query string, topK int) ([]VectorResult, error) {
	queryVec := hashEmbed(query, s.dim)

	s.mu.RLock()
	entries := s.entries[userID]
	s.mu.RUnlock()

	if len(entries) == 0 {
		return nil, nil
	}

	type scored struct {
		entry  vectorEntry
		score  float32
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

func isWordChar(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r > 0x4e00
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
	if len(results) == 0 {
		return ""
	}

	var lines []string
	for _, r := range results {
		if r.Score >= 0.3 { // Only include results above minimum relevance threshold
			ts := ""
			if t, ok := r.Metadata["timestamp"]; ok {
				ts = fmt.Sprintf(" (%v)", t)
			}
			lines = append(lines, fmt.Sprintf("- %s%s [相关度: %.0f%%]", r.Content, ts, r.Score*100))
		}
	}

	if len(lines) == 0 {
		return ""
	}

	return "用户历史相关记忆：\n" + stringsJoin(lines, "\n")
}

// stringsJoin avoids importing strings package for a single use.
func stringsJoin(elems []string, sep string) string {
	switch len(elems) {
	case 0:
		return ""
	case 1:
		return elems[0]
	}
	n := len(sep) * (len(elems) - 1)
	for i := 0; i < len(elems); i++ {
		n += len(elems[i])
	}
	var b []byte
	b = append(b, elems[0]...)
	for _, s := range elems[1:] {
		b = append(b, sep...)
		b = append(b, s...)
	}
	return string(b)
}
