package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/rand"
	"net/http"
	"strconv"
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
	documentHandler *DocumentHandler
	userHandler     *UserHandler
	modelHandler    *ModelHandler
	healthHandler   *HealthHandler
	authMiddleware  func(http.Handler) http.Handler
	contextHandler  *ContextSettingsHandler
}

// NewRouter creates a new Router with all handlers.
func NewRouter(
	authSvc *auth.Service,
	runner *agent.Runner,
	hitlSvc *hitl.Service,
	memSvc *memory.Service,
	registry *tools.ToolRegistry,
	modelSwitcher ModelSwitcher,
	readinessChecker ReadinessChecker,
	cookie SessionCookieConfig,
) *Router {
	return &Router{
		authHandler:     NewAuthHandler(authSvc, cookie),
		agentHandler:    NewAgentHandler(runner, memSvc),
		approvalHandler: NewApprovalHandler(hitlSvc, runner),
		memoryHandler:   NewMemoryHandler(memSvc),
		documentHandler: NewDocumentHandler(memSvc),
		userHandler:     NewUserHandler(registry),
		modelHandler:    NewModelHandler(modelSwitcher),
		healthHandler:   NewHealthHandler(readinessChecker),
		authMiddleware:  auth.AuthMiddleware(authSvc, cookie),
		contextHandler:  NewContextSettingsHandler(runner),
	}
}

// Handler returns the complete http.Handler with all routes.
func (r *Router) Handler() http.Handler {
	mux := http.NewServeMux()

	// Static files (no auth required) — serve embedded web UI
	staticFS, _ := fs.Sub(web.StaticFS, ".")
	fileServer := http.FileServer(http.FS(staticFS))
	indexHTML, _ := fs.ReadFile(staticFS, "index.html")
	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		// Only serve static files for non-API paths
		if strings.HasPrefix(req.URL.Path, "/api/") {
			http.NotFound(w, req)
			return
		}
		// Never let the browser heuristically cache the UI: a stale app.js
		// after a server upgrade silently disables new frontend features.
		w.Header().Set("Cache-Control", "no-cache, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("X-App-Version", web.AssetVersion())

		// index.html carries the version of the build that served it, so an open
		// tab can notice that the server has moved on. No-cache stops the browser
		// from reusing a stale copy, but nothing stops a *running* page from
		// executing the JS it already loaded — which is how a fixed bug can appear
		// unfixed until the tab is reloaded by hand.
		if req.URL.Path == "/" || req.URL.Path == "/index.html" {
			// Every occurrence: the meta tag and both asset URLs carry it, and a
			// half-substituted page leaves the asset URLs literally pointing at
			// "?v={{ASSET_VERSION}}".
			page := strings.ReplaceAll(string(indexHTML), web.AssetVersionPlaceholder, web.AssetVersion())
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Content-Length", strconv.Itoa(len(page)))
			if req.Method != http.MethodHead {
				_, _ = io.WriteString(w, page)
			}
			return
		}
		fileServer.ServeHTTP(w, req)
	})

	// Public routes (no auth required)
	mux.HandleFunc("/healthz", r.healthHandler.Live)
	mux.HandleFunc("/readyz", r.healthHandler.Ready)
	mux.HandleFunc("/api/auth/login", r.authHandler.Login)

	// Protected routes (auth required) — register directly on main mux with auth middleware
	authMw := r.authMiddleware

	// Auth
	mux.Handle("/api/auth/me", authMw(http.HandlerFunc(r.authHandler.Me)))
	mux.Handle("/api/auth/logout", authMw(http.HandlerFunc(r.authHandler.Logout)))

	// Users & Roles. Creating a user takes an arbitrary role list, so these are
	// administration: without the guard any signed-in account could mint an
	// administrator or promote itself.
	adminMw := func(h http.Handler) http.Handler { return authMw(requireAdmin(h)) }
	mux.Handle("/api/users", adminMw(http.HandlerFunc(r.handleUsers)))
	mux.Handle("/api/users/", adminMw(http.HandlerFunc(r.handleUsersSub)))
	// The role list itself is not sensitive; the UI needs it to render.
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
	mux.Handle("/api/memory/consolidate", authMw(http.HandlerFunc(r.memoryHandler.ConsolidateMemory)))
	mux.Handle("/api/memory/settings", authMw(http.HandlerFunc(r.memoryHandler.MemorySettings)))
	mux.Handle("/api/memory/", authMw(http.HandlerFunc(r.memoryHandler.DeleteMemory)))

	// Documents (per-user RAG corpus)
	mux.Handle("/api/documents", authMw(http.HandlerFunc(r.handleDocuments)))
	mux.Handle("/api/documents/", authMw(http.HandlerFunc(r.documentHandler.DeleteDocument)))

	// Model switching
	// Listing is harmless and the selector needs it; switching changes the model for
	// every user of the server, so it is administration.
	mux.Handle("/api/models", authMw(http.HandlerFunc(r.modelHandler.ListModels)))
	mux.Handle("/api/models/switch", adminMw(http.HandlerFunc(r.modelHandler.SwitchModel)))

	// Context budget: readable by anyone who can see the token bar, adjustable only
	// by an administrator, since the window is shared by every user.
	mux.Handle("/api/context/settings", authMw(http.HandlerFunc(r.handleContextSettings)))

	// The body limit wraps the whole mux, so it covers the public login route as
	// well as every authenticated one — login is the only decode site reachable
	// without a session, and it is the one most worth bounding.
	return limitRequestBody(mux)
}

