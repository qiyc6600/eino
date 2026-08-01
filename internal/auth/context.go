package auth

import "context"

// AuthContext carries identity information through the call chain.
// It must be propagated along the call chain but NOT exposed to the LLM.
type AuthContext struct {
	SessionID string
	UserID    string
	Username  string
	Roles     []string
	ThreadID  string
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

// WithToolContext injects a tool context map (user_id, roles, etc.) into context.
// This is used by the runner to pass auth info through Eino's tool execution chain.
func WithToolContext(ctx context.Context, m map[string]any) context.Context {
	return context.WithValue(ctx, toolContextKey{}, m)
}

// FromToolContext extracts the tool context map from context.
func FromToolContext(ctx context.Context) map[string]any {
	m, _ := ctx.Value(toolContextKey{}).(map[string]any)
	return m
}

type toolContextKey struct{}

