package memory

import (
	"context"
	"strings"
	"testing"
)

func TestMemorySettings_RoundTrip(t *testing.T) {
	svc := NewService(NewInMemoryMemoryStore(), nil, nil, nil)
	ctx := context.Background()

	// A user who never touched anything has memory on.
	settings, err := svc.GetSettings(ctx, "u_admin")
	if err != nil {
		t.Fatal(err)
	}
	if !settings.Enabled {
		t.Fatal("memory should default to enabled")
	}
	if !svc.MemoryEnabled(ctx, "u_admin") {
		t.Fatal("expected memory to be enabled by default")
	}

	if err := svc.SetSettings(ctx, "u_admin", MemorySettings{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	settings, err = svc.GetSettings(ctx, "u_admin")
	if err != nil {
		t.Fatal(err)
	}
	if settings.Enabled {
		t.Fatal("expected the switch to persist as off")
	}
	if svc.MemoryEnabled(ctx, "u_admin") {
		t.Fatal("expected MemoryEnabled to report off")
	}
}

// The switch is per user: one user turning memory off must not affect another.
func TestMemorySettings_UserIsolation(t *testing.T) {
	svc := NewService(NewInMemoryMemoryStore(), nil, nil, nil)
	ctx := context.Background()

	if err := svc.SetSettings(ctx, "u_a", MemorySettings{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if !svc.MemoryEnabled(ctx, "u_b") {
		t.Fatal("another user's switch must not leak")
	}
}

// Turning memory off stops extraction and injection, and turning it back on
// restores the entries that were kept.
func TestMemorySettings_DisabledStopsWriteAndRead(t *testing.T) {
	svc := NewService(NewInMemoryMemoryStore(), nil, nil, nil)
	ctx := context.Background()

	if err := svc.ExtractAndSave(ctx, "u_admin", "t1", "我喜欢用Python"); err != nil {
		t.Fatal(err)
	}
	if got := svc.RetrieveRelevant(ctx, "u_admin", "写脚本", 0, false); got == "" {
		t.Fatal("expected memory to be injected while enabled")
	}

	if err := svc.SetSettings(ctx, "u_admin", MemorySettings{Enabled: false}); err != nil {
		t.Fatal(err)
	}

	// Writes are dropped.
	if err := svc.ExtractAndSave(ctx, "u_admin", "t2", "我更喜欢用 Go 写服务"); err != nil {
		t.Fatal(err)
	}
	// Reads inject nothing.
	if got := svc.RetrieveRelevant(ctx, "u_admin", "写脚本", 0, false); got != "" {
		t.Fatalf("disabled memory must not be injected, got %q", got)
	}
	// Entries are kept, not deleted.
	entries, err := svc.ListPreferences(ctx, "u_admin")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("entries should be preserved while memory is off")
	}
	for _, e := range entries {
		if strings.Contains(e.Value, "Go 写服务") {
			t.Fatalf("a disabled user must not accumulate new memories: %+v", e)
		}
	}

	// Turning it back on restores the previous behavior.
	if err := svc.SetSettings(ctx, "u_admin", MemorySettings{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if got := svc.RetrieveRelevant(ctx, "u_admin", "写脚本", 0, false); got == "" {
		t.Fatal("expected memory to be injected again after re-enabling")
	}
}

// The settings entry is framework bookkeeping: it must not show up as a memory
// the user can see, edit, or delete.
func TestMemorySettings_ReservedKeyIsHidden(t *testing.T) {
	svc := NewService(NewInMemoryMemoryStore(), nil, nil, nil)
	ctx := context.Background()

	if err := svc.SetSettings(ctx, "u_admin", MemorySettings{Enabled: false}); err != nil {
		t.Fatal(err)
	}

	entries, err := svc.ListPreferences(ctx, "u_admin")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if IsReservedKey(e.Key) {
			t.Fatalf("reserved entry leaked into the user-visible list: %+v", e)
		}
	}

	if err := svc.DeletePreference(ctx, "u_admin", settingsKey); err == nil {
		t.Fatal("deleting a reserved key through the memory API must be refused")
	}
	if err := svc.UpsertPreference(ctx, "u_admin", settingsKey, `{"memory_enabled":true}`, EntryMeta{}); err == nil {
		t.Fatal("writing a reserved key through the memory API must be refused")
	}
	if !strings.HasPrefix(settingsKey, ReservedKeyPrefix) {
		t.Fatalf("settings key %q should use the reserved prefix", settingsKey)
	}
}
