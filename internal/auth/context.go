package auth

import (
	"context"
	"fmt"
)

// AuthContext carries identity information through the call chain.
// It must be propagated along the call chain but NOT exposed to the LLM.
type AuthContext struct {
	SessionID string
	UserID    string
	Username  string
	Roles     []string
	ThreadID  string
	RunID     string
}

// contextKey is the exported key type for AuthContext in context.Context.
// All packages must use this key to ensure type compatibility.
type contextKey struct{}

// ContextKey is the exported key instance for use with context.Value.
var ContextKey = contextKey{}

// WithAuthContext injects AuthContext into the context.Context.
func WithAuthContext(ctx context.Context, ac *AuthContext) context.Context {
	return context.WithValue(ctx, ContextKey, ac)
}

// FromContext extracts AuthContext from context.Context.
func FromContext(ctx context.Context) *AuthContext {
	ac, _ := ctx.Value(ContextKey).(*AuthContext)
	return ac
}

// MustFromContext extracts AuthContext from context.Context, panics if not found.
func MustFromContext(ctx context.Context) *AuthContext {
	ac := FromContext(ctx)
	if ac == nil {
		panic("auth: AuthContext not found in context")
	}
	return ac
}

// WithThread returns a copy of AuthContext with the given thread ID.
// Callers must not mutate a shared AuthContext in place — the same pointer
// may be captured by goroutines (e.g. preference extraction, snapshots).
func (ac *AuthContext) WithThread(threadID string) *AuthContext {
	copied := *ac
	copied.ThreadID = threadID
	return &copied
}

// WithRun returns a copy of AuthContext with the given run ID.
func (ac *AuthContext) WithRun(runID string) *AuthContext {
	copied := *ac
	copied.RunID = runID
	return &copied
}

// ToolIdentity is the typed, framework-minted identity handed to tools.
// It can only be derived from an AuthContext that the auth middleware
// injected into the Go context — tools never parse identity out of
// LLM arguments or request bodies, and there is no untyped map channel
// that could be constructed with a forged user ID.
type ToolIdentity struct {
	Context    context.Context `json:"-"`
	UserID     string
	Roles      []string
	ThreadID   string
	RunID      string
	ToolCallID string
}

// ToolIdentityFromContext derives the tool identity from the AuthContext
// carried by ctx. Returns nil when the context carries no identity, in
// which case callers must reject the invocation.
func ToolIdentityFromContext(ctx context.Context) *ToolIdentity {
	ac := FromContext(ctx)
	if ac == nil {
		return nil
	}
	return &ToolIdentity{
		Context:  ctx,
		UserID:   ac.UserID,
		Roles:    ac.Roles,
		ThreadID: ac.ThreadID,
		RunID:    ac.RunID,
	}
}

// CheckUserScope is the store-level isolation guard. When the context carries
// an authenticated identity, the requested userID must match it — otherwise
// the call is a cross-user access and is rejected. Contexts without identity
// (internal background jobs) pass through, keeping the check free of false
// positives while still blocking any framework-callable path from crossing
// user boundaries.
func CheckUserScope(ctx context.Context, userID string) error {
	if ac := FromContext(ctx); ac != nil && ac.UserID != userID {
		return fmt.Errorf("cross-user access denied: context user %q cannot access data of user %q", ac.UserID, userID)
	}
	return nil
}
