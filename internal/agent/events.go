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
	ID        string    `json:"id"`
	RunID     string    `json:"run_id"`
	Type      EventType `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	AgentName string    `json:"agent_name,omitempty"`
	ToolName  string    `json:"tool_name,omitempty"`
	Detail    string    `json:"detail,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// EventRecorder collects events for a run.
type EventRecorder struct {
	events []Event
	runID  string
}

// NewEventRecorder creates an EventRecorder for the given run.
func NewEventRecorder(runID string) *EventRecorder {
	return &EventRecorder{
		events: make([]Event, 0),
		runID:  runID,
	}
}

// Record adds an event.
func (r *EventRecorder) Record(eventType EventType, detail string, meta map[string]any) {
	r.events = append(r.events, Event{
		ID:        fmt.Sprintf("e_%d", len(r.events)+1),
		RunID:     r.runID,
		Type:      eventType,
		Timestamp: time.Now(),
		Detail:    detail,
		Metadata:  meta,
	})
}

// Events returns all recorded events.
func (r *EventRecorder) Events() []Event {
	return r.events
}
