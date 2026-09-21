package auth

import (
	"context"
	"testing"
	"time"
)

func newTestService(t *testing.T, store SessionStore, rbac *RBACManager, sessionTTL ...time.Duration) *Service {
	t.Helper()
	users := NewInMemoryUserStore()
	for _, seed := range []struct {
		id, username, password, role string
	}{
		{id: "u_admin", username: "admin", password: "admin123", role: "admin"},
		{id: "u_visitor", username: "visitor", password: "visitor123", role: "visitor"},
	} {
		hash, err := hashPassword(seed.password)
		if err != nil {
			t.Fatal(err)
		}
		if err := users.Create(context.Background(), User{
			ID: seed.id, Username: seed.username, PasswordHash: hash, Roles: []string{seed.role},
		}); err != nil {
			t.Fatal(err)
		}
	}
	service, err := NewServiceWithUserStore(store, users, rbac, sessionTTL...)
	if err != nil {
		t.Fatal(err)
	}
	return service
}
