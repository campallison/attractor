// Package agent implements the coding agent loop for Attractor.
// Layer 2: wire the LLM client and tools into a send-execute-repeat cycle.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/campallison/attractor/internal/llm"
	"github.com/campallison/attractor/internal/tools"
)

const (
	maxRounds = 50

	// defaultMaxTokens is the total output budget per LLM call (thinking +
	// response). Must be large enough for extended thinking plus tool call
	// arguments (e.g., writing a 700-line spec in a single write_file).
	defaultMaxTokens = 32768

	// defaultReasoningMaxTokens caps thinking tokens so they don't consume
	// the entire output budget. This is a subset of defaultMaxTokens;
	// the remainder is available for the model's visible response.
	defaultReasoningMaxTokens = 12288

	// readLoopThreshold is the number of consecutive read-only rounds before
	// the agent loop logs a read-loop warning. A read-only round is one where
	// all tool calls are reads (read_file, grep, glob, shell) with no writes
	// (write_file, edit_file). This value is a starting point; it may need
	// adjustment based on real-world pipeline runs.
	readLoopThreshold = 5

	// maxNudges is the maximum number of course-correction messages injected
	// into the conversation when a read-loop is detected. After this many
	// nudges, further detection events escalate to termination (C4).
	// Set to 2 to accommodate complex stages that legitimately need extended
	// reading (e.g., handlers reading spec + model + store + templates).
	maxNudges = 2
)

// ErrRoundLimitReached is returned by RunTaskCapture when the agent exhausts
// all rounds without naturally completing (i.e., the model never stopped
// calling tools). The caller can still inspect the returned text, usage, and
// conversation for post-mortem analysis.
var ErrRoundLimitReached = errors.New("agent: round limit reached without completing task")

// ErrReadLoopDetected is returned by RunTaskCapture when the agent persists
// in a read-loop after receiving a nudge. This indicates the agent is stuck
// reading files without producing output, and further rounds would waste
// tokens without progress. Distinct from ErrRoundLimitReached so the handler
// can report the specific failure mode.
var ErrReadLoopDetected = errors.New("agent: read-loop detected after nudge, terminating early")

// Completer is the interface for making LLM completion calls. Both *llm.Client
// and test mocks satisfy this interface.
type Completer interface {
	Complete(ctx context.Context, req llm.Request) (llm.Response, error)
}

// RunTask executes an agentic loop: sends a prompt to the LLM with tool
// definitions, executes any tool calls the model requests, feeds results back,
// and repeats until the model responds with text only or the round limit is hit.
//
// The caller must supply a pre-built tool registry. Use tools.DefaultRegistry
// to construct one with the standard tool set.
func RunTask(ctx context.Context, client Completer, model, prompt, workDir string, registry *tools.Registry) error {
	systemPrompt := BuildSystemPrompt(workDir)

	conversation := []llm.Message{
		llm.SystemMessage(systemPrompt),
		llm.UserMessage(prompt),
	}

	toolDefs := registry.Definitions()
	var totalUsage llm.Usage

	for round := 0; round < maxRounds; round++ {
		slog.Info("agent.round", "round", round+1, "max", maxRounds)
		compressed := compressHistory(conversation, defaultKeepFullRounds)
		resp, err := client.Complete(ctx, llm.Request{
			Model:              model,
			Messages:           compressed,
			Tools:              toolDefs,
			MaxTokens:          defaultMaxTokens,
			ReasoningMaxTokens: defaultReasoningMaxTokens,
		})
		if err != nil {
			return fmt.Errorf("agent: LLM call failed on round %d: %w", round, err)
		}

		totalUsage = totalUsage.Add(resp.Usage)

		if text := resp.Text(); text != "" {
			fmt.Printf("[assistant] %s\n", text)
			slog.Debug("agent.assistant", "text", summarize(text, 200))
		}

		toolCalls := resp.ToolCalls()
		if len(toolCalls) == 0 {
			slog.Info("agent.complete", "rounds", round+1, "tokens_in", totalUsage.InputTokens, "tokens_out", totalUsage.OutputTokens)
			fmt.Printf("[done] Total usage: in=%d out=%d total=%d\n",
				totalUsage.InputTokens, totalUsage.OutputTokens, totalUsage.TotalTokens)
			return nil
		}

		conversation = append(conversation, resp.Message)

		for _, tc := range toolCalls {
			result := executeTool(ctx, registry, tc, workDir)
			slog.Info("agent.tool", "tool", tc.Name, "result_bytes", len(result.Content), "is_error", result.IsError)
			fmt.Printf("[tool] %s -> %s\n", tc.Name, summarize(result.Content, 120))
			conversation = append(conversation, llm.ToolResultMessage(
				result.ToolCallID,
				result.Content,
				result.IsError,
			))
		}
	}

	slog.Warn("agent.round_limit", "rounds", maxRounds, "tokens_in", totalUsage.InputTokens, "tokens_out", totalUsage.OutputTokens)
	fmt.Printf("[warning] Round limit (%d) reached. Stopping.\n", maxRounds)
	return nil
}

// executeTool looks up a tool call in the registry and executes it. Unknown
// tools or execution errors are returned as error results so the model can
// adjust its approach.
func executeTool(ctx context.Context, registry *tools.Registry, tc llm.ToolCall, workDir string) llm.ToolResultData {
	registered, ok := registry.Get(tc.Name)
	if !ok {
		return llm.ToolResultData{
			ToolCallID: tc.ID,
			Content:    fmt.Sprintf("Unknown tool: %s", tc.Name),
			IsError:    true,
		}
	}

	output, err := registered.Execute(ctx, json.RawMessage(tc.Arguments), workDir)
	if err != nil {
		return llm.ToolResultData{
			ToolCallID: tc.ID,
			Content:    fmt.Sprintf("Tool error (%s): %s", tc.Name, err.Error()),
			IsError:    true,
		}
	}

	truncated := tools.TruncateToolOutput(output, tc.Name)
	return llm.ToolResultData{
		ToolCallID: tc.ID,
		Content:    truncated,
		IsError:    false,
	}
}

