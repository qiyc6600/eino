package memory

import (
	"context"
	"errors"
	"github.com/example/agent-eino-demo/internal/contextmgr"
	"strings"
	"testing"
	"time"
)

func TestChunkText(t *testing.T) {
	t.Run("empty input yields no chunks", func(t *testing.T) {
		for _, in := range []string{"", "   ", "\n\n\n", "\r\n  \r\n"} {
			if got := ChunkText(in); len(got) != 0 {
				t.Fatalf("ChunkText(%q) = %v, want none", in, got)
			}
		}
	})

	t.Run("short text stays one chunk", func(t *testing.T) {
		got := ChunkText("一段很短的文字。")
		if len(got) != 1 || got[0] != "一段很短的文字。" {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("paragraphs are packed up to the target", func(t *testing.T) {
		var paras []string
		for i := 0; i < 6; i++ {
			paras = append(paras, strings.Repeat("段落内容。", 20)) // ~100 runes each
		}
		chunks := ChunkText(strings.Join(paras, "\n\n"))
		if len(chunks) < 2 {
			t.Fatalf("expected the text to split, got %d chunk(s)", len(chunks))
		}
		// Every chunk except the last must be at or under the target (plus the
		// overlap and its separator).
		for i, c := range chunks[:len(chunks)-1] {
			if n := len([]rune(c)); n > chunkTargetRunes+chunkOverlapRunes+1 {
				t.Fatalf("chunk %d is %d runes, over the target", i, n)
			}
		}
	})

	t.Run("a paragraph longer than the target is hard-split", func(t *testing.T) {
		huge := strings.Repeat("很长的一段没有换行。", 200) // ~2000 runes
		chunks := ChunkText(huge)
		if len(chunks) < 2 {
			t.Fatalf("expected a hard split, got %d chunk(s)", len(chunks))
		}
		for i, c := range chunks {
			if n := len([]rune(c)); n > chunkTargetRunes+chunkOverlapRunes+1 {
				t.Fatalf("chunk %d is %d runes, over the target", i, n)
			}
		}
	})

	t.Run("consecutive chunks overlap", func(t *testing.T) {
		var paras []string
		for i := 0; i < 6; i++ {
			paras = append(paras, "独特标记"+strings.Repeat("填充内容。", 20))
		}
		chunks := ChunkText(strings.Join(paras, "\n\n"))
		if len(chunks) < 2 {
			t.Fatalf("expected multiple chunks, got %d", len(chunks))
		}
		// The tail of chunk i must reappear at the head of chunk i+1.
		prev := []rune(chunks[0])
		tail := string(prev[len(prev)-chunkOverlapRunes:])
		if !strings.HasPrefix(chunks[1], tail) {
			t.Fatalf("chunk 1 does not start with the overlap tail of chunk 0:\n%q\n%q", tail, chunks[1])
		}
	})
}

// TestDocuments_IngestListDelete covers the CRUD path and the reserved-key
// convention: documents live in the memory store but must stay out of the
// user-visible memory list.
func TestDocuments_IngestListDelete(t *testing.T) {
	ctx := context.Background()
	svc := NewService(NewInMemoryMemoryStore(), nil, NewInMemoryVectorStore(), nil)
	svc.SetDocumentStore(NewInMemoryVectorStore())

	doc, err := svc.IngestDocument(ctx, "u1", "部署手册", "第一段内容。\n\n第二段内容。")
	if err != nil {
		t.Fatal(err)
	}
	if doc.ID == "" || doc.Name != "部署手册" || doc.Chunks == 0 || doc.Chars == 0 {
		t.Fatalf("unexpected document: %+v", doc)
	}

	docs, err := svc.ListDocuments(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || docs[0].ID != doc.ID {
		t.Fatalf("expected the document to be listed, got %+v", docs)
	}

	// Documents must not leak into the memory list.
	entries, err := svc.ListPreferences(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("document entries leaked into the memory list: %+v", entries)
	}

	if err := svc.DeleteDocument(ctx, "u1", doc.ID); err != nil {
		t.Fatal(err)
	}
	docs, _ = svc.ListDocuments(ctx, "u1")
	if len(docs) != 0 {
		t.Fatalf("document survived deletion: %+v", docs)
	}
	if err := svc.DeleteDocument(ctx, "u1", doc.ID); !errors.Is(err, ErrDocumentNotFound) {
		t.Fatalf("expected ErrDocumentNotFound on a second delete, got %v", err)
	}
}

// TestDocuments_UserIsolation asserts one user's documents are invisible to
// another, including through deletion.
func TestDocuments_UserIsolation(t *testing.T) {
	ctx := context.Background()
	svc := NewService(NewInMemoryMemoryStore(), nil, NewInMemoryVectorStore(), nil)

	doc, err := svc.IngestDocument(ctx, "u1", "私密文档", "只有 u1 能看到的内容。")
	if err != nil {
		t.Fatal(err)
	}

	docs, err := svc.ListDocuments(ctx, "u2")
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 0 {
		t.Fatalf("another user's documents are visible: %+v", docs)
	}

	// Deleting someone else's document must read as missing, not succeed.
	if err := svc.DeleteDocument(ctx, "u2", doc.ID); !errors.Is(err, ErrDocumentNotFound) {
		t.Fatalf("cross-user delete should report not found, got %v", err)
	}
	if docs, _ = svc.ListDocuments(ctx, "u1"); len(docs) != 1 {
		t.Fatalf("the owner's document was removed by another user: %+v", docs)
	}
}

func TestDocuments_Validation(t *testing.T) {
	ctx := context.Background()
	svc := NewService(NewInMemoryMemoryStore(), nil, NewInMemoryVectorStore(), nil)

	for _, tc := range []struct{ name, doc, content string }{
		{"empty name", "  ", "内容"},
		{"empty content", "名称", "   "},
	} {
		if _, err := svc.IngestDocument(ctx, "u1", tc.doc, tc.content); err == nil {
			t.Fatalf("%s: expected an error", tc.name)
		}
	}
}

// TestRetrieveRelevant_DocumentSection covers the document half of the injected
// context: numbered citations, its own budget, and the off switch.
func TestRetrieveRelevant_DocumentSection(t *testing.T) {
	ctx := context.Background()

	newService := func(t *testing.T) *Service {
		t.Helper()
		svc := NewService(NewInMemoryMemoryStore(), nil, nil, nil)
		svc.SetDocumentStore(NewInMemoryVectorStore())
		// The hash fallback's scores are compressed, so it uses the lower cut-off
		// the app wiring picks for that provider.
		svc.SetVectorMinScore(0.1)
		return svc
	}

	t.Run("chunks are injected with citation numbers", func(t *testing.T) {
		svc := newService(t)
		if _, err := svc.IngestDocument(ctx, "u1", "运维手册", "重启服务前先确认备份完成。\n\n备份失败时不要继续。"); err != nil {
			t.Fatal(err)
		}
		out := svc.RetrieveRelevant(ctx, "u1", "重启服务前要做什么", 0, false)
		if !strings.Contains(out, "【文档片段】") {
			t.Fatalf("document section missing:\n%s", out)
		}
		if !strings.Contains(out, "[1] 运维手册") {
			t.Fatalf("expected a numbered citation:\n%s", out)
		}
		if !strings.Contains(out, "备份") {
			t.Fatalf("expected the chunk text:\n%s", out)
		}
	})

	t.Run("zero budget disables the document section", func(t *testing.T) {
		svc := newService(t)
		if _, err := svc.IngestDocument(ctx, "u1", "运维手册", "重启服务前先确认备份完成。"); err != nil {
			t.Fatal(err)
		}
		svc.SetDocumentBudgetTokens(0)
		if out := svc.RetrieveRelevant(ctx, "u1", "重启服务前要做什么", 0, false); strings.Contains(out, "【文档片段】") {
			t.Fatalf("document retrieval should be off at budget 0:\n%s", out)
		}
	})

	t.Run("the document section respects its own budget", func(t *testing.T) {
		svc := newService(t)
		if _, err := svc.IngestDocument(ctx, "u1", "短手册", "重启服务前先确认备份完成。"); err != nil {
			t.Fatal(err)
		}

		// A budget with room for the one chunk keeps it.
		svc.SetDocumentBudgetTokens(200)
		out := svc.RetrieveRelevant(ctx, "u1", "重启服务前要做什么", 0, false)
		idx := strings.Index(out, "【文档片段】")
		if idx < 0 {
			t.Fatalf("a sufficient budget should keep the chunk:\n%s", out)
		}
		if got := contextmgr.CountText(out[idx:]); got > 200 {
			t.Fatalf("document section is %d tokens, over its 200 budget", got)
		}

		// A budget too small for any chunk drops the section rather than
		// overflowing it.
		svc.SetDocumentBudgetTokens(5)
		if out := svc.RetrieveRelevant(ctx, "u1", "重启服务前要做什么", 0, false); strings.Contains(out, "【文档片段】") {
			t.Fatalf("a 5-token budget cannot fit a chunk, yet one was injected:\n%s", out)
		}
	})

	t.Run("no documents means no section", func(t *testing.T) {
		svc := newService(t)
		if out := svc.RetrieveRelevant(ctx, "u1", "任意问题", 0, false); strings.Contains(out, "【文档片段】") {
			t.Fatalf("unexpected document section:\n%s", out)
		}
	})
}

// TestEnsureVectorIndex_RebuildsAfterRestart covers the lazy rebuild: the vector
// backends are in-process, so a restart leaves the stored text without its
// vectors. Recall must come back on the first query.
func TestEnsureVectorIndex_RebuildsAfterRestart(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryMemoryStore()
	svc := NewService(store, nil, NewInMemoryVectorStore(), nil)
	svc.SetDocumentStore(NewInMemoryVectorStore())
	svc.SetVectorMinScore(0.1)

	if _, err := svc.IngestDocument(ctx, "u1", "运维手册", "重启服务前先确认备份完成。"); err != nil {
		t.Fatal(err)
	}
	if out := svc.RetrieveRelevant(ctx, "u1", "重启服务前要做什么", 0, false); !strings.Contains(out, "【文档片段】") {
		t.Fatalf("document recall did not work before the restart:\n%s", out)
	}

	// Simulate a restart: the KV store survives (same store), the vector indexes
	// do not (fresh service, fresh stores).
	restarted := NewService(store, nil, NewInMemoryVectorStore(), nil)
	restarted.SetDocumentStore(NewInMemoryVectorStore())
	restarted.SetVectorMinScore(0.1)
	out := restarted.RetrieveRelevant(ctx, "u1", "重启服务前要做什么", 0, false)
	if !strings.Contains(out, "【文档片段】") {
		t.Fatalf("document recall was not rebuilt after restart:\n%s", out)
	}
	if !strings.Contains(out, "[1] 运维手册") {
		t.Fatalf("rebuilt chunk lost its document name:\n%s", out)
	}
}

// TestVectorMinScore_MustMatchEmbedderScale pins the reason the cut-off is
// configurable, using fixed scores so it does not depend on hash-embedding
// quirks: the same result is discarded at the default threshold and kept at the
// lower one the hash provider uses.
func TestVectorMinScore_MustMatchEmbedderScale(t *testing.T) {
	ctx := context.Background()

	build := func(minScore float64) string {
		svc := NewService(NewInMemoryMemoryStore(), nil, nil, nil)
		svc.SetDocumentStore(&stubVectorStore{results: []VectorResult{
			{Content: "相关片段内容", Score: 0.2, Metadata: map[string]any{"name": "手册", "chunk": 1}},
		}})
		if minScore > 0 {
			svc.SetVectorMinScore(minScore)
		}
		return svc.RetrieveRelevant(ctx, "u1", "任意查询", 0, false)
	}

	// The default cut-off is calibrated for real embeddings; the hash fallback's
	// scores sit below it.
	if out := build(0); strings.Contains(out, "【文档片段】") {
		t.Fatalf("the default threshold should discard a 0.20 match:\n%s", out)
	}
	if out := build(0.1); !strings.Contains(out, "相关片段内容") {
		t.Fatalf("the lower threshold should keep a 0.20 match:\n%s", out)
	}
}

// TestEnsureVectorIndex_RebuildsEpisodes covers the same rebuild for conversation
// episodes, which had the identical problem: the KV entry survives a restart
// while its vector does not.
//
// It asserts on the vector index itself — the KV text is returned either way, so
// checking the injected string would pass without any rebuild happening.
func TestEnsureVectorIndex_RebuildsEpisodes(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryMemoryStore()
	now := time.Now().Format(time.RFC3339)
	if err := store.Put(ctx, MemoryEntry{
		UserID: "u1", Key: "ep_20260101000000", Value: "冒烟测试记录",
		Type: MemoryTypeEpisode, Importance: 2, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	vec := NewInMemoryVectorStore()
	svc := NewService(store, nil, vec, nil)

	// Nothing is indexed until a retrieval needs it.
	if results, _ := vec.Query(ctx, "u1", "冒烟测试记录", 5); len(results) != 0 {
		t.Fatalf("expected an empty index before the rebuild, got %d entries", len(results))
	}

	svc.RetrieveRelevant(ctx, "u1", "冒烟测试记录", 0, false)

	results, err := vec.Query(ctx, "u1", "冒烟测试记录", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Content != "冒烟测试记录" {
		t.Fatalf("the episode was not re-indexed: %+v", results)
	}
}
