package memory

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	chromem "github.com/philippgille/chromem-go"

	"github.com/example/agent-eino-demo/internal/auth"
)

// ChromemVectorStore implements VectorStore using chromem-go with real embeddings.
// Supports Ollama (local) and OpenAI-compatible (cloud) embedding providers.
type ChromemVectorStore struct {
	db        *chromem.DB
	embedFunc chromem.EmbeddingFunc
}

// NewChromemVectorStoreWithOllama creates a ChromemVectorStore using Ollama for embeddings.
// Ollama must be running locally with an embedding model pulled (e.g. nomic-embed-text).
// If Ollama is not available, returns an error — use NewChromemVectorStoreWithOpenAI as fallback.
func NewChromemVectorStoreWithOllama(modelName string) (*ChromemVectorStore, error) {
	ollamaURL := os.Getenv("OLLAMA_BASE_URL")
	if ollamaURL == "" {
		ollamaURL = "http://localhost:11434"
	}

	if modelName == "" {
		modelName = "nomic-embed-text"
	}

	embedFunc := chromem.NewEmbeddingFuncOllama(modelName, ollamaURL)

	db := chromem.NewDB()

	return &ChromemVectorStore{
		db:        db,
		embedFunc: embedFunc,
	}, nil
}

// NewChromemVectorStoreWithOpenAI creates a ChromemVectorStore using OpenAI-compatible API for embeddings.
// Uses OPENAI_BASE_URL and OPENAI_API_KEY environment variables.
func NewChromemVectorStoreWithOpenAI(modelName, baseURL, apiKey string) (*ChromemVectorStore, error) {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	if apiKey == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY is required for OpenAI embedding")
	}
	if modelName == "" {
		modelName = "text-embedding-3-small"
	}

	embedFunc := chromem.NewEmbeddingFuncOpenAICompat(baseURL, apiKey, modelName, nil)

	db := chromem.NewDB()

	return &ChromemVectorStore{
		db:        db,
		embedFunc: embedFunc,
	}, nil
}

// Store saves a memory entry with its embedding vector via chromem-go.
func (s *ChromemVectorStore) Store(ctx context.Context, userID, content string, metadata map[string]any) error {
	return s.StoreWithID(ctx, userID, fmt.Sprintf("mem_%d", time.Now().UnixNano()), content, metadata)
}

// StoreWithID saves an entry under the given id, replacing any document that
// already uses it (chromem overwrites on a repeated id, which makes re-indexing
// idempotent).
func (s *ChromemVectorStore) StoreWithID(ctx context.Context, userID, id, content string, metadata map[string]any) error {
	if err := auth.CheckUserScope(ctx, userID); err != nil {
		return err
	}
	if id == "" {
		return fmt.Errorf("vector entry id must not be empty")
	}

	collection, err := s.db.GetOrCreateCollection(s.collectionName(userID), nil, s.embedFunc)
	if err != nil {
		return fmt.Errorf("failed to get/create collection: %w", err)
	}

	doc := chromem.Document{
		ID:       id,
		Content:  content,
		Metadata: chromemMetadata(metadata),
	}

	return collection.AddDocuments(ctx, []chromem.Document{doc}, 1)
}

// Delete removes the named entries. Unknown ids are ignored.
func (s *ChromemVectorStore) Delete(ctx context.Context, userID string, ids ...string) error {
	if err := auth.CheckUserScope(ctx, userID); err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}

	collection, err := s.db.GetOrCreateCollection(s.collectionName(userID), nil, s.embedFunc)
	if err != nil {
		return fmt.Errorf("failed to get collection: %w", err)
	}
	// chromem requires at least one selector; ids alone is the precise form.
	return collection.Delete(ctx, nil, nil, ids...)
}

// Query returns the top-K most similar memories for a query string.
func (s *ChromemVectorStore) Query(ctx context.Context, userID, query string, topK int) ([]VectorResult, error) {
	if err := auth.CheckUserScope(ctx, userID); err != nil {
		return nil, err
	}
	// chromem rejects an empty query text, and the token-bar path calls with an
	// empty query — returning nothing is the correct answer there.
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}

	collection, err := s.db.GetOrCreateCollection(s.collectionName(userID), nil, s.embedFunc)
	if err != nil {
		return nil, fmt.Errorf("failed to get collection: %w", err)
	}

	// chromem errors when nResults exceeds the number of stored documents, which
	// a small corpus hits immediately. Clamping here keeps recall working instead
	// of failing the whole query.
	count := collection.Count()
	if count == 0 {
		return nil, nil
	}
	if topK > count {
		topK = count
	}

	results, err := collection.Query(ctx, query, topK, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("vector query failed: %w", err)
	}

	out := make([]VectorResult, 0, len(results))
	for _, r := range results {
		meta := make(map[string]any)
		for k, v := range r.Metadata {
			meta[k] = v
		}
		out = append(out, VectorResult{
			Content:  r.Content,
			Score:    r.Similarity,
			Metadata: meta,
		})
	}
	return out, nil
}

// DeleteUser removes all vector entries for a user by deleting their collection.
func (s *ChromemVectorStore) DeleteUser(ctx context.Context, userID string) error {
	if err := auth.CheckUserScope(ctx, userID); err != nil {
		return err
	}
	return s.db.DeleteCollection(s.collectionName(userID))
}

// collectionName scopes a chromem collection to one user.
func (s *ChromemVectorStore) collectionName(userID string) string {
	return fmt.Sprintf("user_%s", userID)
}

// chromemMetadata flattens metadata to the string map chromem requires.
func chromemMetadata(metadata map[string]any) map[string]string {
	out := make(map[string]string, len(metadata))
	for k, v := range metadata {
		out[k] = fmt.Sprintf("%v", v)
	}
	return out
}

// Ensure ChromemVectorStore implements VectorStore
var _ VectorStore = (*ChromemVectorStore)(nil)
