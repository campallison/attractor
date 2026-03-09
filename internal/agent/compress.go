package agent

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/campallison/attractor/internal/llm"
)

const defaultKeepFullRounds = 4

// compressHistory returns a copy of the conversation with old tool result
// contents replaced by brief summaries. "Old" means any round before the
// last keepFullRounds rounds. A round is defined as an assistant message
// containing tool calls followed by its corresponding tool result messages.
//
// The system message, user prompt, and all assistant messages are preserved
// unchanged. Only tool result content from old rounds is replaced.
//
// The original conversation slice is never modified.
func compressHistory(conversation []llm.Message, keepFullRounds int) []llm.Message {
	// Identify round boundaries: each assistant message with tool calls
	// marks the start of a round.
	var roundStarts []int
	for i, msg := range conversation {
		if msg.Role == llm.RoleAssistant && len(msg.ToolCalls()) > 0 {
			roundStarts = append(roundStarts, i)
		}
	}

	totalRounds := len(roundStarts)
	if totalRounds <= keepFullRounds {
		return conversation
	}

	slog.Debug("agent.compress", "total_rounds", totalRounds, "keep_full", keepFullRounds, "compressing", totalRounds-keepFullRounds, "messages_before", len(conversation))

	// Build a map of tool call IDs that should be compressed, along with
	// a human-readable summary derived from the tool name and arguments.
	compressUpTo := totalRounds - keepFullRounds
	summaries := make(map[string]string)

	for i := 0; i < compressUpTo; i++ {
		for _, tc := range conversation[roundStarts[i]].ToolCalls() {
			summaries[tc.ID] = summarizeToolCall(tc.Name, tc.Arguments)
		}
	}

	// Build a new slice, replacing only the tool result messages that
	// belong to old rounds.
	result := make([]llm.Message, len(conversation))
	for i, msg := range conversation {
		if msg.Role == llm.RoleTool && msg.ToolCallID != "" {
			if summary, ok := summaries[msg.ToolCallID]; ok {
				isError := false
				if len(msg.Parts) > 0 && msg.Parts[0].ToolResult != nil {
					isError = msg.Parts[0].ToolResult.IsError
				}
				result[i] = llm.ToolResultMessage(msg.ToolCallID, summary, isError)
				continue
			}
		}
		result[i] = msg
	}

	return result
}

// summarizeToolCall produces a short description of a tool invocation from
// its name and raw JSON arguments. The summary is used to replace verbose
// tool results (which can be tens of thousands of characters) in compressed
// conversation history.
func summarizeToolCall(name string, args json.RawMessage) string {
	var parsed map[string]any
	if err := json.Unmarshal(args, &parsed); err != nil {
		return fmt.Sprintf("[previously called: %s]", name)
	}

	switch name {
	case "read_file":
		if path, ok := parsed["path"].(string); ok {
			return fmt.Sprintf("[previously read: %s]", path)
		}
	case "write_file":
		if path, ok := parsed["path"].(string); ok {
			return fmt.Sprintf("[previously wrote: %s]", path)
		}
	case "edit_file":
		if path, ok := parsed["path"].(string); ok {
			return fmt.Sprintf("[previously edited: %s]", path)
		}
	case "shell":
		if cmd, ok := parsed["command"].(string); ok {
			if len(cmd) > 80 {
				cmd = cmd[:80] + "..."
			}
			return fmt.Sprintf("[previously ran: %s]", cmd)
		}
	case "grep":
		if pattern, ok := parsed["pattern"].(string); ok {
			return fmt.Sprintf("[previously searched: grep %s]", pattern)
		}
	case "glob":
		if pattern, ok := parsed["pattern"].(string); ok {
			return fmt.Sprintf("[previously searched: glob %s]", pattern)
		}
	}

	return fmt.Sprintf("[previously called: %s]", name)
}
