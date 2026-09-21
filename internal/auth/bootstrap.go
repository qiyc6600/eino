package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

const MinBootstrapPasswordLength = 12

// EnsureBootstrapAdmin creates the first administrator from explicit
// configuration. Existing accounts are never overwritten. Once an admin is
// stored durably, deployments may remove both bootstrap values.
func (s *Service) EnsureBootstrapAdmin(ctx context.Context, username, password string) error {
	username = strings.TrimSpace(username)
	if (username == "") != (password == "") {
		return errors.New("BOOTSTRAP_ADMIN_USERNAME and BOOTSTRAP_ADMIN_PASSWORD must be set together")
	}
	if username == "" {
		users, err := s.users.List(ctx)
		if err != nil {
			return fmt.Errorf("list users for admin bootstrap: %w", err)
		}
		for _, user := range users {
			if hasRole(user.Roles, "admin") {
				return nil
			}
		}
		return errors.New("no administrator exists; set BOOTSTRAP_ADMIN_USERNAME and BOOTSTRAP_ADMIN_PASSWORD")
	}
	if utf8.RuneCountInString(password) < MinBootstrapPasswordLength {
		return fmt.Errorf("BOOTSTRAP_ADMIN_PASSWORD must contain at least %d characters", MinBootstrapPasswordLength)
	}

	if existing, ok, err := s.users.GetByUsername(ctx, username); err != nil {
		return fmt.Errorf("look up bootstrap administrator: %w", err)
	} else if ok {
		if !hasRole(existing.Roles, "admin") {
			return fmt.Errorf("bootstrap user %q exists without the admin role", username)
		}
		return nil
	}

	passwordHash, err := hashPassword(password)
	if err != nil {
		return err
	}
	user := User{
		ID:           "u_" + username,
		Username:     username,
		PasswordHash: passwordHash,
		Roles:        []string{"admin"},
	}
	if err := s.users.Create(ctx, user); err != nil {
		// Multiple instances may bootstrap the same empty database together.
		// Accept the unique-key loser only after confirming the winner is admin.
		if !errors.Is(err, ErrUserExists) {
			return fmt.Errorf("create bootstrap administrator: %w", err)
		}
		existing, ok, lookupErr := s.users.GetByUsername(ctx, username)
		if lookupErr != nil {
			return fmt.Errorf("verify bootstrap administrator: %w", lookupErr)
		}
		if !ok || !hasRole(existing.Roles, "admin") {
			return fmt.Errorf("bootstrap user %q was created without the admin role", username)
		}
	}
	return nil
}

func hasRole(roles []string, target string) bool {
	for _, role := range roles {
		if role == target {
			return true
		}
	}
	return false
}
