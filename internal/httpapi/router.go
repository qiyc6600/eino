package httpapi

import (
	"encoding/json"
	"io/fs"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/example/agent-eino-demo/internal/agent"
	"github.com/example/agent-eino-demo/internal/auth"
	"github.com/example/agent-eino-demo/internal/hitl"
	"github.com/example/agent-eino-demo/internal/memory"
	"github.com/example/agent-eino-demo/internal/tools"
	"github.com/example/agent-eino-demo/web"
)

// Router sets up all HTTP routes.
type Router struct {
	authHandler     *AuthHandler
	agentHandler    *AgentHandler
	approvalHandler *ApprovalHandler
	memoryHandler   *MemoryHandler
	userHandler     *UserHandler
	modelHandler    *ModelHandler
	authMiddleware  func(http.Handler) http.Handler
}

// NewRouter creates a new Router with all handlers.
func NewRouter(
	authSvc *auth.Service,
	runner *agent.Runner,
	hitlSvc *hitl.Service,
	memSvc *memory.Service,
	registry *tools.ToolRegistry,
	modelSwitcher ModelSwitcher,
) *Router {
	return &Router{
		authHandler:     NewAuthHandler(authSvc),
		agentHandler:    NewAgentHandler(runner, memSvc),
		approvalHandler: NewApprovalHandler(hitlSvc, runner),
		memoryHandler:   NewMemoryHandler(memSvc),
		userHandler:     NewUserHandler(registry),
		modelHandler:    NewModelHandler(modelSwitcher),
		authMiddleware:  auth.AuthMiddleware(authSvc),
	}
}

// Handler returns the complete http.Handler with all routes.
func (r *Router) Handler() http.Handler {
	mux := http.NewServeMux()

	// Static files (no auth required) — serve embedded web UI
	staticFS, _ := fs.Sub(web.StaticFS, ".")
	fileServer := http.FileServer(http.FS(staticFS))
	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		// Only serve static files for non-API paths
		if strings.HasPrefix(req.URL.Path, "/api/") {
			http.NotFound(w, req)
			return
		}
		fileServer.ServeHTTP(w, req)
	})

	// Public routes (no auth required)
	mux.HandleFunc("/api/auth/login", r.authHandler.Login)

	// Protected routes (auth required) — register directly on main mux with auth middleware
	authMw := r.authMiddleware

	// Auth
	mux.Handle("/api/auth/me", authMw(http.HandlerFunc(r.authHandler.Me)))
	mux.Handle("/api/auth/logout", authMw(http.HandlerFunc(r.authHandler.Logout)))

	// Users & Roles
	mux.Handle("/api/users", authMw(http.HandlerFunc(r.handleUsers)))
	mux.Handle("/api/users/", authMw(http.HandlerFunc(r.handleUsersSub)))
	mux.Handle("/api/roles", authMw(http.HandlerFunc(r.authHandler.ListRoles)))

	// Agent chat
	mux.Handle("/api/agent/chat", authMw(http.HandlerFunc(r.agentHandler.Chat)))
	mux.Handle("/api/agent/runs/", authMw(http.HandlerFunc(r.agentHandler.GetRunEvents)))
	mux.Handle("/api/agent/resume", authMw(http.HandlerFunc(r.agentHandler.Resume)))

	// Thread & messages
	mux.Handle("/api/chat/threads", authMw(http.HandlerFunc(r.handleChatThreads)))
	mux.Handle("/api/chat/", authMw(http.HandlerFunc(r.handleChatThreadSub)))

	// Approvals
	mux.Handle("/api/approvals", authMw(http.HandlerFunc(r.approvalHandler.ListApprovals)))
	mux.Handle("/api/approvals/", authMw(http.HandlerFunc(r.handleApprovalsSub)))

	// Tools
	mux.Handle("/api/tools", authMw(http.HandlerFunc(r.userHandler.ListTools)))
	mux.Handle("/api/tools/", authMw(http.HandlerFunc(r.userHandler.GetTool)))

	// Memory
	mux.Handle("/api/memory", authMw(http.HandlerFunc(r.handleMemory)))
	mux.Handle("/api/memory/", authMw(http.HandlerFunc(r.memoryHandler.DeleteMemory)))

	// Model switching
	mux.Handle("/api/models", authMw(http.HandlerFunc(r.modelHandler.ListModels)))
	mux.Handle("/api/models/switch", authMw(http.HandlerFunc(r.modelHandler.SwitchModel)))

	return mux
}

func (r *Router) handleUsers(w http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodGet:
		r.authHandler.ListUsers(w, req)
	case http.MethodPost:
		r.authHandler.CreateUser(w, req)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (r *Router) handleUsersSub(w http.ResponseWriter, req *http.Request) {
	// PUT /api/users/{userId}/roles
	if strings.HasSuffix(req.URL.Path, "/roles") && req.Method == http.MethodPut {
		r.authHandler.UpdateUserRoles(w, req)
		return
	}
	writeError(w, http.StatusNotFound, "not found")
}

func (r *Router) handleChatThreads(w http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodGet:
		r.agentHandler.ListThreads(w, req)
	case http.MethodPost:
		r.agentHandler.CreateThread(w, req)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (r *Router) handleChatThreadSub(w http.ResponseWriter, req *http.Request) {
	// DELETE /api/chat/{threadId}/delete
	if strings.HasSuffix(req.URL.Path, "/delete") && req.Method == http.MethodDelete {
		r.agentHandler.DeleteThread(w, req)
		return
	}
	if req.Method == http.MethodGet {
		// GET /api/chat/{threadId}/messages
		r.agentHandler.GetThreadMessages(w, req)
		return
	}
	writeError(w, http.StatusNotFound, "not found")
}

func (r *Router) handleApprovalsSub(w http.ResponseWriter, req *http.Request) {
	if strings.HasSuffix(req.URL.Path, "/decision") {
		r.approvalHandler.MakeDecision(w, req)
		return
	}
	r.approvalHandler.GetApproval(w, req)
}

func (r *Router) handleMemory(w http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodGet:
		r.memoryHandler.ListMemory(w, req)
	case http.MethodPost:
		r.memoryHandler.PutMemory(w, req)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// Helper functions

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func extractPathSuffix(path, prefix string) string {
	if strings.HasPrefix(path, prefix) {
		return strings.TrimPrefix(path, prefix)
	}
	return path
}

func trimSuffix(s, suffix string) string {
	return strings.TrimSuffix(s, suffix)
}

func randomID() string {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	const hex = "0123456789abcdef"
	b := make([]byte, 8)
	for i := range b {
		b[i] = hex[r.Intn(16)]
	}
	return string(b)
}
