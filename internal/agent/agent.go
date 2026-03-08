// Package agent implements the coding agent loop for Attractor.
// Layer 2: wire the LLM client and tools into a send-execute-repeat cycle.
package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/campallison/attractor/internal/llm"
	"github.com/campallison/attractor/internal/tools"
)

const maxRounds = 50

// Completer is the interface for making LLM completion calls. Both *llm.Client
// and test mocks satisfy this interface.
type Completer interface {
	Complete(ctx context.Context, req llm.Request) (llm.Response, error)
}

// RunTask executes an agentic loop: sends a prompt to the LLM with tool
// definitions, executes any tool calls the model requests, feeds results back,
// and repeats until the model responds with text only or the round limit is hit.
func RunTask(ctx context.Context, client Completer, model, prompt, workDir string) error {
	registry := tools.DefaultRegistry("attractor-sandbox")
	systemPrompt := BuildSystemPrompt(workDir)

	conversation := []llm.Message{
		llm.SystemMessage(systemPrompt),
		llm.UserMessage(prompt),
	}

	toolDefs := registry.Definitions()
	var totalUsage llm.Usage

	for round := 0; round < maxRounds; round++ {
		resp, err := client.Complete(ctx, llm.Request{
			Model:    model,
			Messages: conversation,
			Tools:    toolDefs,
		})
		if err != nil {
			return fmt.Errorf("agent: LLM call failed on round %d: %w", round, err)
		}

		totalUsage = totalUsage.Add(resp.Usage)

		// Print any assistant text.
		if text := resp.Text(); text != "" {
			fmt.Printf("[assistant] %s\n", text)
		}

		toolCalls := resp.ToolCalls()
		if len(toolCalls) == 0 {
			fmt.Printf("[done] Total usage: in=%d out=%d total=%d\n",
				totalUsage.InputTokens, totalUsage.OutputTokens, totalUsage.TotalTokens)
			return nil
		}

		// Append the assistant's message (with tool calls) to the conversation.
		conversation = append(conversation, resp.Message)

		// Execute each tool call and append results.
		for _, tc := range toolCalls {
			result := executeTool(registry, tc, workDir)
			fmt.Printf("[tool] %s -> %s\n", tc.Name, summarize(result.Content, 120))
			conversation = append(conversation, llm.ToolResultMessage(
				result.ToolCallID,
				result.Content,
				result.IsError,
			))
		}
	}

	fmt.Printf("[warning] Round limit (%d) reached. Stopping.\n", maxRounds)
	return nil
}

// executeTool looks up a tool call in the registry and executes it. Unknown
// tools or execution errors are returned as error results so the model can
// adjust its approach.
func executeTool(registry *tools.Registry, tc llm.ToolCall, workDir string) llm.ToolResultData {
	registered, ok := registry.Get(tc.Name)
	if !ok {
		return llm.ToolResultData{
			ToolCallID: tc.ID,
			Content:    fmt.Sprintf("Unknown tool: %s", tc.Name),
			IsError:    true,
		}
	}

	output, err := registered.Execute(json.RawMessage(tc.Arguments), workDir)
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
func RunTaskCapture(ctx context.Context, client Completer, model, prompt, workDir string) (string, llm.Usage, int, error) {
	registry := tools.DefaultRegistry("attractor-sandbox")
	systemPrompt := BuildSystemPrompt(workDir)

	conversation := []llm.Message{
		llm.SystemMessage(systemPrompt),
		llm.UserMessage(prompt),
	}

	toolDefs := registry.Definitions()
	var lastText string
	var totalUsage llm.Usage

	for round := 0; round < maxRounds; round++ {
		resp, err := client.Complete(ctx, llm.Request{
			Model:    model,
			Messages: conversation,
			Tools:    toolDefs,
		})
		if err != nil {
			return "", totalUsage, round, fmt.Errorf("agent: LLM call failed on round %d: %w", round, err)
		}

		totalUsage = totalUsage.Add(resp.Usage)

		if text := resp.Text(); text != "" {
			lastText = text
		}

		toolCalls := resp.ToolCalls()
		if len(toolCalls) == 0 {
			return lastText, totalUsage, round + 1, nil
		}

		conversation = append(conversation, resp.Message)

		for _, tc := range toolCalls {
			result := executeTool(registry, tc, workDir)
			conversation = append(conversation, llm.ToolResultMessage(
				result.ToolCallID,
				result.Content,
				result.IsError,
			))
		}
	}

	if lastText != "" {
		return lastText, totalUsage, maxRounds, nil
	}
	return "", totalUsage, maxRounds, fmt.Errorf("agent: round limit (%d) reached with no final response", maxRounds)
}

// summarize returns the first n characters of s, appending "..." if truncated.
func summarize(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
