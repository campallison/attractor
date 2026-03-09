package pipeline

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/campallison/attractor/internal/dot"
	"github.com/campallison/attractor/internal/llm"
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

// BackendResult carries the LLM response and associated token usage from a
// single codergen stage execution.
type BackendResult struct {
	Response     string
	Usage        llm.Usage
	Model        string
	Rounds       int
	Conversation []llm.Message
	Exhausted    bool // true when the agent hit the round limit without completing
}

// CodergenBackend is the interface for LLM execution backends.
type CodergenBackend interface {
	Run(node *dot.Node, prompt string, ctx *Context) (BackendResult, error)
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

	slog.Info("pipeline.stage.start", "node", node.ID, "prompt_len", len(prompt))

	stageDir := filepath.Join(logsRoot, sanitizeNodeID(node.ID))
	_ = os.MkdirAll(stageDir, 0o755)
	_ = os.WriteFile(filepath.Join(stageDir, "prompt.md"), []byte(prompt), 0o644)

	var responseText string
	var stageUsage *StageUsage
	if h.Backend != nil {
		result, err := h.Backend.Run(node, prompt, ctx)
		if err != nil {
			slog.Warn("pipeline.stage.fail", "node", node.ID, "error", err)
			outcome := Outcome{
				Status:        StatusFail,
				FailureReason: err.Error(),
			}
			writeStatus(stageDir, outcome)
			return outcome
		}
		responseText = result.Response
		stageUsage = &StageUsage{
			Model:        result.Model,
			Rounds:       result.Rounds,
			InputTokens:  result.Usage.InputTokens,
			OutputTokens: result.Usage.OutputTokens,
			TotalTokens:  result.Usage.TotalTokens,
		}
		if usageData, err := json.MarshalIndent(stageUsage, "", "  "); err == nil {
			_ = os.WriteFile(filepath.Join(stageDir, "usage.json"), usageData, 0o644)
		}
		if len(result.Conversation) > 0 {
			if convData, err := json.MarshalIndent(result.Conversation, "", "  "); err == nil {
				_ = os.WriteFile(filepath.Join(stageDir, "conversation.json"), convData, 0o644)
				slog.Info("pipeline.conversation.saved", "node", node.ID, "messages", len(result.Conversation))
			}
		}
		if result.Exhausted {
			_ = os.WriteFile(filepath.Join(stageDir, "response.md"), []byte(responseText), 0o644)
			reason := fmt.Sprintf("agent exhausted round limit (%d) without completing task", result.Rounds)
			slog.Warn("pipeline.stage.exhausted", "node", node.ID, "rounds", result.Rounds)
			outcome := Outcome{
				Status:        StatusFail,
				FailureReason: reason,
				Usage:         stageUsage,
			}
			writeStatus(stageDir, outcome)
			return outcome
		}
	} else {
		responseText = "[simulated] response for stage: " + node.ID
	}

	_ = os.WriteFile(filepath.Join(stageDir, "response.md"), []byte(responseText), 0o644)
	slog.Info("pipeline.stage.done", "node", node.ID, "response_len", len(responseText))

	outcome := Outcome{
		Status: StatusSuccess,
		Notes:  "stage completed: " + node.ID,
		Usage:  stageUsage,
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

// sanitizeNodeID strips path separators and parent-directory components from a
// node ID so it cannot be used to traverse outside the logs root.
func sanitizeNodeID(id string) string {
	id = strings.ReplaceAll(id, "..", "_")
	id = strings.ReplaceAll(id, string(filepath.Separator), "_")
	if id == "" {
		id = "_unnamed"
	}
	return id
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

func (b SimulatedBackend) Run(node *dot.Node, prompt string, _ *Context) (BackendResult, error) {
	return BackendResult{
		Response: fmt.Sprintf("[simulated] completed stage %s", node.ID),
	}, nil
}
