package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/example/agent-eino-demo/internal/auth"
)

// Document is one ingested text document, owned by a single user.
type Document struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Chunks    int    `json:"chunks"`
	Chars     int    `json:"chars"`
	CreatedAt string `json:"created_at"`
}

// Documents reuse the reserved-key convention: they live in the same per-user
// store as memories, so they inherit user isolation and the configured backend
// (memory / file / PostgreSQL) with no new storage code, while the existing
// reserved-key filters keep them out of the memory list, retrieval,
// consolidation and the memory write API.
const (
	documentKeyPrefix = ReservedKeyPrefix + "doc_"
	chunkKeyPrefix    = ReservedKeyPrefix + "chunk_"
)

// Chunking parameters. The target keeps a chunk small enough that several fit a
// retrieval budget, and the overlap means a sentence split across a boundary is
// still reachable from one side.
const (
	chunkTargetRunes  = 400
	chunkOverlapRunes = 80
)

// ErrDocumentNotFound is returned when a document does not exist for the caller.
var ErrDocumentNotFound = fmt.Errorf("document not found")

func documentKey(id string) string { return documentKeyPrefix + id }

func chunkKey(docID string, index int) string {
	return fmt.Sprintf("%s%s_%d", chunkKeyPrefix, docID, index)
}

// chunkVectorID is the id a chunk carries in the vector index. It is derived
// from the document and chunk so re-indexing is idempotent and deleting a
// document can remove exactly its chunks.
func chunkVectorID(docID string, index int) string {
	return fmt.Sprintf("doc_%s_%d", docID, index)
}

// IngestDocument stores a document and its chunks, and indexes the chunks for
// retrieval. Re-uploading the same name creates a second document rather than
// replacing the first.
func (s *Service) IngestDocument(ctx context.Context, userID, name, content string) (Document, error) {
	if err := auth.CheckUserScope(ctx, userID); err != nil {
		return Document{}, err
	}
	name = strings.TrimSpace(name)
	content = strings.TrimSpace(content)
	if name == "" {
		return Document{}, fmt.Errorf("文档名称不能为空")
	}
	if content == "" {
		return Document{}, fmt.Errorf("文档内容不能为空")
	}

	chunks := ChunkText(content)
	if len(chunks) == 0 {
		return Document{}, fmt.Errorf("文档内容不能为空")
	}

	doc := Document{
		ID:        "d_" + uuid.NewString(),
		Name:      name,
		Chunks:    len(chunks),
		Chars:     len([]rune(content)),
		CreatedAt: time.Now().Format(time.RFC3339),
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return Document{}, err
	}

	// Chunk text first: a failure part-way leaves chunks without metadata, which
	// list and delete both ignore, rather than metadata advertising chunks that
	// were never stored.
	for i, chunk := range chunks {
		if err := s.store.Put(ctx, MemoryEntry{
			UserID: userID, Key: chunkKey(doc.ID, i), Value: chunk,
			Source: "document", Type: MemoryTypeFact, Importance: DefaultImportance,
			CreatedAt: doc.CreatedAt, UpdatedAt: doc.CreatedAt,
		}); err != nil {
			return Document{}, err
		}
	}
	if err := s.store.Put(ctx, MemoryEntry{
		UserID: userID, Key: documentKey(doc.ID), Value: string(raw),
		Source: "document", Type: MemoryTypeFact, Importance: DefaultImportance,
		CreatedAt: doc.CreatedAt, UpdatedAt: doc.CreatedAt,
	}); err != nil {
		return Document{}, err
	}

	s.indexDocumentChunks(ctx, userID, doc, chunks)
	return doc, nil
}

// indexDocumentChunks adds a document's chunks to the document vector index.
// A missing index is not an error: the document stays fully stored and
// retrievable by name, it just cannot be recalled semantically.
func (s *Service) indexDocumentChunks(ctx context.Context, userID string, doc Document, chunks []string) {
	if s.docStore == nil {
		return
	}
	for i, chunk := range chunks {
		_ = s.docStore.StoreWithID(ctx, userID, chunkVectorID(doc.ID, i), chunk, map[string]any{
			"type":        "document",
			"document_id": doc.ID,
			"name":        doc.Name,
			"chunk":       i + 1,
		})
	}
}

