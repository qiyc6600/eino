package memory

import (
	"context"

	"testing"

	chromem "github.com/philippgille/chromem-go"

	"github.com/example/agent-eino-demo/internal/auth"
)

// stubEmbedding is a deterministic offline embedding so the chromem-backed store
// can be tested without a network call. It mirrors the hash fallback: text with
// shared words gets similar vectors.
func stubEmbedding(_ context.Context, text string) ([]float32, error) {
	return hashEmbed(text, 64), nil
}

func newTestChromemStore(t *testing.T) *ChromemVectorStore {
	t.Helper()
	return &ChromemVectorStore{db: chromem.NewDB(), embedFunc: stubEmbedding}
}

// TestVectorStore_StoreWithIDAndDelete covers the capability document chunks
// need: a stable id so an entry can be replaced and removed again.
func TestVectorStore_StoreWithIDAndDelete(t *testing.T) {
	ctx := context.Background()
	stores := map[string]VectorStore{
		"in-memory": NewInMemoryVectorStore(),
		"chromem":   newTestChromemStore(t),
	}

	for name, store := range stores {
		t.Run(name, func(t *testing.T) {
			if err := store.StoreWithID(ctx, "u1", "chunk-1", "alpha content", nil); err != nil {
				t.Fatal(err)
			}
			if err := store.StoreWithID(ctx, "u1", "chunk-2", "beta content", nil); err != nil {
				t.Fatal(err)
			}

			// Re-using an id replaces rather than duplicates, which is what makes
			// re-indexing idempotent.
			if err := store.StoreWithID(ctx, "u1", "chunk-1", "alpha content revised", nil); err != nil {
				t.Fatal(err)
			}
			results, err := store.Query(ctx, "u1", "alpha content revised", 10)
			if err != nil {
				t.Fatal(err)
			}
			if countContent(results, "alpha content revised") != 1 {
				t.Fatalf("re-storing an id duplicated it: %+v", results)
			}
			if countContent(results, "alpha content") != 0 {
				t.Fatalf("the replaced entry is still present: %+v", results)
			}

			// Deleting one id leaves the other alone.
			if err := store.Delete(ctx, "u1", "chunk-1"); err != nil {
				t.Fatal(err)
			}
			results, err = store.Query(ctx, "u1", "beta content", 10)
			if err != nil {
				t.Fatal(err)
			}
			if countContent(results, "alpha content revised") != 0 {
				t.Fatalf("the deleted entry survived: %+v", results)
			}
			if countContent(results, "beta content") != 1 {
				t.Fatalf("deleting one id removed another: %+v", results)
			}

			// Unknown ids are ignored rather than failing.
			if err := store.Delete(ctx, "u1", "does-not-exist"); err != nil {
				t.Fatalf("deleting an unknown id should be a no-op: %v", err)
			}
		})
	}
}

func countContent(results []VectorResult, content string) int {
	n := 0
	for _, r := range results {
		if r.Content == content {
			n++
		}
	}
	return n
}

// TestChromemStore_SmallCorpusAndEmptyQuery covers two wrapper bugs that made
// recall silently disappear: chromem rejects nResults above the document count,
// and it rejects an empty query text — which the token-bar path sends.
func TestChromemStore_SmallCorpusAndEmptyQuery(t *testing.T) {
	ctx := context.Background()
	store := newTestChromemStore(t)

	if err := store.Store(ctx, "u1", "only entry", nil); err != nil {
		t.Fatal(err)
	}

	// topK far above the corpus size must not fail the query.
	results, err := store.Query(ctx, "u1", "only entry", 5)
	if err != nil {
		t.Fatalf("a small corpus must not fail the query: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected the single entry, got %d results", len(results))
	}

	// An empty query returns nothing instead of erroring.
	results, err = store.Query(ctx, "u1", "   ", 5)
	if err != nil {
		t.Fatalf("an empty query must not error: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("an empty query should return nothing, got %+v", results)
	}

	// An empty collection is the same case one step earlier.
	if results, err = store.Query(ctx, "u2", "anything", 5); err != nil || len(results) != 0 {
		t.Fatalf("an empty collection should return nothing, got %+v err=%v", results, err)
	}
}

// TestChromemStore_DeleteUser asserts the collection is really dropped; the
// wrapper used to report this as unsupported.
func TestChromemStore_DeleteUser(t *testing.T) {
	ctx := context.Background()
	store := newTestChromemStore(t)

	if err := store.Store(ctx, "u1", "entry to drop", nil); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteUser(ctx, "u1"); err != nil {
		t.Fatalf("DeleteUser should be supported: %v", err)
	}
	results, err := store.Query(ctx, "u1", "entry to drop", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("entries survived DeleteUser: %+v", results)
	}
}

// TestVectorStore_CrossUserDeleteDenied asserts the store-level isolation guard
// covers the new methods too.
func TestVectorStore_CrossUserDeleteDenied(t *testing.T) {
	ctx := context.Background()
	stores := map[string]VectorStore{
		"in-memory": NewInMemoryVectorStore(),
		"chromem":   newTestChromemStore(t),
	}
	// An identity that does not match the target userID.
	scoped := auth.WithAuthContext(ctx, &auth.AuthContext{UserID: "u-other"})

	for name, store := range stores {
		t.Run(name, func(t *testing.T) {
			if err := store.Delete(scoped, "u1", "chunk-1"); err == nil {
				t.Error("cross-user delete was allowed")
			}
			if err := store.StoreWithID(scoped, "u1", "chunk-1", "x", nil); err == nil {
				t.Error("cross-user write was allowed")
			}
		})
	}
}

// TestVectorStore_StoreRequiresID pins that an empty id is rejected rather than
// silently creating an unaddressable entry.
func TestVectorStore_StoreRequiresID(t *testing.T) {
	ctx := context.Background()
	for name, store := range map[string]VectorStore{
		"in-memory": NewInMemoryVectorStore(),
		"chromem":   newTestChromemStore(t),
	} {
		t.Run(name, func(t *testing.T) {
			if err := store.StoreWithID(ctx, "u1", "", "content", nil); err == nil {
				t.Fatal("expected an error for an empty id")
			}
		})
	}
}
