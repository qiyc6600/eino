package auth

import (
	"context"
	"strings"
	"testing"
)

func TestArgon2IDPasswordHash(t *testing.T) {
	first, err := hashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	second, err := hashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(first, "$argon2id$") {
		t.Fatalf("expected Argon2id PHC string, got %q", first)
	}
	if first == second {
		t.Fatal("password hashes must use unique random salts")
	}
	valid, needsUpgrade, err := verifyPassword(first, "correct horse battery staple")
	if err != nil || !valid || needsUpgrade {
		t.Fatalf("verify current hash: valid=%v upgrade=%v err=%v", valid, needsUpgrade, err)
	}
	valid, _, err = verifyPassword(first, "wrong")
	if err != nil || valid {
		t.Fatalf("wrong password accepted: valid=%v err=%v", valid, err)
	}
}

func TestLegacySHA256LoginUpgradesStoredHash(t *testing.T) {
	ctx := context.Background()
	users := NewInMemoryUserStore()
	legacy := User{ID: "u_legacy", Username: "legacy", PasswordHash: legacyPasswordHash("old-password"), Roles: []string{"visitor"}}
	if err := users.Create(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	service, err := NewServiceWithUserStore(NewInMemorySessionStore(), users, NewRBACManager())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Login(ctx, legacy.Username, "old-password"); err != nil {
		t.Fatalf("legacy login failed: %v", err)
	}
	upgraded, ok, err := users.GetByID(ctx, legacy.ID)
	if err != nil || !ok {
		t.Fatalf("load upgraded user: ok=%v err=%v", ok, err)
	}
	if !strings.HasPrefix(upgraded.PasswordHash, "$argon2id$") {
		t.Fatalf("legacy hash was not upgraded: %q", upgraded.PasswordHash)
	}
	if valid, needsUpgrade, err := verifyPassword(upgraded.PasswordHash, "old-password"); err != nil || !valid || needsUpgrade {
		t.Fatalf("upgraded hash verification: valid=%v upgrade=%v err=%v", valid, needsUpgrade, err)
	}
}

func TestPasswordHashParserRejectsUnsafeValues(t *testing.T) {
	for _, encoded := range []string{
		"not-a-hash",
		"$argon2id$v=19$m=999999999,t=2,p=1$c2FsdHNhbHQ$aGFzaGhhc2hoYXNoaGFzaA",
		"$argon2id$v=19$m=19456,t=2,p=1junk$c2FsdHNhbHQ$aGFzaGhhc2hoYXNoaGFzaA",
	} {
		if valid, _, err := verifyPassword(encoded, "password"); err == nil || valid {
			t.Fatalf("unsafe hash accepted: %q", encoded)
		}
	}
}

func TestCreateUserStoresArgon2IDHash(t *testing.T) {
	ctx := context.Background()
	users := NewInMemoryUserStore()
	service, err := NewServiceWithUserStore(NewInMemorySessionStore(), users, NewRBACManager())
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateUser(ctx, "new-user", "password", []string{"visitor"})
	if err != nil {
		t.Fatal(err)
	}
	stored, ok, err := users.GetByID(ctx, created.ID)
	if err != nil || !ok {
		t.Fatalf("load created user: ok=%v err=%v", ok, err)
	}
	if !strings.HasPrefix(stored.PasswordHash, "$argon2id$") {
		t.Fatalf("expected Argon2id hash, got %q", stored.PasswordHash)
	}
}
