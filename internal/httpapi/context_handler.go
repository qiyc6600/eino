package httpapi

import (
	"net/http"

	"github.com/example/agent-eino-demo/internal/agent"
	"github.com/example/agent-eino-demo/internal/contextmgr"
)

// ContextSettingsHandler exposes the context budget for reading and adjustment.
//
// The window and the trigger ratio are separate knobs, and the number on the token
// bar — the threshold — is derived from both:
//
//	threshold = overhead + (window - overhead) × ratio
//
// So "adjust the threshold" has two answers, and the handler returns the derived
// value alongside the two knobs rather than making a caller back-solve it. The
// overhead is not scaled by the ratio: it is the tool schemas and the answer
// reserve, which are spent before any message is considered.
type ContextSettingsHandler struct {
	runner *agent.Runner
}

func NewContextSettingsHandler(runner *agent.Runner) *ContextSettingsHandler {
	return &ContextSettingsHandler{runner: runner}
}

// summarizer is read from the runner rather than passed in: the runner owns the
// instance its runs use, so adjusting it here is guaranteed to affect them.
func (h *ContextSettingsHandler) summarizer() *contextmgr.Summarizer {
	return h.runner.Summarizer()
}

// ContextSettings is the response shape for both GET and PUT, so a client always
// sees the values that actually took effect rather than the ones it asked for.
type ContextSettings struct {
	MaxTokens int `json:"max_tokens"`
	// ThresholdRatio is the share of the usable budget at which compaction fires.
	ThresholdRatio float64 `json:"threshold_ratio"`
	// SummaryTargetTokens is the size the summary is asked to come down to.
	SummaryTargetTokens int `json:"summary_target_tokens"`

	// Derived, read-only: the overhead that is not scaled, the share left for
	// messages, and the resulting trigger.
	Overhead  int `json:"overhead"`
	Usable    int `json:"usable"`
	Threshold int `json:"threshold"`

	// Clamped names the fields whose requested value was outside the working range
	// and was therefore changed. Empty when everything was applied as asked, so a
	// caller can tell "I set 100" from "I set 100 and got 256".
	Clamped []string `json:"clamped,omitempty"`

	// WindowBelowOverhead means the window cannot hold the request overhead, so
	// compaction fires on any history at all and the effective window is the floor.
	WindowBelowOverhead bool `json:"window_below_overhead,omitempty"`
}

func (h *ContextSettingsHandler) Get(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.snapshot(nil))
}

// Update applies a partial change. Only the fields present are touched, so a
// client can adjust one knob without restating the others.
func (h *ContextSettingsHandler) Update(w http.ResponseWriter, r *http.Request) {
	var body struct {
		MaxTokens           *int     `json:"max_tokens"`
		ThresholdRatio      *float64 `json:"threshold_ratio"`
		SummaryTargetTokens *int     `json:"summary_target_tokens"`
	}
	if !decodeBody(w, r, &body) {
		return
	}

	var clamped []string
	if body.MaxTokens != nil {
		requested := *body.MaxTokens
		h.runner.SetMaxTokens(requested)
		if h.runner.MaxTokens() != requested {
			clamped = append(clamped, "max_tokens")
		}
	}
	if body.ThresholdRatio != nil {
		requested := *body.ThresholdRatio
		h.summarizer().SetThresholdRatio(requested)
		if h.summarizer().ThresholdRatio() != requested {
			clamped = append(clamped, "threshold_ratio")
		}
	}
	if body.SummaryTargetTokens != nil {
		requested := *body.SummaryTargetTokens
		h.summarizer().SetSummaryTargetTokens(requested)
		if h.summarizer().SummaryTargetTokens() != requested {
			clamped = append(clamped, "summary_target_tokens")
		}
	}

	writeJSON(w, http.StatusOK, h.snapshot(clamped))
}

func (h *ContextSettingsHandler) snapshot(clamped []string) ContextSettings {
	return ContextSettings{
		MaxTokens:           h.runner.MaxTokens(),
		ThresholdRatio:      h.summarizer().ThresholdRatio(),
		SummaryTargetTokens: h.summarizer().SummaryTargetTokens(),
		Overhead:            h.runner.BaseContextTokens(),
		Usable:              h.runner.UsableContextTokens(),
		Threshold:           h.runner.CompactionThreshold(),
		Clamped:             clamped,
		WindowBelowOverhead: h.runner.WindowBelowOverhead(),
	}
}
