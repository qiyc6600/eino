package httpapi

import (
	"errors"
	"net/http"

	"github.com/example/agent-eino-demo/internal/auth"
	"github.com/example/agent-eino-demo/internal/memory"
)

// DocumentHandler handles document RAG endpoints.
type DocumentHandler struct {
	memSvc *memory.Service
}

// NewDocumentHandler creates a new DocumentHandler.
func NewDocumentHandler(memSvc *memory.Service) *DocumentHandler {
	return &DocumentHandler{memSvc: memSvc}
}

// maxDocumentChars bounds one document so a single upload cannot fill the store.
const maxDocumentChars = 200_000

// ListDocuments handles GET /api/documents
// Chunk text is deliberately excluded: the list is for management, and the
// chunks can be far larger than the metadata.
func (h *DocumentHandler) ListDocuments(w http.ResponseWriter, r *http.Request) {
	ac := auth.FromContext(r.Context())
	if ac == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	docs, err := h.memSvc.ListDocuments(r.Context(), ac.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, docs)
}

// IngestDocumentRequest is the request body for POST /api/documents
type IngestDocumentRequest struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

// IngestDocument handles POST /api/documents
func (h *DocumentHandler) IngestDocument(w http.ResponseWriter, r *http.Request) {
	ac := auth.FromContext(r.Context())
	if ac == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	var req IngestDocumentRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if len([]rune(req.Content)) > maxDocumentChars {
		writeError(w, http.StatusRequestEntityTooLarge, "document is too large")
		return
	}

	doc, err := h.memSvc.IngestDocument(r.Context(), ac.UserID, req.Name, req.Content)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

// DeleteDocument handles DELETE /api/documents/{id}
// A document owned by someone else reads as missing rather than forbidden, so the
// response never confirms that another user's document exists.
func (h *DocumentHandler) DeleteDocument(w http.ResponseWriter, r *http.Request) {
	ac := auth.FromContext(r.Context())
	if ac == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	id := extractPathSuffix(r.URL.Path, "/api/documents/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "document id is required")
		return
	}

	if err := h.memSvc.DeleteDocument(r.Context(), ac.UserID, id); err != nil {
		if errors.Is(err, memory.ErrDocumentNotFound) {
			writeError(w, http.StatusNotFound, "document not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