// maxRequestBytes bounds one request body.
//
// The per-field caps are not a substitute: maxDocumentChars is checked after the
// body has already been decoded, so a client could hand over a body of any size
// and have the server materialise all of it before rejecting it. This bound is
// what makes that impossible.
//
// The value is derived from the largest legitimate body: a document at
// maxDocumentChars (200,000 runes) is about 600KB as UTF-8, and up to 1.2MB if a
// client sends \uXXXX escapes for every rune. 4MB leaves room for the name, the
// JSON envelope and future growth without letting an unbounded body through.
const maxRequestBytes = 4 << 20

func limitRequestBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
		}
		next.ServeHTTP(w, r)
	})
}

// decodeBody reads a JSON request body and reports whether it was usable,
// writing the response itself when it was not.
//
// An over-sized body is a different failure from a malformed one and gets its own
// status: the caller hit the configured bound rather than sending something the
// server could not parse, and a client that retries on 400 would retry forever.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge,
				fmt.Sprintf("request body exceeds %d bytes", maxRequestBytes))
			return false
		}
		writeError(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	return true
}

// handleContextSettings routes the context budget: reading it is open to any
// authenticated user so the token bar can explain itself, changing it is not.
func (r *Router) handleContextSettings(w http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodGet:
		r.contextHandler.Get(w, req)
	case http.MethodPut:
		requireAdmin(http.HandlerFunc(r.contextHandler.Update)).ServeHTTP(w, req)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
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
		if strings.HasSuffix(req.URL.Path, "/tokens") {
			// GET /api/chat/{threadId}/tokens
			r.agentHandler.GetThreadTokens(w, req)
			return
		}
		// GET /api/chat/{threadId}/messages
		r.agentHandler.GetThreadMessages(w, req)
		return
	}
	writeError(w, http.StatusNotFound, "not found")
}

func (r *Router) handleApprovalsSub(w http.ResponseWriter, req *http.Request) {
	// Checked before the id path, or "history" would be read as an interrupt id.
	if strings.HasSuffix(req.URL.Path, "/history") {
		r.approvalHandler.ListHistory(w, req)
		return
	}
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

func (r *Router) handleDocuments(w http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodGet:
		r.documentHandler.ListDocuments(w, req)
	case http.MethodPost:
		r.documentHandler.IngestDocument(w, req)
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
