package pipeline

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/campallison/attractor/internal/dot"
)

// --- Start Handler ---

// StartHandler is a no-op handler for the pipeline entry point.
type StartHandler struct{}

func (h StartHandler) Execute(_ *dot.Node, _ *Context, _ *dot.Graph, _ string) Outcome {
	return Outcome{Status: StatusSuccess}
}

// --- Exit Handler ---

// ExitHandler is a no-op handler for the pipeline exit point. Goal gate
// enforcement is handled by the engine, not by this handler.
type ExitHandler struct{}

func (h ExitHandler) Execute(_ *dot.Node, _ *Context, _ *dot.Graph, _ string) Outcome {
	return Outcome{Status: StatusSuccess}
}

// --- Conditional Handler ---

// ConditionalHandler is a pass-through for diamond-shaped routing nodes. The
// actual routing logic lives in the engine's edge selection algorithm.
type ConditionalHandler struct{}

func (h ConditionalHandler) Execute(node *dot.Node, _ *Context, _ *dot.Graph, _ string) Outcome {
	return Outcome{
		Status: StatusSuccess,
		Notes:  "conditional node evaluated: " + node.ID,
	}
}

// --- Codergen Handler ---

// CodergenBackend is the interface for LLM execution backends. The pipeline
// engine only cares that it gets a string response or an error back.
type CodergenBackend interface {
	Run(node *dot.Node, prompt string, ctx *Context) (string, error)
}

// CodergenHandler is the default handler for all LLM task nodes (shape=box).
// It expands template variables in the prompt, calls the backend, and writes
// prompt/response/status artifacts to the logs directory.
type CodergenHandler struct {
	Backend CodergenBackend
}

func (h CodergenHandler) Execute(node *dot.Node, ctx *Context, g *dot.Graph, logsRoot string) Outcome {
	prompt := node.Prompt()
	if prompt == "" {
		prompt = node.NodeLabel()
	}
	prompt = expandVariables(prompt, g, ctx)

	stageDir := filepath.Join(logsRoot, node.ID)
	_ = os.MkdirAll(stageDir, 0o755)
	_ = os.WriteFile(filepath.Join(stageDir, "prompt.md"), []byte(prompt), 0o644)

	var responseText string
	if h.Backend != nil {
		result, err := h.Backend.Run(node, prompt, ctx)
		if err != nil {
			outcome := Outcome{
				Status:        StatusFail,
				FailureReason: err.Error(),
			}
			writeStatus(stageDir, outcome)
			return outcome
		}
		responseText = result
	} else {
		responseText = "[simulated] response for stage: " + node.ID
	}

	_ = os.WriteFile(filepath.Join(stageDir, "response.md"), []byte(responseText), 0o644)

	outcome := Outcome{
		Status: StatusSuccess,
		Notes:  "stage completed: " + node.ID,
		ContextUpdates: map[string]string{
			"last_stage":    node.ID,
			"last_response": truncate(responseText, 200),
		},
	}
	writeStatus(stageDir, outcome)
	return outcome
}

// expandVariables performs simple $goal variable expansion in prompts.
func expandVariables(prompt string, g *dot.Graph, _ *Context) string {
	return strings.ReplaceAll(prompt, "$goal", g.Goal())
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// statusJSON is the on-disk format for status.json, matching Appendix C.
type statusJSON struct {
	Outcome            string            `json:"outcome"`
	PreferredNextLabel string            `json:"preferred_next_label,omitempty"`
	SuggestedNextIDs   []string          `json:"suggested_next_ids,omitempty"`
	ContextUpdates     map[string]string `json:"context_updates,omitempty"`
	Notes              string            `json:"notes,omitempty"`
}

func writeStatus(stageDir string, o Outcome) {
	s := statusJSON{
		Outcome:            string(o.Status),
		PreferredNextLabel: o.PreferredLabel,
		SuggestedNextIDs:   o.SuggestedNextIDs,
		ContextUpdates:     o.ContextUpdates,
		Notes:              o.Notes,
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(stageDir, "status.json"), data, 0o644)
}

// DefaultHandlerRegistry creates a registry pre-loaded with the Phase 1
// built-in handlers. The codergen handler uses the provided backend (may be
// nil for simulation mode).
func DefaultHandlerRegistry(backend CodergenBackend) *HandlerRegistry {
	codergen := CodergenHandler{Backend: backend}
	r := NewHandlerRegistry(codergen)
	r.Register("start", StartHandler{})
	r.Register("exit", ExitHandler{})
	r.Register("conditional", ConditionalHandler{})
	r.Register("codergen", codergen)
	return r
}

// --- Simulation backend (for testing without an LLM) ---

// SimulatedBackend always returns a canned response. Useful for testing the
// pipeline engine without making real LLM calls.
type SimulatedBackend struct{}

func (b SimulatedBackend) Run(node *dot.Node, prompt string, _ *Context) (string, error) {
	return fmt.Sprintf("[simulated] completed stage %s", node.ID), nil
}
