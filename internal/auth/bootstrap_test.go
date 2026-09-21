package auth

import (
	"context"
	"strings"
	"sync"
	"testing"
)

func newBootstrapTestService(t *testing.T) (*Service, *InMemoryUserStore) {
	t.Helper()
	users := NewInMemoryUserStore()
	service, err := NewServiceWithUserStore(NewInMemorySessionStore(), users, NewRBACManager())
	if err != nil {
		t.Fatal(err)
	}
	return service, users
}

func TestEnsureBootstrapAdminRequiresCredentialsForEmptyStore(t *testing.T) {
	service, _ := newBootstrapTestService(t)
	if err := service.EnsureBootstrapAdmin(context.Background(), "", ""); err == nil || !strings.Contains(err.Error(), "no administrator exists") {
		t.Fatalf("expected missing bootstrap error, got %v", err)
	}
	if err := service.EnsureBootstrapAdmin(context.Background(), "admin", ""); err == nil {
		t.Fatal("expected partial bootstrap configuration to fail")
	}
	if err := service.EnsureBootstrapAdmin(context.Background(), "admin", "too-short"); err == nil {
		t.Fatal("expected weak bootstrap password to fail")
	}
}

func TestEnsureBootstrapAdminCreatesWithoutOverwriting(t *testing.T) {
	ctx := context.Background()
	service, users := newBootstrapTestService(t)
	const originalPassword = "strong-admin-password"
	if err := service.EnsureBootstrapAdmin(ctx, "root-admin", originalPassword); err != nil {
		t.Fatal(err)
	}
	admin, ok, err := users.GetByUsername(ctx, "root-admin")
	if err != nil || !ok || !hasRole(admin.Roles, "admin") {
		t.Fatalf("bootstrap admin missing: ok=%v user=%+v err=%v", ok, admin, err)
	}
	if admin.ID != "u_root-admin" {
		t.Fatalf("bootstrap ID must be stable across restarts, got %q", admin.ID)
	}
	if !strings.HasPrefix(admin.PasswordHash, "$argon2id$") {
		t.Fatalf("bootstrap password is not Argon2id: %q", admin.PasswordHash)
	}
	if err := service.EnsureBootstrapAdmin(ctx, "root-admin", "different-admin-password"); err != nil {
		t.Fatal(err)
	}
	unchanged, _, _ := users.GetByUsername(ctx, "root-admin")
	if valid, _, err := verifyPassword(unchanged.PasswordHash, originalPassword); err != nil || !valid {
		t.Fatalf("existing password was overwritten: valid=%v err=%v", valid, err)
	}
	if err := service.EnsureBootstrapAdmin(ctx, "", ""); err != nil {
		t.Fatalf("stored administrator should allow bootstrap secrets to be removed: %v", err)
	}
}

func TestEnsureBootstrapAdminRejectsNonAdminCollision(t *testing.T) {
	ctx := context.Background()
	service, users := newBootstrapTestService(t)
	hash, err := hashPassword("existing-user-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := users.Create(ctx, User{ID: "u_existing", Username: "existing", PasswordHash: hash, Roles: []string{"visitor"}}); err != nil {
		t.Fatal(err)
	}
	if err := service.EnsureBootstrapAdmin(ctx, "existing", "strong-admin-password"); err == nil || !strings.Contains(err.Error(), "without the admin role") {
		t.Fatalf("expected role collision error, got %v", err)
	}
	if err := service.EnsureBootstrapAdmin(ctx, "", ""); err == nil {
		t.Fatal("a non-admin account must not satisfy bootstrap readiness")
	}
}

func TestEnsureBootstrapAdminConcurrentInstances(t *testing.T) {
	ctx := context.Background()
	users := NewInMemoryUserStore()
	const workers = 8
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			service, err := NewServiceWithUserStore(NewInMemorySessionStore(), users, NewRBACManager())
			if err == nil {
				err = service.EnsureBootstrapAdmin(ctx, "admin", "concurrent-admin-password")
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	listed, err := users.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || !hasRole(listed[0].Roles, "admin") {
		t.Fatalf("expected one administrator, got %+v", listed)
	}
}
