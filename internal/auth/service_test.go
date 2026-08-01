package auth

import (
	"context"
	"testing"
)

func TestLogin_Success(t *testing.T) {
	store := NewInMemorySessionStore()
	rbac := NewRBACManager()
	svc := NewService(store, rbac)

	resp, err := svc.Login(context.Background(), "admin", "admin123")
	if err != nil {
		t.Fatalf("expected login success, got error: %v", err)
	}
	if resp.SessionID == "" {
		t.Error("expected non-empty session ID")
	}
	if resp.User.Username != "admin" {
		t.Errorf("expected username=admin, got %s", resp.User.Username)
	}
	if len(resp.User.Roles) != 1 || resp.User.Roles[0] != "admin" {
		t.Errorf("expected roles=[admin], got %v", resp.User.Roles)
	}
}

func TestLogin_Visitor(t *testing.T) {
	store := NewInMemorySessionStore()
	rbac := NewRBACManager()
	svc := NewService(store, rbac)

	resp, err := svc.Login(context.Background(), "visitor", "visitor123")
	if err != nil {
		t.Fatalf("expected login success, got error: %v", err)
	}
	if resp.User.Username != "visitor" {
		t.Errorf("expected username=visitor, got %s", resp.User.Username)
	}
	if len(resp.User.Roles) != 1 || resp.User.Roles[0] != "visitor" {
		t.Errorf("expected roles=[visitor], got %v", resp.User.Roles)
	}
}

func TestLogin_WrongPassword(t *testing.T) {
	store := NewInMemorySessionStore()
	rbac := NewRBACManager()
	svc := NewService(store, rbac)

	_, err := svc.Login(context.Background(), "admin", "wrong")
	if err == nil {
		t.Error("expected login failure for wrong password")
	}
}

func TestLogin_UserNotFound(t *testing.T) {
	store := NewInMemorySessionStore()
	rbac := NewRBACManager()
	svc := NewService(store, rbac)

	_, err := svc.Login(context.Background(), "nonexistent", "pass")
	if err == nil {
		t.Error("expected login failure for nonexistent user")
	}
}

func TestValidateSession_Valid(t *testing.T) {
	store := NewInMemorySessionStore()
	rbac := NewRBACManager()
	svc := NewService(store, rbac)

	resp, _ := svc.Login(context.Background(), "admin", "admin123")
	session, err := svc.ValidateSession(context.Background(), resp.SessionID)
	if err != nil {
		t.Fatalf("expected valid session, got error: %v", err)
	}
	if session.UserID != "u_admin" {
		t.Errorf("expected userID=u_admin, got %s", session.UserID)
	}
	if session.Username != "admin" {
		t.Errorf("expected username=admin, got %s", session.Username)
	}
}

func TestValidateSession_Invalid(t *testing.T) {
	store := NewInMemorySessionStore()
	rbac := NewRBACManager()
	svc := NewService(store, rbac)

	_, err := svc.ValidateSession(context.Background(), "fake_session_id")
	if err == nil {
		t.Error("expected error for invalid session")
	}
}

func TestLogout(t *testing.T) {
	store := NewInMemorySessionStore()
	rbac := NewRBACManager()
	svc := NewService(store, rbac)

	resp, _ := svc.Login(context.Background(), "admin", "admin123")
	svc.Logout(context.Background(), resp.SessionID)

	_, err := svc.ValidateSession(context.Background(), resp.SessionID)
	if err == nil {
		t.Error("expected session to be invalidated after logout")
	}
}

func TestCreateUser(t *testing.T) {
	store := NewInMemorySessionStore()
	rbac := NewRBACManager()
	svc := NewService(store, rbac)

	user, err := svc.CreateUser(context.Background(), "testuser", "password", []string{"visitor"})
	if err != nil {
		t.Fatalf("expected user creation success, got error: %v", err)
	}
	if user.Username != "testuser" {
		t.Errorf("expected username=testuser, got %s", user.Username)
	}

	// Login with new user
	resp, err := svc.Login(context.Background(), "testuser", "password")
	if err != nil {
		t.Fatalf("expected login with new user, got error: %v", err)
	}
	if resp.User.Username != "testuser" {
		t.Errorf("expected username=testuser, got %s", resp.User.Username)
	}
}

func TestCreateUser_Duplicate(t *testing.T) {
	store := NewInMemorySessionStore()
	rbac := NewRBACManager()
	svc := NewService(store, rbac)

	_, err := svc.CreateUser(context.Background(), "admin", "password", []string{"admin"})
	if err == nil {
		t.Error("expected error for duplicate user creation")
	}
}

func TestUpdateUserRoles(t *testing.T) {
	store := NewInMemorySessionStore()
	rbac := NewRBACManager()
	svc := NewService(store, rbac)

	err := svc.UpdateUserRoles(context.Background(), "u_admin", []string{"visitor"})
	if err != nil {
		t.Fatalf("expected role update success, got error: %v", err)
	}

	user, _ := svc.GetUser(context.Background(), "admin")
	if len(user.Roles) != 1 || user.Roles[0] != "visitor" {
		t.Errorf("expected roles=[visitor] after update, got %v", user.Roles)
	}
}

func TestListUsers(t *testing.T) {
	store := NewInMemorySessionStore()
	rbac := NewRBACManager()
	svc := NewService(store, rbac)

	users := svc.ListUsers(context.Background())
	if len(users) < 2 {
		t.Errorf("expected at least 2 seed users, got %d", len(users))
	}
}

func TestRBAC_AdminCanInvokeAll(t *testing.T) {
	rbac := NewRBACManager()
	tools := []string{"calculator", "weather", "grep", "query_order", "delete_order", "send_email"}
	for _, tool := range tools {
		if !rbac.CanInvokeTool(context.Background(), []string{"admin"}, tool) {
			t.Errorf("admin should be able to invoke %s", tool)
		}
	}
}

func TestRBAC_VisitorLimited(t *testing.T) {
	rbac := NewRBACManager()
	allowed := map[string]bool{
		"calculator":  true,
		"weather":     true,
		"query_order": true,
	}
	denied := map[string]bool{
		"grep":         true,
		"delete_order": true,
		"send_email":   true,
	}

	for tool := range allowed {
		if !rbac.CanInvokeTool(context.Background(), []string{"visitor"}, tool) {
			t.Errorf("visitor should be able to invoke %s", tool)
		}
	}
	for tool := range denied {
		if rbac.CanInvokeTool(context.Background(), []string{"visitor"}, tool) {
			t.Errorf("visitor should NOT be able to invoke %s", tool)
		}
	}
}

func TestRBAC_GetToolsForRoles(t *testing.T) {
	rbac := NewRBACManager()

	adminTools := rbac.GetToolsForRoles([]string{"admin"})
	if len(adminTools) != 6 {
		t.Errorf("expected 6 admin tools, got %d", len(adminTools))
	}

	visitorTools := rbac.GetToolsForRoles([]string{"visitor"})
	if len(visitorTools) != 3 {
		t.Errorf("expected 3 visitor tools, got %d", len(visitorTools))
	}
}
