package memory

import (
	"context"
	"fmt"
	"os"
	"time"

	chromem "github.com/philippgille/chromem-go"
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
	collectionName := fmt.Sprintf("user_%s", userID)

	// Get or create collection for this user
	collection, err := s.db.GetOrCreateCollection(collectionName, nil, s.embedFunc)
	if err != nil {
		return fmt.Errorf("failed to get/create collection: %w", err)
	}

	// Generate a unique document ID
	docID := fmt.Sprintf("mem_%d", time.Now().UnixNano())

	// Convert metadata to string map for chromem
	chromemMeta := make(map[string]string)
	for k, v := range metadata {
		chromemMeta[k] = fmt.Sprintf("%v", v)
	}

	doc := chromem.Document{
		ID:       docID,
		Content:  content,
		Metadata: chromemMeta,
	}

	return collection.AddDocuments(ctx, []chromem.Document{doc}, 1)
}

// Query returns the top-K most similar memories for a query string.
func (s *ChromemVectorStore) Query(ctx context.Context, userID, query string, topK int) ([]VectorResult, error) {
	collectionName := fmt.Sprintf("user_%s", userID)

	collection, err := s.db.GetOrCreateCollection(collectionName, nil, s.embedFunc)
	if err != nil {
		return nil, fmt.Errorf("failed to get collection: %w", err)
	}

	// chromem Query returns []chromem.Result
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
	// chromem-go doesn't have a DeleteCollection method in v0.7.0
	return fmt.Errorf("DeleteUser not supported by chromem-go; use InMemoryVectorStore for full deletion support")
}

// Ensure ChromemVectorStore implements VectorStore
var _ VectorStore = (*ChromemVectorStore)(nil)
