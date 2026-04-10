package pipeline

import (
	"context"
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

func (h StartHandler) Execute(_ context.Context, _ *dot.Node, _ *Context, _ *dot.Graph, _ string) Outcome {
	return Outcome{Status: StatusSuccess}
}

// --- Exit Handler ---

// ExitHandler is a no-op handler for the pipeline exit point. Goal gate
// enforcement is handled by the engine, not by this handler.
type ExitHandler struct{}

func (h ExitHandler) Execute(_ context.Context, _ *dot.Node, _ *Context, _ *dot.Graph, _ string) Outcome {
	return Outcome{Status: StatusSuccess}
}

// --- Conditional Handler ---

// ConditionalHandler is a pass-through for diamond-shaped routing nodes. The
// actual routing logic lives in the engine's edge selection algorithm.
type ConditionalHandler struct{}

func (h ConditionalHandler) Execute(_ context.Context, node *dot.Node, _ *Context, _ *dot.Graph, _ string) Outcome {
	return Outcome{
		Status: StatusSuccess,
		Notes:  "conditional node evaluated: " + node.ID,
	}
}

// --- Codergen Handler ---

const (
	ExhaustionRoundLimit = "round_limit"
	ExhaustionReadLoop   = "read_loop"
)

// BackendResult carries the LLM response and associated token usage from a
// single codergen stage execution.
type BackendResult struct {
	Response         string
	Usage            llm.Usage
	Model            string
	Rounds           int
	Conversation     []llm.Message
	Exhausted        bool   // true when the agent hit the round limit or was terminated early
	ExhaustionReason string // ExhaustionRoundLimit or ExhaustionReadLoop; empty when Exhausted is false
}

// CodergenBackend is the interface for LLM execution backends.
type CodergenBackend interface {
	Run(ctx context.Context, node *dot.Node, prompt string, pctx *Context) (BackendResult, error)
}

// CheckRunner executes a shell command (typically inside the Docker sandbox)
// and returns its combined stdout+stderr output. A nil error means the command
// exited 0. Used by build gates to run compilation checks.
type CheckRunner func(ctx context.Context, cmd string) (output string, err error)

// CodergenHandler is the default handler for all LLM task nodes (shape=box).
// It expands template variables in the prompt, calls the backend, and writes
// prompt/response/status artifacts to the logs directory.
//
// When CheckRunner is non-nil and the node has a check_cmd attribute, the
// handler runs the check after the agent completes. If the check fails, the
// agent is re-invoked with the error output up to check_max_retries times.
//
// WorkDir is the project work directory where agents create files. When set,
// the handler manages a _scratch/ directory lifecycle: setup before the stage,
// summary verification after, and archive+cleanup between stages.
type CodergenHandler struct {
	Backend     CodergenBackend
	CheckRunner CheckRunner
	WorkDir     string
}

func (h CodergenHandler) Execute(ctx context.Context, node *dot.Node, pctx *Context, g *dot.Graph, logsRoot string) Outcome {
	prompt := node.Prompt()
	if prompt == "" {
		prompt = node.NodeLabel()
	}
	prompt = expandVariables(prompt, g, pctx)

	slog.Info("pipeline.stage.start", "node", node.ID, "prompt_len", len(prompt))

	stageDir := filepath.Join(logsRoot, sanitizeNodeID(node.ID))
	_ = os.MkdirAll(stageDir, 0o755)
	_ = os.WriteFile(filepath.Join(stageDir, "prompt.md"), []byte(prompt), 0o644)

	// Scratch lifecycle: set up _scratch/ before the agent runs.
	// WorkDir is intentionally left empty in simulate mode to skip scratch.
	if h.WorkDir != "" {
		completedRaw := pctx.GetString("completed_stages")
		var completed []string
		if completedRaw != "" {
			completed = strings.Split(completedRaw, ",")
		}
		desc := node.Prompt()
		if desc == "" {
			desc = node.NodeLabel()
		}
		if len(desc) > 200 {
			desc = desc[:200] + "..."
		}
		if err := SetupScratch(h.WorkDir, node.ID, completed, desc); err != nil {
			slog.Warn("pipeline.scratch.setup.error", "node", node.ID, "error", err)
		}
	} else {
		slog.Info("pipeline.scratch.skipped", "node", node.ID, "reason", "no work dir (simulate mode)")
	}

	// Filesystem observation: snapshot before agent runs.
	var beforeSnap map[string]int64
	if h.WorkDir != "" {
		var snapErr error
		beforeSnap, snapErr = SnapshotDir(h.WorkDir)
		if snapErr != nil {
			slog.Warn("pipeline.snapshot.before.error", "node", node.ID, "error", snapErr)
		} else {
			slog.Info("pipeline.snapshot.before", "node", node.ID, "files", len(beforeSnap))
		}
	} else {
		slog.Info("pipeline.snapshot.skipped", "node", node.ID, "reason", "no work dir (simulate mode)")
	}

	var responseText string
	var stageUsage *StageUsage
	var lastConversation []llm.Message
	var buildGateAttempts int
	var buildGatePassed *bool

	if h.Backend != nil {
		result, err := h.Backend.Run(ctx, node, prompt, pctx)
		if err != nil {
			slog.Warn("pipeline.stage.fail", "node", node.ID, "error", err)
			outcome := Outcome{
				Status:        StatusFail,
				FailureReason: err.Error(),
				PromptLength:  len(prompt),
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
			lastConversation = result.Conversation
			if convData, err := json.MarshalIndent(result.Conversation, "", "  "); err == nil {
				_ = os.WriteFile(filepath.Join(stageDir, "conversation.json"), convData, 0o644)
				slog.Info("pipeline.conversation.saved", "node", node.ID, "messages", len(result.Conversation))
			}
		}
		if result.Exhausted {
			_ = os.WriteFile(filepath.Join(stageDir, "response.md"), []byte(responseText), 0o644)
			fsDiff := captureFilesystemDiff(h.WorkDir, beforeSnap, node.ID, stageDir)
			var reason string
			if result.ExhaustionReason == ExhaustionReadLoop {
				reason = fmt.Sprintf("agent terminated: persistent read-loop detected after %d rounds", result.Rounds)
			} else {
				reason = fmt.Sprintf("agent exhausted round limit (%d) without completing task", result.Rounds)
			}
			slog.Warn("pipeline.stage.exhausted", "node", node.ID, "rounds", result.Rounds, "reason", result.ExhaustionReason)
			outcome := Outcome{
				Status:           StatusFail,
				FailureReason:    reason,
				Usage:            stageUsage,
				ExhaustionReason: result.ExhaustionReason,
				PromptLength:     len(prompt),
				ResponseLength:   len(responseText),
				FileDiffCounts:   toFileDiffCounts(fsDiff),
			}
			writeStatus(stageDir, outcome)
			return outcome
		}

		// Build gate: run check_cmd after successful completion.
		if checkCmd := node.CheckCmd(); checkCmd != "" && h.CheckRunner != nil {
			gate := runBuildGate(ctx, h.CheckRunner, h.Backend, node, pctx, checkCmd, prompt, stageDir)
			buildGateAttempts = gate.Attempts

			if gate.FailureReason != "" {
				fsDiff := captureFilesystemDiff(h.WorkDir, beforeSnap, node.ID, stageDir)
				failed := false
				buildGatePassed = &failed
				stageUsage.Rounds += gate.ExtraRounds
				stageUsage.InputTokens += gate.ExtraUsage.InputTokens
				stageUsage.OutputTokens += gate.ExtraUsage.OutputTokens
				stageUsage.TotalTokens += gate.ExtraUsage.TotalTokens
				if gate.ResponseText != "" {
					responseText = gate.ResponseText
				}
				outcome := Outcome{
					Status:            StatusFail,
					FailureReason:     gate.FailureReason,
					Usage:             stageUsage,
					ExhaustionReason:  gate.ExhaustionReason,
					PromptLength:      len(prompt),
					ResponseLength:    len(responseText),
					BuildGateAttempts: buildGateAttempts,
					BuildGatePassed:   buildGatePassed,
					FileDiffCounts:    toFileDiffCounts(fsDiff),
				}
				writeStatus(stageDir, outcome)
				return outcome
			}

			passed := true
			buildGatePassed = &passed
			stageUsage.Rounds += gate.ExtraRounds
			stageUsage.InputTokens += gate.ExtraUsage.InputTokens
			stageUsage.OutputTokens += gate.ExtraUsage.OutputTokens
			stageUsage.TotalTokens += gate.ExtraUsage.TotalTokens
			if gate.ResponseText != "" {
				responseText = gate.ResponseText
			}
		}
	} else {
		responseText = "[simulated] response for stage: " + node.ID
	}

	_ = os.WriteFile(filepath.Join(stageDir, "response.md"), []byte(responseText), 0o644)
	slog.Info("pipeline.stage.done", "node", node.ID, "response_len", len(responseText))

	// Filesystem observation: snapshot after agent runs and compute diff.
	fsDiff := captureFilesystemDiff(h.WorkDir, beforeSnap, node.ID, stageDir)

	// Scratch lifecycle: archive, read summary, clean up.
	var scratchSummary string
	scratchSummaryProduced := false
	if h.WorkDir != "" && h.Backend != nil {
		var err error
		scratchSummary, err = ArchiveAndCleanScratch(h.WorkDir, logsRoot, node.ID)
		if err != nil {
			slog.Warn("pipeline.scratch.archive.error", "node", node.ID, "error", err)
		}
		scratchSummaryProduced = scratchSummary != ""
	}

	files := extractFileList(lastConversation)

	// C3: Empty output detection, enhanced with filesystem observation.
	// The conversation-based file list may miss files if the agent used
	// unconventional tool names or patterns. The filesystem diff provides
	// ground truth. We warn if both signals agree that nothing was produced.
	if h.Backend != nil && !node.AllowEmptyOutput() {
		conversationEmpty := len(files) == 0
		fsEmpty := fsDiff == nil || fsDiff.IsEmpty()
		if conversationEmpty && fsEmpty {
			slog.Warn("pipeline.stage.empty_output", "node", node.ID, "source", "both")
		} else if conversationEmpty && !fsEmpty {
			slog.Info("pipeline.stage.empty_conversation_but_fs_changed", "node", node.ID,
				"fs_added", len(fsDiff.Added), "fs_modified", len(fsDiff.Modified))
		}
	}

	var fsDiffStr string
	if fsDiff != nil && !fsDiff.IsEmpty() {
		fsDiffStr = fsDiff.String()
	}
	stageSummary := buildStageSummary(node.ID, files, responseText, scratchSummary, fsDiffStr)

	completedStages := pctx.GetString("completed_stages")
	if completedStages != "" {
		completedStages += "," + node.ID
	} else {
		completedStages = node.ID
	}

	slog.Info("pipeline.context_carryover", "node", node.ID, "files", len(files), "summary_len", len(stageSummary))

	outcome := Outcome{
		Status: StatusSuccess,
		Notes:  "stage completed: " + node.ID,
		Usage:  stageUsage,
		ContextUpdates: map[string]string{
			"last_stage":               node.ID,
			"last_response":            truncate(responseText, 200),
			"completed_stages":         completedStages,
			"stage_summary:" + node.ID: stageSummary,
		},
		PromptLength:           len(prompt),
		ResponseLength:         len(responseText),
		ScratchSummaryProduced: scratchSummaryProduced,
		BuildGateAttempts:      buildGateAttempts,
		BuildGatePassed:        buildGatePassed,
		FileDiffCounts:         toFileDiffCounts(fsDiff),
	}
	writeStatus(stageDir, outcome)
	return outcome
}

// --- Context carryover ---

const (
	summaryResponseMaxLen = 300
	scratchSummaryMaxLen  = 1000
	fsDiffMaxLen          = 2000
)

// extractFileList scans a conversation for write_file and edit_file tool calls
// and returns a deduplicated list of file paths in order of first appearance.
// Paths inside _scratch/ are excluded since they are intermediate working
// notes, not deliverables.
func extractFileList(conversation []llm.Message) []string {
	seen := make(map[string]bool)
	var files []string
	for _, msg := range conversation {
		if msg.Role != llm.RoleAssistant {
			continue
		}
		for _, tc := range msg.ToolCalls() {
			if tc.Name != "write_file" && tc.Name != "edit_file" {
				continue
			}
			var parsed map[string]any
			if err := json.Unmarshal(tc.Arguments, &parsed); err != nil {
				continue
			}
			path, ok := parsed["path"].(string)
			if !ok || path == "" {
				continue
			}
			if isScratchPath(path) {
				continue
			}
			if !seen[path] {
				seen[path] = true
				files = append(files, path)
			}
		}
	}
	return files
}

// isScratchPath returns true if the path is inside the _scratch/ directory.
// Handles both relative paths (_scratch/foo) and absolute paths
// (/work/dir/_scratch/foo).
func isScratchPath(path string) bool {
	clean := filepath.Clean(path)
	parts := strings.Split(clean, string(filepath.Separator))
	for _, part := range parts {
		if part == scratchDir {
			return true
		}
	}
	return false
}

// buildStageSummary formats a concise summary of a completed stage for
// injection into downstream prompts. When scratchSummary is non-empty, it
// includes the agent's synthesized notes from _scratch/SUMMARY.md. When
// fsDiffStr is non-empty, it includes the ground-truth filesystem diff.
func buildStageSummary(nodeID string, files []string, response, scratchSummary, fsDiffStr string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[Stage: %s] completed.", nodeID)
	if len(files) > 0 {
		b.WriteString("\nFiles created/modified: ")
		b.WriteString(strings.Join(files, ", "))
	}
	if fsDiffStr != "" {
		diff := fsDiffStr
		if len(diff) > fsDiffMaxLen {
			diff = diff[:fsDiffMaxLen] + "..."
		}
		fmt.Fprintf(&b, "\nFilesystem changes:\n%s", diff)
	}
	if scratchSummary != "" {
		summary := scratchSummary
		if len(summary) > scratchSummaryMaxLen {
			summary = summary[:scratchSummaryMaxLen] + "..."
		}
		fmt.Fprintf(&b, "\nStage notes: %s", summary)
	}
	if response != "" {
		summary := response
		if len(summary) > summaryResponseMaxLen {
			summary = summary[:summaryResponseMaxLen] + "..."
		}
		fmt.Fprintf(&b, "\nSummary: %s", summary)
	}
	return b.String()
}

// buildPriorContext reads completed stage summaries from the pipeline context
// and formats them as a preamble block for the next stage's prompt.
func buildPriorContext(ctx *Context) string {
	stagesRaw := ctx.GetString("completed_stages")
	if stagesRaw == "" {
		return ""
	}
	stageIDs := strings.Split(stagesRaw, ",")

	var parts []string
	for _, id := range stageIDs {
		summary := ctx.GetString("stage_summary:" + id)
		if summary != "" {
			parts = append(parts, summary)
		}
	}
	if len(parts) == 0 {
		return ""
	}

	return "=== PRIOR STAGE CONTEXT ===\n" +
		strings.Join(parts, "\n\n") +
		"\n=== END PRIOR STAGE CONTEXT ===\n\n"
}

// expandVariables performs variable expansion in prompts:
//   - $goal is replaced with the graph-level goal attribute
//   - $prior_context is replaced with summaries of completed stages
//
// If $prior_context does not appear in the prompt but prior context exists,
// it is automatically prepended.
func expandVariables(prompt string, g *dot.Graph, ctx *Context) string {
	prompt = strings.ReplaceAll(prompt, "$goal", g.Goal())

	priorCtx := buildPriorContext(ctx)
	if strings.Contains(prompt, "$prior_context") {
		prompt = strings.ReplaceAll(prompt, "$prior_context", priorCtx)
	} else if priorCtx != "" {
		prompt = priorCtx + prompt
	}

	return prompt
}

// captureFilesystemDiff takes the after-snapshot, computes the diff against
// beforeSnap, writes it to the stage log directory, and returns it. Returns nil
// when workDir is empty or beforeSnap is nil (simulate mode).
func captureFilesystemDiff(workDir string, beforeSnap map[string]int64, nodeID, stageDir string) *FileDiff {
	if workDir == "" || beforeSnap == nil {
		return nil
	}
	afterSnap, snapErr := SnapshotDir(workDir)
	if snapErr != nil {
		slog.Warn("pipeline.snapshot.after.error", "node", nodeID, "error", snapErr)
		return nil
	}
	diff := DiffSnapshots(beforeSnap, afterSnap)
	slog.Info("pipeline.snapshot.diff", "node", nodeID,
		"added", len(diff.Added), "removed", len(diff.Removed),
		"modified", len(diff.Modified), "unchanged", diff.Unchanged)
	_ = os.WriteFile(filepath.Join(stageDir, "filesystem_diff.txt"), []byte(diff.String()), 0o644)
	return &diff
}

// toFileDiffCounts converts a FileDiff pointer into a FileDiffCounts pointer
// for inclusion in the Outcome.
func toFileDiffCounts(fd *FileDiff) *FileDiffCounts {
	if fd == nil {
		return nil
	}
	return &FileDiffCounts{
		Added:     len(fd.Added),
		Modified:  len(fd.Modified),
		Removed:   len(fd.Removed),
		Unchanged: fd.Unchanged,
	}
}

// buildGateResult carries the outcome of a build gate retry loop. The caller
// uses this to accumulate usage and construct the appropriate Outcome.
type buildGateResult struct {
	Attempts         int
	Passed           bool
	ExtraUsage       llm.Usage
	ExtraRounds      int
	ResponseText     string // latest response from a fix run; empty when no fix ran
	FailureReason    string // non-empty on terminal failure
	ExhaustionReason string // non-empty when a fix run was exhausted
}

// runBuildGate executes the check_cmd and, on failure, retries by calling the
// backend with a fix prompt up to the node's check_max_retries limit. It writes
// check output artifacts to stageDir. The function has no knowledge of
// filesystem snapshots or Outcome construction -- the caller handles those.
func runBuildGate(
	ctx context.Context,
	runner CheckRunner,
	backend CodergenBackend,
	node *dot.Node,
	pctx *Context,
	checkCmd string,
	originalPrompt string,
	stageDir string,
) buildGateResult {
	maxAttempts := node.CheckMaxRetries()
	var result buildGateResult

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		result.Attempts = attempt
		slog.Info("pipeline.buildgate", "node", node.ID, "attempt", attempt, "cmd", checkCmd)
		checkOutput, checkErr := runner(ctx, checkCmd)
		if checkErr == nil {
			slog.Info("pipeline.buildgate.pass", "node", node.ID, "attempt", attempt)
			result.Passed = true
			return result
		}

		slog.Warn("pipeline.buildgate.fail", "node", node.ID, "attempt", attempt, "output", truncate(checkOutput, 500))
		_ = os.WriteFile(filepath.Join(stageDir, fmt.Sprintf("buildgate_attempt_%d.txt", attempt)), []byte(checkOutput), 0o644)

		if attempt == maxAttempts {
			result.FailureReason = fmt.Sprintf("build gate failed after %d attempts: %s", maxAttempts, truncate(checkOutput, 200))
			slog.Error("pipeline.buildgate.exhausted", "node", node.ID, "attempts", maxAttempts)
			return result
		}

		fixPrompt := buildRetryPrompt(originalPrompt, checkOutput)
		fixResult, fixErr := backend.Run(ctx, node, fixPrompt, pctx)
		if fixErr != nil {
			slog.Warn("pipeline.buildgate.fix.error", "node", node.ID, "error", fixErr)
			result.FailureReason = fmt.Sprintf("build gate fix attempt failed: %v", fixErr)
			return result
		}

		result.ExtraRounds += fixResult.Rounds
		result.ExtraUsage.InputTokens += fixResult.Usage.InputTokens
		result.ExtraUsage.OutputTokens += fixResult.Usage.OutputTokens
		result.ExtraUsage.TotalTokens += fixResult.Usage.TotalTokens
		result.ResponseText = fixResult.Response

		if fixResult.Exhausted {
			slog.Warn("pipeline.buildgate.fix.exhausted", "node", node.ID, "attempt", attempt)
			result.FailureReason = fmt.Sprintf("agent exhausted during build gate fix (attempt %d)", attempt)
			result.ExhaustionReason = fixResult.ExhaustionReason
			return result
		}
	}

	return result
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

func (b SimulatedBackend) Run(_ context.Context, node *dot.Node, prompt string, _ *Context) (BackendResult, error) {
	return BackendResult{
		Response: fmt.Sprintf("[simulated] completed stage %s", node.ID),
	}, nil
}
