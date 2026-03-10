package agent

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/campallison/attractor/internal/llm"
)

const (
	defaultKeepFullRounds = 4

	// Tool results shorter than this (in bytes) are kept verbatim even in
	// the compressed zone for shell results that are nearly free token-wise.
	shortResultThreshold = 300

	// read_file results longer than this get skeleton-summarised (first/last
	// lines preserved, middle omitted) instead of a path-only summary.
	readFileSkeletonThreshold = 500

	// Number of lines to keep at the head and tail of a skeletonised file.
	skeletonHeadLines = 3
	skeletonTailLines = 3
)

// writeForgetTools are tool names whose successful results carry no
// information worth keeping — the model never re-reads its own writes.
var writeForgetTools = map[string]bool{
	"write_file": true,
	"edit_file":  true,
}

// compressHistory returns a copy of the conversation with old tool result
// contents replaced by brief summaries. "Old" means any round before the
// last keepFullRounds rounds. A round is defined as an assistant message
// containing tool calls followed by its corresponding tool result messages.
//
// Additionally, write_file and edit_file results are always compressed
// (even within the keep window) because their success output is never
// re-referenced by the model.
//
// Short shell results (< shortResultThreshold bytes) in the old zone are
// preserved verbatim because they are nearly free token-wise.
//
// read_file results in the old zone get skeleton summaries (first/last
// lines preserved) instead of a path-only placeholder.
//
// The system message, user prompt, and all assistant messages are preserved
// unchanged. The original conversation slice is never modified.
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

	// Index tool-call metadata by ID across ALL rounds so that the
	// write-and-forget pass can look up the tool name for any result.
	toolMeta := make(map[string]llm.ToolCall)
	for _, start := range roundStarts {
		for _, tc := range conversation[start].ToolCalls() {
			toolMeta[tc.ID] = tc
		}
	}

	// Determine which tool-call IDs fall in the "old" zone.
	compressUpTo := 0
	if totalRounds > keepFullRounds {
		compressUpTo = totalRounds - keepFullRounds
	}
	oldCallIDs := make(map[string]bool)
	for i := 0; i < compressUpTo; i++ {
		for _, tc := range conversation[roundStarts[i]].ToolCalls() {
			oldCallIDs[tc.ID] = true
		}
	}

	// If there is nothing to compress and no write-forget candidates, bail.
	hasWriteForget := false
	for _, msg := range conversation {
		if msg.Role == llm.RoleTool && msg.ToolCallID != "" {
			if tc, ok := toolMeta[msg.ToolCallID]; ok && writeForgetTools[tc.Name] {
				hasWriteForget = true
				break
			}
		}
	}
	if compressUpTo == 0 && !hasWriteForget {
		return conversation
	}

	slog.Debug("agent.compress",
		"total_rounds", totalRounds,
		"keep_full", keepFullRounds,
		"old_rounds", compressUpTo,
		"has_write_forget", hasWriteForget,
		"messages", len(conversation),
	)

	result := make([]llm.Message, len(conversation))
	for i, msg := range conversation {
		if msg.Role != llm.RoleTool || msg.ToolCallID == "" {
			result[i] = msg
			continue
		}

		tc, known := toolMeta[msg.ToolCallID]
		if !known {
			result[i] = msg
			continue
		}

		content := ""
		isError := false
		if len(msg.Parts) > 0 && msg.Parts[0].ToolResult != nil {
			content = msg.Parts[0].ToolResult.Content
			isError = msg.Parts[0].ToolResult.IsError
		}

		// Write-and-forget: compress write/edit results everywhere,
		// even within the keep window, unless the result was an error.
		if writeForgetTools[tc.Name] && !isError {
			result[i] = llm.ToolResultMessage(
				msg.ToolCallID,
				summarizeToolCall(tc.Name, tc.Arguments),
				false,
			)
			continue
		}

		// Outside the old zone, keep everything else verbatim.
		if !oldCallIDs[msg.ToolCallID] {
			result[i] = msg
			continue
		}

		// Old zone: apply smart compression based on tool type.
		summary := smartSummarize(tc.Name, tc.Arguments, content)
		result[i] = llm.ToolResultMessage(msg.ToolCallID, summary, isError)
	}

	return result
}

// smartSummarize produces a context-aware summary of a tool result. It uses
// richer strategies than the basic summarizeToolCall: skeleton summaries for
// large file reads, passthrough for short shell output, etc.
func smartSummarize(name string, args json.RawMessage, content string) string {
	switch name {
	case "read_file":
		return summarizeReadFile(args, content)
	case "shell":
		if len(content) < shortResultThreshold {
			return content
		}
		return summarizeToolCall(name, args)
	default:
		return summarizeToolCall(name, args)
	}
}

// summarizeReadFile produces a skeleton summary for large file reads,
// preserving the first and last few lines so the model retains structural
// awareness (package, imports, final signatures).
func summarizeReadFile(args json.RawMessage, content string) string {
	var parsed map[string]any
	if err := json.Unmarshal(args, &parsed); err != nil {
		return "[previously read: unknown file]"
	}
	path, _ := parsed["path"].(string)
	if path == "" {
		path = "unknown file"
	}

	if len(content) <= readFileSkeletonThreshold {
		return fmt.Sprintf("[previously read: %s]\n%s", path, content)
	}

	lines := strings.Split(content, "\n")
	totalLines := len(lines)

	if totalLines <= skeletonHeadLines+skeletonTailLines {
		return fmt.Sprintf("[previously read: %s]\n%s", path, content)
	}

	head := strings.Join(lines[:skeletonHeadLines], "\n")
	tail := strings.Join(lines[totalLines-skeletonTailLines:], "\n")
	omitted := totalLines - skeletonHeadLines - skeletonTailLines
	return fmt.Sprintf("[previously read: %s (%d lines, showing first %d + last %d)]\n%s\n... %d lines omitted ...\n%s",
		path, totalLines, skeletonHeadLines, skeletonTailLines, head, omitted, tail)
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