// ListDocuments returns the caller's documents, newest first.
func (s *Service) ListDocuments(ctx context.Context, userID string) ([]Document, error) {
	if err := auth.CheckUserScope(ctx, userID); err != nil {
		return nil, err
	}
	entries, err := s.store.List(ctx, userID)
	if err != nil {
		return nil, err
	}

	docs := make([]Document, 0)
	for _, e := range entries {
		if !strings.HasPrefix(e.Key, documentKeyPrefix) {
			continue
		}
		var doc Document
		if err := json.Unmarshal([]byte(e.Value), &doc); err != nil {
			continue
		}
		docs = append(docs, doc)
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].CreatedAt > docs[j].CreatedAt })
	return docs, nil
}

// DeleteDocument removes a document, its chunks and its vector entries.
func (s *Service) DeleteDocument(ctx context.Context, userID, id string) error {
	if err := auth.CheckUserScope(ctx, userID); err != nil {
		return err
	}
	entry, ok, err := s.store.Get(ctx, userID, documentKey(id))
	if err != nil {
		return err
	}
	if !ok {
		return ErrDocumentNotFound
	}
	var doc Document
	if err := json.Unmarshal([]byte(entry.Value), &doc); err != nil {
		return fmt.Errorf("文档元数据损坏：%w", err)
	}

	vectorIDs := make([]string, 0, doc.Chunks)
	for i := 0; i < doc.Chunks; i++ {
		vectorIDs = append(vectorIDs, chunkVectorID(id, i))
		if err := s.store.Delete(ctx, userID, chunkKey(id, i)); err != nil {
			return err
		}
	}
	if err := s.store.Delete(ctx, userID, documentKey(id)); err != nil {
		return err
	}
	// The vector index is derived state; a failure here leaves stale entries that
	// the KV store no longer backs, so they are dropped on the next rebuild.
	if s.docStore != nil {
		_ = s.docStore.Delete(ctx, userID, vectorIDs...)
	}
	return nil
}

// ChunkText splits text into overlapping chunks, preferring paragraph
// boundaries. A paragraph longer than the target is hard-split so one huge block
// cannot become a single oversized chunk.
func ChunkText(text string) []string {
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	normalized = strings.TrimSpace(normalized)
	if normalized == "" {
		return nil
	}

	var segments []string
	for _, para := range strings.Split(normalized, "\n\n") {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		segments = append(segments, hardSplit(para, chunkTargetRunes)...)
	}

	var chunks []string
	var current []rune
	flush := func() {
		if len(current) == 0 {
			return
		}
		chunks = append(chunks, strings.TrimSpace(string(current)))
		current = nil
	}
	for _, seg := range segments {
		segRunes := []rune(seg)
		if len(current) > 0 && len(current)+1+len(segRunes) > chunkTargetRunes {
			flush()
		}
		if len(current) > 0 {
			current = append(current, '\n')
		}
		current = append(current, segRunes...)
	}
	flush()

	if chunkOverlapRunes <= 0 || len(chunks) < 2 {
		return chunks
	}
	// Carry the tail of each chunk into the next one.
	overlapped := make([]string, 0, len(chunks))
	for i, chunk := range chunks {
		if i == 0 {
			overlapped = append(overlapped, chunk)
			continue
		}
		prev := []rune(chunks[i-1])
		start := len(prev) - chunkOverlapRunes
		if start < 0 {
			start = 0
		}
		overlapped = append(overlapped, string(prev[start:])+"\n"+chunk)
	}
	return overlapped
}

// hardSplit cuts a block into target-sized pieces. Overlap is added later, at
// the chunk level, so pieces here are exact slices.
func hardSplit(block string, size int) []string {
	runes := []rune(block)
	if len(runes) <= size {
		return []string{block}
	}
	out := make([]string, 0, len(runes)/size+1)
	for start := 0; start < len(runes); start += size {
		end := start + size
		if end > len(runes) {
			end = len(runes)
		}
		out = append(out, string(runes[start:end]))
	}
	return out
}

// parseChunkKey recovers the document id and chunk index from a chunk key.
func parseChunkKey(key string) (docID string, index int, ok bool) {
	rest := strings.TrimPrefix(key, chunkKeyPrefix)
	if rest == key {
		return "", 0, false
	}
	sep := strings.LastIndex(rest, "_")
	if sep <= 0 {
		return "", 0, false
	}
	index, err := strconv.Atoi(rest[sep+1:])
	if err != nil || index < 0 {
		return "", 0, false
	}
	return rest[:sep], index, true
}
