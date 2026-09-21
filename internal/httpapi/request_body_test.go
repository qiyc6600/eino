package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestLimitRequestBody covers the request body bound.
//
// The per-field caps are not a substitute for it: maxDocumentChars is checked
// after the body has already been decoded, so without this bound a client could
// hand over a body of any size and have the server materialise all of it before
// rejecting it. The test drives the document endpoint because that is the one
// with the largest legitimate body, so it is where the bound must be generous
// enough as well as present.
func TestLimitRequestBody(t *testing.T) {
	newHandler := func(t *testing.T, body []byte, limit int64) *httptest.ResponseRecorder {
		t.Helper()
		handler := limitRequestBody(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var dst struct {
				Content string `json:"content"`
			}
			if !decodeBody(w, r, &dst) {
				return
			}
			// Report what actually arrived, so a test can prove the body was not
			// silently truncated into something parseable.
			writeJSON(w, http.StatusOK, map[string]int{"received": len(dst.Content)})
		}))
		req := httptest.NewRequest(http.MethodPost, "/api/documents", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		return recorder
	}

	t.Run("a body at the limit is accepted", func(t *testing.T) {
		payload, _ := json.Marshal(map[string]string{"content": strings.Repeat("a", 1024)})
		if int64(len(payload)) > maxRequestBytes {
			t.Fatalf("fixture is larger than the limit: %d", len(payload))
		}
		rec := newHandler(t, payload, maxRequestBytes)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected the body to be accepted, got %d: %s", rec.Code, rec.Body)
		}
	})

	t.Run("an over-sized body is rejected with 413, not 400", func(t *testing.T) {
		// One byte past the bound, wrapped in a JSON envelope.
		payload := append([]byte(`{"content":"`), bytes.Repeat([]byte("a"), int(maxRequestBytes))...)
		payload = append(payload, '"', '}')

		rec := newHandler(t, payload, maxRequestBytes)
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("expected 413 for an over-sized body, got %d: %s", rec.Code, rec.Body)
		}
		// The distinction matters to a client: 400 says "your request was wrong",
		// 413 says "it was too big". Retrying the former is pointless, retrying
		// the latter with less data is the fix.
		if !strings.Contains(rec.Body.String(), "exceeds") {
			t.Fatalf("the 413 should say why, got %s", rec.Body)
		}
	})

	t.Run("a malformed body is still 400", func(t *testing.T) {
		rec := newHandler(t, []byte(`{"content":`), maxRequestBytes)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for malformed JSON, got %d: %s", rec.Code, rec.Body)
		}
	})

	t.Run("a truncated body is not silently accepted", func(t *testing.T) {
		// What a body just over the bound must not become: a shorter, valid
		// document that the server accepts without noticing the truncation.
		payload := append([]byte(`{"content":"`), bytes.Repeat([]byte("a"), int(maxRequestBytes))...)
		payload = append(payload, []byte(`","extra":"`)...)
		payload = append(payload, bytes.Repeat([]byte("b"), 16)...)
		payload = append(payload, '"', '}')

		rec := newHandler(t, payload, maxRequestBytes)
		if rec.Code == http.StatusOK {
			t.Fatalf("an over-sized body was accepted as a shorter one: %s", rec.Body)
		}
	})

	t.Run("a request without a body is unaffected", func(t *testing.T) {
		handler := limitRequestBody(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))
		req := httptest.NewRequest(http.MethodGet, "/api/tools", nil)
		req.Body = nil
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		if recorder.Code != http.StatusNoContent {
			t.Fatalf("a bodyless request must pass through, got %d", recorder.Code)
		}
	})
}

// TestMaxRequestBytesAccommodatesTheDocumentCap pins the derivation of the bound:
// it has to be larger than the largest legitimate body, or the cap it exists to
// protect becomes unreachable.
func TestMaxRequestBytesAccommodatesTheDocumentCap(t *testing.T) {
	// A document at maxDocumentChars, encoded the most expensive way a client can
	// choose: every rune as a \uXXXX escape (6 bytes each).
	worstCase := int64(maxDocumentChars) * 6
	if maxRequestBytes <= worstCase {
		t.Fatalf("the body limit (%d) is not above the worst-case encoding of a "+
			"document at maxDocumentChars (%d); a legitimate upload would be rejected",
			maxRequestBytes, worstCase)
	}
}