// RunTaskCapture runs the same agentic loop as RunTask but captures and
// returns the final assistant text response instead of printing it. Used by
// the pipeline engine's codergen handler. It also returns accumulated token
// usage and the number of LLM rounds executed.
//
// maxRoundsOverride controls how many rounds the agent may run. When 0, the
// package-level default (50) is used. Pipeline stages can set this via the
// max_rounds DOT attribute to impose tighter or looser limits per stage.
//
// The caller must supply a pre-built tool registry. Use tools.DefaultRegistry
// to construct one with the standard tool set.
func RunTaskCapture(ctx context.Context, client Completer, model, prompt, workDir string, maxRoundsOverride int, registry *tools.Registry) (string, llm.Usage, int, []llm.Message, error) {
	limit := maxRounds
	if maxRoundsOverride > 0 {
		limit = maxRoundsOverride
	}

	systemPrompt := BuildSystemPrompt(workDir)

	conversation := []llm.Message{
		llm.SystemMessage(systemPrompt),
		llm.UserMessage(prompt),
	}

	toolDefs := registry.Definitions()
	var lastText string
	var totalUsage llm.Usage
	var consecutiveReadOnlyRounds int
	var nudgeCount int

	for round := 0; round < limit; round++ {
		slog.Info("agent.round", "round", round+1, "max", limit)
		compressed := compressHistory(conversation, defaultKeepFullRounds)
		resp, err := client.Complete(ctx, llm.Request{
			Model:              model,
			Messages:           compressed,
			Tools:              toolDefs,
			MaxTokens:          defaultMaxTokens,
			ReasoningMaxTokens: defaultReasoningMaxTokens,
		})
		if err != nil {
			return "", totalUsage, round, conversation, fmt.Errorf("agent: LLM call failed on round %d: %w", round, err)
		}

		totalUsage = totalUsage.Add(resp.Usage)

		if text := resp.Text(); text != "" {
			lastText = text
			slog.Debug("agent.assistant", "text", summarize(text, 200))
		}

		toolCalls := resp.ToolCalls()
		if len(toolCalls) == 0 {
			slog.Info("agent.complete", "rounds", round+1, "tokens_in", totalUsage.InputTokens, "tokens_out", totalUsage.OutputTokens)
			return lastText, totalUsage, round + 1, conversation, nil
		}

		conversation = append(conversation, resp.Message)

		for _, tc := range toolCalls {
			result := executeTool(ctx, registry, tc, workDir)
			slog.Info("agent.tool", "tool", tc.Name, "result_bytes", len(result.Content), "is_error", result.IsError)
			conversation = append(conversation, llm.ToolResultMessage(
				result.ToolCallID,
				result.Content,
				result.IsError,
			))
		}

		if isReadOnlyRound(toolCalls) {
			consecutiveReadOnlyRounds++
			if consecutiveReadOnlyRounds >= readLoopThreshold {
				slog.Warn("agent.read_loop_detected",
					"consecutive_read_rounds", consecutiveReadOnlyRounds,
					"round", round+1,
					"nudge_count", nudgeCount,
					"tokens_in", totalUsage.InputTokens,
					"tokens_out", totalUsage.OutputTokens,
				)
				if nudgeCount < maxNudges {
					nudgeCount++
					nudgeMsg := fmt.Sprintf(
						"[PIPELINE ENGINE] You have been reading files for %d consecutive rounds "+
							"without writing any output. Remember to maintain working notes in _scratch/ "+
							"as you go. If you have gathered enough information, begin writing your "+
							"deliverables now.", consecutiveReadOnlyRounds)
					conversation = append(conversation, llm.UserMessage(nudgeMsg))
					slog.Info("agent.read_loop_nudge", "nudge_count", nudgeCount, "round", round+1)
					consecutiveReadOnlyRounds = 0
				} else {
					slog.Warn("agent.read_loop_terminated",
						"round", round+1,
						"nudge_count", nudgeCount,
						"tokens_in", totalUsage.InputTokens,
						"tokens_out", totalUsage.OutputTokens,
					)
					return lastText, totalUsage, round + 1, conversation, ErrReadLoopDetected
				}
			}
		} else {
			consecutiveReadOnlyRounds = 0
			nudgeCount = 0 // a write proves the agent isn't stuck; reset for future read phases
		}
	}

	slog.Warn("agent.round_limit", "rounds", limit, "tokens_in", totalUsage.InputTokens, "tokens_out", totalUsage.OutputTokens)
	return lastText, totalUsage, limit, conversation, ErrRoundLimitReached
}

// summarize returns the first n characters of s, appending "..." if truncated.
func summarize(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// writingTools are tool names that produce filesystem output. Keep in sync
// with tools registered in tools.DefaultRegistry that create or modify files.
var writingTools = map[string]bool{
	"write_file": true,
	"edit_file":  true,
}

// isReadOnlyRound returns true if none of the tool calls in a round write
// to the filesystem. An empty tool call list returns false (no-tool rounds
// end the agent loop before this is called).
func isReadOnlyRound(toolCalls []llm.ToolCall) bool {
	if len(toolCalls) == 0 {
		return false
	}
	for _, tc := range toolCalls {
		if writingTools[tc.Name] {
			return false
		}
	}
	return true
}
