package agent

import (
	"fmt"
	"time"
)

// EventType enumerates the kinds of events that occur during agent execution.
type EventType string

const (
	EventModelCallStart  EventType = "model_call_start"
	EventModelCallEnd    EventType = "model_call_end"
	EventToolCallStart   EventType = "tool_call_start"
	EventToolCallEnd     EventType = "tool_call_end"
	EventACLDenied       EventType = "acl_denied"
	EventHITLInterrupt   EventType = "hitl_interrupt"
	EventHITLResume      EventType = "hitl_resume"
	EventSummaryCompress EventType = "summary_compress"
	EventAgentStart      EventType = "agent_start"
	EventAgentEnd        EventType = "agent_end"
	EventSupervisorRoute EventType = "supervisor_route"
)

// Event records a single event during agent execution.
type Event struct {
	ID        string         `json:"id"`
	RunID     string         `json:"run_id"`
	Type      EventType      `json:"type"`
	Timestamp time.Time      `json:"timestamp"`
	AgentName string         `json:"agent_name,omitempty"`
	ToolName  string         `json:"tool_name,omitempty"`
	Detail    string         `json:"detail,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// ProgressSink observes a run while it is still executing. Callbacks run on the
// goroutine that drives the run (the HTTP handler for a chat request), so an
// implementation may write to the response directly without extra locking.
type ProgressSink interface {
	// OnEvent receives every recorded event as it happens.
	OnEvent(Event)
	// OnDelta receives an incremental content fragment from the model.
	OnDelta(content string)
}

// EventRecorder collects events for a run and forwards them to an optional
// sink so callers can surface progress before the run finishes.
type EventRecorder struct {
	events []Event
	runID  string
	sink   ProgressSink
}

// NewEventRecorder creates an EventRecorder for the given run.
func NewEventRecorder(runID string) *EventRecorder {
	return &EventRecorder{
		events: make([]Event, 0),
		runID:  runID,
	}
}

// ContinueEventRecorder carries earlier events through an approval resume.
func ContinueEventRecorder(runID string, earlier []Event) *EventRecorder {
	recorder := NewEventRecorder(runID)
	recorder.events = append(recorder.events, earlier...)
	return recorder
}

// SetSink attaches a progress sink. Passing nil detaches it.
func (r *EventRecorder) SetSink(sink ProgressSink) {
	r.sink = sink
}

// Streaming reports whether a sink is attached, so callers can skip work that
// only exists to feed it.
func (r *EventRecorder) Streaming() bool {
	return r != nil && r.sink != nil
}

// Record adds an event and forwards it to the sink.
func (r *EventRecorder) Record(eventType EventType, detail string, meta map[string]any) {
	event := Event{
		ID:        fmt.Sprintf("e_%d", len(r.events)+1),
		RunID:     r.runID,
		Type:      eventType,
		Timestamp: time.Now(),
		Detail:    detail,
		Metadata:  meta,
	}
	// Lift the subject out of the metadata so the event is self-describing
	// without the reader having to know each event type's metadata keys.
	if name, ok := meta["tool"].(string); ok {
		event.ToolName = name
	}
	if name, ok := meta["agent"].(string); ok {
		event.AgentName = name
	}
	r.events = append(r.events, event)
	if r.sink != nil {
		r.sink.OnEvent(event)
	}
}

// Delta forwards an incremental content fragment to the sink. Empty fragments
// are dropped so callers can pass model chunks through unexamined.
func (r *EventRecorder) Delta(content string) {
	if r != nil && r.sink != nil && content != "" {
		r.sink.OnDelta(content)
	}
}

// Events returns all recorded events.
func (r *EventRecorder) Events() []Event {
	return r.events
}
