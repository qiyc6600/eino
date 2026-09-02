package auth

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

// TestService_ConcurrentUserManagement exercises login, user creation,
// listing, and role updates from many goroutines at once. Run with -race
// to verify the users map is properly synchronized.
func TestService_ConcurrentUserManagement(t *testing.T) {
	svc := NewService(NewInMemorySessionStore(), NewRBACManager())
	ctx := context.Background()

	const workers = 8
	const iterations = 50

	var wg sync.WaitGroup
	errCh := make(chan error, workers*iterations*2)

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				username := fmt.Sprintf("user_%d_%d", worker, i)

				// Concurrent writes: create + role update
				if _, err := svc.CreateUser(ctx, username, "pass123", []string{"visitor"}); err != nil {
					errCh <- fmt.Errorf("create %s: %w", username, err)
					continue
				}
				// Lookup the created user's ID via the users list.
				var userID string
				for _, u := range svc.ListUsers(ctx) {
					if u.Username == username {
						userID = u.ID
						break
					}
				}
				if userID == "" {
					errCh <- fmt.Errorf("created user %s not found in list", username)
					continue
				}
				if err := svc.UpdateUserRoles(ctx, userID, []string{"visitor", "admin"}); err != nil {
					errCh <- fmt.Errorf("update roles %s: %w", username, err)
				}

				// Concurrent reads: seed users + list + login
				if _, err := svc.Login(ctx, "admin", "admin123"); err != nil {
					errCh <- fmt.Errorf("login admin: %w", err)
				}
				svc.GetUser(ctx, "visitor")
				svc.GetUserByID(ctx, "u_admin")
			}
		}(w)
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
}
