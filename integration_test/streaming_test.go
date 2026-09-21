package integration_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// sseFrame is one parsed frame of an SSE response.
type sseFrame struct {
	event string
	data  map[string]any
}

// parseSSEFrames splits a complete SSE body into frames in the order the server
// wrote them. That order is the evidence that matters: the handler writes the
// done frame only after the run returns, so any progress frame appearing before
// it was written while the run was still executing.
func parseSSEFrames(t *testing.T, body []byte) []sseFrame {
	t.Helper()
	lines := strings.Split(string(body), "\n")
	var frames []sseFrame
	for i := 0; i < len(lines); i++ {
		if !strings.HasPrefix(lines[i], "event: ") {
			continue
		}
		event := strings.TrimSpace(strings.TrimPrefix(lines[i], "event: "))
		if i+1 >= len(lines) || !strings.HasPrefix(lines[i+1], "data: ") {
			continue
		}
		var data map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(lines[i+1], "data: ")), &data); err != nil {
			t.Fatalf("frame %q carries invalid JSON: %v", event, err)
		}
		frames = append(frames, sseFrame{event: event, data: data})
		i++
	}
	return frames
}

// streamChat posts a streaming chat request and returns the parsed frames.
func streamChat(t *testing.T, serverURL, sessionID, message string) []sseFrame {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{
		"message":  message,
		"threadId": "t_stream",
		"stream":   true,
	})
	req, err := http.NewRequest(http.MethodPost, serverURL+"/api/agent/chat", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("create request failed: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+sessionID)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read stream failed: %v", err)
	}
	return parseSSEFrames(t, raw)
}

// TestIntegration_ChatStreamsIncrementalChunks asserts the answer is delivered
// as several fragments written before the run finished, instead of one payload
// sent after completion.
func TestIntegration_ChatStreamsIncrementalChunks(t *testing.T) {
	application := createTestApp(t)
	server := httptest.NewServer(application.Router.Handler())
	defer server.Close()
	sessionID := doLogin(t, server.URL, "admin", testAdminPassword)

	frames := streamChat(t, server.URL, sessionID, "1+1等于多少")

	var chunks []string
	doneIndex := -1
	for i, frame := range frames {
		switch frame.event {
		case "chunk":
			if doneIndex >= 0 {
				t.Fatalf("chunk arrived after the done frame: %v", frame.data)
			}
			content, _ := frame.data["content"].(string)
			chunks = append(chunks, content)
		case "done":
			doneIndex = i
		}
	}
	if doneIndex < 0 {
		t.Fatal("stream ended without a done frame")
	}
	if len(chunks) < 2 {
		t.Fatalf("expected the answer to arrive in several fragments, got %d: %q", len(chunks), chunks)
	}

	done := frames[doneIndex].data
	answer, _ := done["answer"].(string)
	if answer == "" {
		t.Fatalf("done frame carries no answer: %v", done)
	}
	// Sub-agents share the run's recorder, so fragments can include a child
	// agent's intermediate answer; the parent's answer must still be the tail.
	streamed := strings.Join(chunks, "")
	if !strings.HasSuffix(streamed, answer) {
		t.Fatalf("streamed fragments do not end with the final answer:\nstreamed %q\nanswer   %q", streamed, answer)
	}
	if done["streamed"] != true {
		t.Fatalf("done frame should report streamed=true, got %v", done["streamed"])
	}
	if done["status"] != "completed" {
		t.Fatalf("expected completed, got %v", done["status"])
	}
}

// TestIntegration_StreamEmitsToolProgress asserts tool activity reaches the
// client as it happens rather than only in the final events payload.
func TestIntegration_StreamEmitsToolProgress(t *testing.T) {
	application := createTestApp(t)
	server := httptest.NewServer(application.Router.Handler())
	defer server.Close()
	sessionID := doLogin(t, server.URL, "admin", testAdminPassword)

	frames := streamChat(t, server.URL, sessionID, "1+1等于多少")

	phases := map[string]bool{}
	names := map[string]string{}
	doneIndex := -1
	for i, frame := range frames {
		if frame.event == "done" {
			doneIndex = i
			continue
		}
		if frame.event != "tool_call" {
			continue
		}
		if doneIndex >= 0 {
			t.Fatalf("tool progress arrived after the done frame: %v", frame.data)
		}
		phase, _ := frame.data["phase"].(string)
		phases[phase] = true
		// The UI renders "<phase> <tool>", so the subject must be present.
		if name, _ := frame.data["tool"].(string); name != "" {
			names[phase] = name
		}
	}
	if doneIndex < 0 {
		t.Fatal("stream ended without a done frame")
	}
	// The mock routes math questions to the math sub-agent, which then calls the
	// calculator: both the route and the tool lifecycle must be visible live.
	if !phases["route"] {
		t.Errorf("expected a route progress frame, got phases %v", phases)
	}
	if !phases["start"] || !phases["end"] {
		t.Errorf("expected tool start and end progress frames, got phases %v", phases)
	}
	for _, phase := range []string{"route", "start", "end"} {
		if names[phase] == "" {
			t.Errorf("progress frame %q carries no tool/agent name: %v", phase, frames)
		}
	}
	if names["end"] != "calculator" {
		t.Errorf("expected the calculator tool to be named in the end frame, got %q", names["end"])
	}
}
