package auth

import "time"

// User represents a system user.
type User struct {
	ID           string
	Username     string
	PasswordHash string
	Roles        []string
}

// Session represents an active user session.
type Session struct {
	ID        string
	UserID    string
	Username  string
	Roles     []string
	CreatedAt time.Time
}

// Role represents a named role with tool-level permissions.
type Role struct {
	Name        string
	Permissions []Permission
}

// Permission represents a tool-level permission entry.
type Permission struct {
	Resource string // always "tool"
	Action   string // always "invoke"
	Name     string // tool name: calculator, weather, delete_order, etc.
}

// LoginRequest is the login API request body.
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// LoginResponse is the login API response body.
type LoginResponse struct {
	SessionID string     `json:"sessionId"`
	User      UserPublic `json:"user"`
}

// UserPublic is the public user information returned in API responses.
type UserPublic struct {
	ID       string   `json:"id"`
	Username string   `json:"username"`
	Roles    []string `json:"roles"`
}
