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

// CheckRunner executes a shell command (typically inside the Docker sandbox)
// and returns its combined stdout+stderr output. A nil error means the command
// exited 0. Used by build gates to run compilation checks.
type CheckRunner func(cmd string) (output string, err error)

// CodergenHandler is the default handler for all LLM task nodes (shape=box).
// It expands template variables in the prompt, calls the backend, and writes
// prompt/response/status artifacts to the logs directory.
//
// When CheckRunner is non-nil and the node has a check_cmd attribute, the
// handler runs the check after the agent completes. If the check fails, the
// agent is re-invoked with the error output up to check_max_retries times.
type CodergenHandler struct {
	Backend     CodergenBackend
	CheckRunner CheckRunner
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

		// Build gate: run check_cmd after successful completion.
		if checkCmd := node.CheckCmd(); checkCmd != "" && h.CheckRunner != nil {
			maxAttempts := node.CheckMaxRetries()
			for attempt := 1; attempt <= maxAttempts; attempt++ {
				slog.Info("pipeline.buildgate", "node", node.ID, "attempt", attempt, "cmd", checkCmd)
				checkOutput, checkErr := h.CheckRunner(checkCmd)
				if checkErr == nil {
					slog.Info("pipeline.buildgate.pass", "node", node.ID, "attempt", attempt)
					break
				}
				slog.Warn("pipeline.buildgate.fail", "node", node.ID, "attempt", attempt, "output", truncate(checkOutput, 500))
				_ = os.WriteFile(filepath.Join(stageDir, fmt.Sprintf("buildgate_attempt_%d.txt", attempt)), []byte(checkOutput), 0o644)

				if attempt == maxAttempts {
					reason := fmt.Sprintf("build gate failed after %d attempts: %s", maxAttempts, truncate(checkOutput, 200))
					slog.Error("pipeline.buildgate.exhausted", "node", node.ID, "attempts", maxAttempts)
					outcome := Outcome{
						Status:        StatusFail,
						FailureReason: reason,
						Usage:         stageUsage,
					}
					writeStatus(stageDir, outcome)
					return outcome
				}

				fixPrompt := prompt + "\n\n--- BUILD GATE FAILURE ---\nThe following compilation/check errors were found after your changes. Fix them:\n\n" + checkOutput
				fixResult, fixErr := h.Backend.Run(node, fixPrompt, ctx)
				if fixErr != nil {
					slog.Warn("pipeline.buildgate.fix.error", "node", node.ID, "error", fixErr)
					outcome := Outcome{
						Status:        StatusFail,
						FailureReason: fmt.Sprintf("build gate fix attempt failed: %v", fixErr),
						Usage:         stageUsage,
					}
					writeStatus(stageDir, outcome)
					return outcome
				}

				stageUsage.Rounds += fixResult.Rounds
				stageUsage.InputTokens += fixResult.Usage.InputTokens
				stageUsage.OutputTokens += fixResult.Usage.OutputTokens
				stageUsage.TotalTokens += fixResult.Usage.TotalTokens
				responseText = fixResult.Response

				if fixResult.Exhausted {
					reason := fmt.Sprintf("agent exhausted during build gate fix (attempt %d)", attempt)
					slog.Warn("pipeline.buildgate.fix.exhausted", "node", node.ID, "attempt", attempt)
					outcome := Outcome{
						Status:        StatusFail,
						FailureReason: reason,
						Usage:         stageUsage,
					}
					writeStatus(stageDir, outcome)
					return outcome
				}
			}
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
// built-in handlers. The caller constructs the CodergenHandler with the
// desired backend and optional CheckRunner for build gates.
func DefaultHandlerRegistry(codergen CodergenHandler) *HandlerRegistry {
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
