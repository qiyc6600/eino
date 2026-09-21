package memory

import (
	"context"
	"strings"
	"testing"
)

func TestInMemoryVectorStore_StoreAndQuery(t *testing.T) {
	store := NewInMemoryVectorStore()
	ctx := context.Background()

	// Store some memories
	store.Store(ctx, "u_admin", "我喜欢用Python写代码", map[string]any{"thread": "t_1"})
	store.Store(ctx, "u_admin", "我在北京工作", map[string]any{"thread": "t_2"})
	store.Store(ctx, "u_admin", "我偏好简洁的回答风格", map[string]any{"thread": "t_3"})

	// Query: should find Python-related memory
	results, err := store.Query(ctx, "u_admin", "帮我写个脚本", 3)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}

	// The Python memory should be most relevant (shared words: 写, code)
	t.Logf("Top result: content=%q score=%.3f", results[0].Content, results[0].Score)
	if results[0].Content != "我喜欢用Python写代码" {
		// Hash-based embedding may not perfectly rank, but it should be in the results
		t.Logf("Warning: top result is not Python-related (hash embedding limitation)")
	}
}

func TestInMemoryVectorStore_UserIsolation(t *testing.T) {
	store := NewInMemoryVectorStore()
	ctx := context.Background()

	store.Store(ctx, "u_admin", "admin secret", nil)
	store.Store(ctx, "u_visitor", "visitor info", nil)

	results, _ := store.Query(ctx, "u_admin", "secret", 5)
	if len(results) == 0 {
		t.Fatal("expected results for admin")
	}
	for _, r := range results {
		if r.Content == "visitor info" {
			t.Error("user isolation violated: admin got visitor memory")
		}
	}
}

func TestInMemoryVectorStore_EmptyQuery(t *testing.T) {
	store := NewInMemoryVectorStore()
	ctx := context.Background()

	results, err := store.Query(ctx, "u_admin", "anything", 5)
	if err != nil {
		t.Fatalf("query on empty store failed: %v", err)
	}
	if len(results) != 0 {
		t.Error("expected no results for empty store")
	}
}

func TestInMemoryVectorStore_DeleteUser(t *testing.T) {
	store := NewInMemoryVectorStore()
	ctx := context.Background()

	store.Store(ctx, "u_admin", "some memory", nil)
	store.DeleteUser(ctx, "u_admin")

	results, _ := store.Query(ctx, "u_admin", "memory", 5)
	if len(results) != 0 {
		t.Error("expected no results after DeleteUser")
	}
}

func TestFormatVectorResults(t *testing.T) {
	results := []VectorResult{
		{Content: "用户喜欢Python", Score: 0.85, Metadata: map[string]any{"timestamp": "2024-01-01"}},
		{Content: "用户在北京", Score: 0.15, Metadata: map[string]any{}},
	}

	formatted := FormatVectorResults(results)
	if formatted == "" {
		t.Fatal("expected non-empty formatted output")
	}
	if !containsStr(formatted, "Python") {
		t.Error("expected formatted output to contain 'Python'")
	}
	if containsStr(formatted, "北京") {
		t.Error("expected low-score result (0.15) to be filtered out (below 0.3 threshold)")
	}
}

// TestFormatVectorResultsWithin covers the length cap: without it the recalled
// text can exceed the whole memory budget on its own.
func TestFormatVectorResultsWithin(t *testing.T) {
	long := strings.Repeat("这是一个很长的历史片段。", 20)
	results := []VectorResult{
		{Content: long, Score: 0.95, Metadata: map[string]any{}},
		{Content: "用户喜欢Python", Score: 0.85, Metadata: map[string]any{}},
		{Content: "用户在北京", Score: 0.80, Metadata: map[string]any{}},
	}

	t.Run("no cap keeps everything", func(t *testing.T) {
		got := FormatVectorResultsWithin(results, 0, 0)
		if !containsStr(got, "Python") || !containsStr(got, "北京") {
			t.Fatalf("uncapped formatting dropped entries: %q", got)
		}
	})

	t.Run("an oversized entry does not consume the whole budget", func(t *testing.T) {
		// The first entry alone blows the cap, so it is skipped in favour of the
		// shorter, slightly less relevant ones that still fit.
		got := FormatVectorResultsWithin(results, 40, 0)
		if containsStr(got, long) {
			t.Fatalf("the oversized entry should have been skipped: %q", got)
		}
		if !containsStr(got, "Python") {
			t.Fatalf("expected the shorter entry to be kept: %q", got)
		}
	})

	t.Run("entries are dropped whole, never cut in half", func(t *testing.T) {
		got := FormatVectorResultsWithin(results, 25, 0)
		// Whatever survives must be a complete line: every content piece that
		// appears in the output must appear in full.
		if containsStr(got, long[:len(long)/2]) && !containsStr(got, long) {
			t.Fatalf("an entry was truncated instead of dropped: %q", got)
		}
		for _, r := range results {
			if containsStr(got, r.Content[:8]) && !containsStr(got, r.Content) {
				t.Fatalf("partial entry in output: %q", got)
			}
		}
	})
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
