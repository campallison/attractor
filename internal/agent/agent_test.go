package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/campallison/attractor/internal/llm"
	"github.com/campallison/attractor/internal/tools"
	"github.com/google/go-cmp/cmp"
)

func testRegistry() *tools.Registry {
	return tools.DefaultRegistry("test-sandbox")
}

// mockCompleter is a test double for Completer that returns pre-configured
// responses in sequence.
type mockCompleter struct {
	responses []llm.Response
	calls     []llm.Request
	callIdx   int
}

func (m *mockCompleter) Complete(_ context.Context, req llm.Request) (llm.Response, error) {
	m.calls = append(m.calls, req)
	if m.callIdx >= len(m.responses) {
		return llm.Response{
			Message:      llm.AssistantMessage("(no more mock responses)"),
			FinishReason: llm.FinishReason{Reason: "stop"},
		}, nil
	}
	resp := m.responses[m.callIdx]
	m.callIdx++
	return resp, nil
}

// textResponse creates a Response with only text content (no tool calls).
func textResponse(text string) llm.Response {
	return llm.Response{
		Message:      llm.AssistantMessage(text),
		FinishReason: llm.FinishReason{Reason: "stop"},
		Usage:        llm.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
	}
}

// toolCallResponse creates a Response containing a single tool call.
func toolCallResponse(callID, toolName string, args map[string]interface{}) llm.Response {
	argsJSON, _ := json.Marshal(args)
	return llm.Response{
		Message: llm.Message{
			Role: llm.RoleAssistant,
			Parts: []llm.ContentPart{{
				Kind: llm.KindToolCall,
				ToolCall: &llm.ToolCall{
					ID:        callID,
					Name:      toolName,
					Arguments: argsJSON,
				},
			}},
		},
		FinishReason: llm.FinishReason{Reason: "tool_calls"},
		Usage:        llm.Usage{InputTokens: 20, OutputTokens: 10, TotalTokens: 30},
	}
}

func TestRunTaskTextOnly(t *testing.T) {
	mock := &mockCompleter{
		responses: []llm.Response{
			textResponse("Task complete."),
		},
	}

	err := RunTask(context.Background(), mock, "test-model", "say hello", t.TempDir(), testRegistry())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if diff := cmp.Diff(1, len(mock.calls)); diff != "" {
		t.Errorf("call count mismatch (-want +got):\n%s", diff)
	}
}

func TestRunTaskToolCallThenText(t *testing.T) {
	dir := t.TempDir()

	mock := &mockCompleter{
		responses: []llm.Response{
			toolCallResponse("call_1", "write_file", map[string]interface{}{
				"file_path": "hello.txt",
				"content":   "Hello!",
			}),
			textResponse("Done, I created hello.txt."),
		},
	}

	err := RunTask(context.Background(), mock, "test-model", "create hello.txt", dir, testRegistry())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if diff := cmp.Diff(2, len(mock.calls)); diff != "" {
		t.Errorf("call count mismatch (-want +got):\n%s", diff)
	}

	// The second call should include the tool result in the messages.
	lastReq := mock.calls[1]
	foundToolResult := false
	for _, msg := range lastReq.Messages {
		if msg.Role == llm.RoleTool {
			foundToolResult = true
			break
		}
	}
	if !foundToolResult {
		t.Error("expected second LLM call to include a tool result message")
	}
}

func TestRunTaskCapture_ReturnsUsageAndRounds(t *testing.T) {
	mock := &mockCompleter{
		responses: []llm.Response{
			toolCallResponse("call_1", "write_file", map[string]interface{}{
				"file_path": "test.txt",
				"content":   "hello",
			}),
			textResponse("File created."),
		},
	}

	text, usage, rounds, conversation, err := RunTaskCapture(context.Background(), mock, "test-model", "create test.txt", t.TempDir(), 0, testRegistry())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if diff := cmp.Diff("File created.", text); diff != "" {
		t.Errorf("response text mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(2, rounds); diff != "" {
		t.Errorf("rounds mismatch (-want +got):\n%s", diff)
	}

	// Usage should be the sum of both rounds:
	// round 1: input=20, output=10, total=30 (tool call response)
	// round 2: input=10, output=5, total=15 (text response)
	if diff := cmp.Diff(30, usage.InputTokens); diff != "" {
		t.Errorf("input tokens mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(15, usage.OutputTokens); diff != "" {
		t.Errorf("output tokens mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(45, usage.TotalTokens); diff != "" {
		t.Errorf("total tokens mismatch (-want +got):\n%s", diff)
	}
	if len(conversation) == 0 {
		t.Error("expected non-empty conversation history")
	}
}

func TestRunTaskCapture_TextOnly(t *testing.T) {
	mock := &mockCompleter{
		responses: []llm.Response{
			textResponse("Direct answer."),
		},
	}

	text, usage, rounds, _, err := RunTaskCapture(context.Background(), mock, "test-model", "answer me", t.TempDir(), 0, testRegistry())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if diff := cmp.Diff("Direct answer.", text); diff != "" {
		t.Errorf("response text mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(1, rounds); diff != "" {
		t.Errorf("rounds mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(10, usage.InputTokens); diff != "" {
		t.Errorf("input tokens mismatch (-want +got):\n%s", diff)
	}
}

func TestRunTaskUnknownTool(t *testing.T) {
	mock := &mockCompleter{
		responses: []llm.Response{
			toolCallResponse("call_1", "nonexistent_tool", map[string]interface{}{}),
			textResponse("Sorry, that tool doesn't exist."),
		},
	}

	err := RunTask(context.Background(), mock, "test-model", "do something", t.TempDir(), testRegistry())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have made 2 calls: first got unknown tool error result, second got text.
	if diff := cmp.Diff(2, len(mock.calls)); diff != "" {
		t.Errorf("call count mismatch (-want +got):\n%s", diff)
	}

	// Verify the tool result in the second call is an error.
	lastReq := mock.calls[1]
	for _, msg := range lastReq.Messages {
		if msg.Role == llm.RoleTool {
			for _, p := range msg.Parts {
				if p.Kind == llm.KindToolResult && p.ToolResult != nil {
					if !p.ToolResult.IsError {
						t.Error("expected tool result to be marked as error for unknown tool")
					}
					return
				}
			}
		}
	}
	t.Error("did not find a tool result message in the second call")
}

// infiniteToolCallCompleter always returns a tool call, never text. Used to
// test round-limit exhaustion.
type infiniteToolCallCompleter struct {
	callCount int
}

func (m *infiniteToolCallCompleter) Complete(_ context.Context, _ llm.Request) (llm.Response, error) {
	m.callCount++
	args, _ := json.Marshal(map[string]string{"file_path": "nonexistent.txt"})
	return llm.Response{
		Message: llm.Message{
			Role: llm.RoleAssistant,
			Parts: []llm.ContentPart{
				{
					Kind: llm.KindText,
					Text: "Let me read this file...",
				},
				{
					Kind: llm.KindToolCall,
					ToolCall: &llm.ToolCall{
						ID:        fmt.Sprintf("call_%d", m.callCount),
						Name:      "read_file",
						Arguments: args,
					},
				},
			},
		},
		FinishReason: llm.FinishReason{Reason: "tool_calls"},
		Usage:        llm.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
	}, nil
}

// mixedToolCallCompleter alternates between read_file and write_file so the
// read-loop detector never fires, allowing the round limit to be reached.
type mixedToolCallCompleter struct {
	callCount int
}

func (m *mixedToolCallCompleter) Complete(_ context.Context, _ llm.Request) (llm.Response, error) {
	m.callCount++
	toolName := "read_file"
	argsKey := "file_path"
	if m.callCount%5 == 0 {
		toolName = "write_file"
		argsKey = "file_path"
	}
	args, _ := json.Marshal(map[string]string{argsKey: "file.txt"})
	return llm.Response{
		Message: llm.Message{
			Role: llm.RoleAssistant,
			Parts: []llm.ContentPart{
				{Kind: llm.KindText, Text: "working..."},
				{
					Kind: llm.KindToolCall,
					ToolCall: &llm.ToolCall{
						ID:        fmt.Sprintf("call_%d", m.callCount),
						Name:      toolName,
						Arguments: args,
					},
				},
			},
		},
		FinishReason: llm.FinishReason{Reason: "tool_calls"},
		Usage:        llm.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
	}, nil
}

func TestRunTask_RoundLimitReturnsError(t *testing.T) {
	mock := &mixedToolCallCompleter{}

	err := RunTask(context.Background(), mock, "test-model", "do infinite work", t.TempDir(), testRegistry())

	if !errors.Is(err, ErrRoundLimitReached) {
		t.Fatalf("expected ErrRoundLimitReached, got: %v", err)
	}
	if diff := cmp.Diff(maxRounds, mock.callCount); diff != "" {
		t.Errorf("expected exactly maxRounds LLM calls (-want +got):\n%s", diff)
	}
}

func TestRunTaskCapture_RoundLimitReached(t *testing.T) {
	mock := &mixedToolCallCompleter{}

	text, usage, rounds, conversation, err := RunTaskCapture(
		context.Background(), mock, "test-model", "do infinite work", t.TempDir(), 0, testRegistry(),
	)

	if !errors.Is(err, ErrRoundLimitReached) {
		t.Fatalf("expected ErrRoundLimitReached, got: %v", err)
	}
	if diff := cmp.Diff(maxRounds, rounds); diff != "" {
		t.Errorf("rounds mismatch (-want +got):\n%s", diff)
	}
	if text == "" {
		t.Error("expected lastText to be non-empty (assistant text was present in tool-call responses)")
	}
	if usage.TotalTokens == 0 {
		t.Error("expected non-zero token usage")
	}
	if len(conversation) == 0 {
		t.Error("expected non-empty conversation")
	}
	if diff := cmp.Diff(maxRounds, mock.callCount); diff != "" {
		t.Errorf("expected exactly maxRounds LLM calls (-want +got):\n%s", diff)
	}
}

func TestIsReadOnlyRound(t *testing.T) {
	tests := []struct {
		name  string
		tools []string
		want  bool
	}{
		{"empty", nil, false},
		{"single read_file", []string{"read_file"}, true},
		{"single write_file", []string{"write_file"}, false},
		{"single edit_file", []string{"edit_file"}, false},
		{"grep only", []string{"grep"}, true},
		{"glob only", []string{"glob"}, true},
		{"shell only", []string{"shell"}, true},
		{"mixed reads", []string{"read_file", "grep", "shell"}, true},
		{"read then write", []string{"read_file", "write_file"}, false},
		{"read then edit", []string{"read_file", "edit_file"}, false},
		{"write among many reads", []string{"read_file", "grep", "glob", "shell", "write_file"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var calls []llm.ToolCall
			for i, name := range tc.tools {
				calls = append(calls, llm.ToolCall{
					ID:   fmt.Sprintf("call_%d", i),
					Name: name,
				})
			}
			got := isReadOnlyRound(calls)
			if got != tc.want {
				t.Errorf("isReadOnlyRound(%v) = %v, want %v", tc.tools, got, tc.want)
			}
		})
	}
}

func TestRunTaskCapture_ReadLoopDetection(t *testing.T) {
	mock := &infiniteToolCallCompleter{}

	_, _, rounds, _, err := RunTaskCapture(
		context.Background(), mock, "test-model", "analyze code", t.TempDir(), 20, testRegistry(),
	)

	// With C4 in place, a persistent read-loop now terminates early with
	// ErrReadLoopDetected instead of hitting the round limit.
	if !errors.Is(err, ErrReadLoopDetected) {
		t.Fatalf("expected ErrReadLoopDetected, got: %v", err)
	}
	// With maxNudges=2: nudge at round 5, second nudge at round 10,
	// termination at round 15.
	if rounds != 15 {
		t.Errorf("expected termination at round 15 (nudges at 5 and 10, terminate at 15), got %d", rounds)
	}
}

// capturingReadCompleter always returns read_file calls and captures all
// requests so tests can inspect the conversation for injected nudge messages.
type capturingReadCompleter struct {
	requests []llm.Request
}

func (m *capturingReadCompleter) Complete(_ context.Context, req llm.Request) (llm.Response, error) {
	m.requests = append(m.requests, req)
	callID := fmt.Sprintf("call_%d", len(m.requests))
	args, _ := json.Marshal(map[string]string{"file_path": "main.go"})
	return llm.Response{
		Message: llm.Message{
			Role: llm.RoleAssistant,
			Parts: []llm.ContentPart{{
				Kind: llm.KindToolCall,
				ToolCall: &llm.ToolCall{
					ID:        callID,
					Name:      "read_file",
					Arguments: args,
				},
			}},
		},
		FinishReason: llm.FinishReason{Reason: "tool_calls"},
		Usage:        llm.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
	}, nil
}

func TestRunTaskCapture_NudgeInjection(t *testing.T) {
	mock := &capturingReadCompleter{}

	// Run with enough rounds to hit nudge at round 5. Use 8 rounds so
	// the agent doesn't also hit the second detection event (which would
	// trigger early termination via C4).
	_, _, _, conversation, err := RunTaskCapture(
		context.Background(), mock, "test-model", "analyze code", t.TempDir(), 8, testRegistry(),
	)

	if !errors.Is(err, ErrRoundLimitReached) {
		t.Fatalf("expected ErrRoundLimitReached, got: %v", err)
	}

	nudgeCount := 0
	for _, msg := range conversation {
		if msg.Role == llm.RoleUser {
			text := msg.Text()
			if strings.Contains(text, "[PIPELINE ENGINE]") {
				nudgeCount++
				if !strings.Contains(text, "_scratch/") {
					t.Error("nudge should reference _scratch/")
				}
			}
		}
	}
	if nudgeCount != 1 {
		t.Errorf("expected exactly 1 nudge message in conversation, got %d", nudgeCount)
	}

	// Verify the nudge was present in the request sent to the LLM on the
	// round after it was injected. The nudge fires after round 5 (index 4),
	// so round 6 (index 5) should see it.
	if len(mock.requests) < 6 {
		t.Fatalf("expected at least 6 requests, got %d", len(mock.requests))
	}
	round6Req := mock.requests[5]
	foundNudge := false
	for _, msg := range round6Req.Messages {
		if msg.Role == llm.RoleUser && strings.Contains(msg.Text(), "[PIPELINE ENGINE]") {
			foundNudge = true
			break
		}
	}
	if !foundNudge {
		t.Error("expected nudge message in round 6 request to LLM")
	}
}

func TestRunTaskCapture_NudgeResetsCounter(t *testing.T) {
	// With 8 rounds: nudge at round 5, then only 3 more read rounds.
	// The counter resets after the nudge, so it should NOT fire again
	// (would need 5 more to re-trigger, but we only run 3 more).
	mock := &capturingReadCompleter{}

	_, _, _, conversation, err := RunTaskCapture(
		context.Background(), mock, "test-model", "analyze code", t.TempDir(), 8, testRegistry(),
	)

	if !errors.Is(err, ErrRoundLimitReached) {
		t.Fatalf("expected ErrRoundLimitReached, got: %v", err)
	}

	nudgeCount := 0
	for _, msg := range conversation {
		if msg.Role == llm.RoleUser && strings.Contains(msg.Text(), "[PIPELINE ENGINE]") {
			nudgeCount++
		}
	}
	if nudgeCount != 1 {
		t.Errorf("expected exactly 1 nudge (counter should reset), got %d", nudgeCount)
	}
}

func TestRunTaskCapture_ReadLoopTermination(t *testing.T) {
	// With 20 rounds available and maxNudges=2: nudge at round 5, counter
	// resets, second nudge at round 10, counter resets, third detection at
	// round 15 triggers early termination (maxNudges exhausted).
	mock := &capturingReadCompleter{}

	_, _, rounds, conversation, err := RunTaskCapture(
		context.Background(), mock, "test-model", "analyze code", t.TempDir(), 20, testRegistry(),
	)

	if !errors.Is(err, ErrReadLoopDetected) {
		t.Fatalf("expected ErrReadLoopDetected, got: %v", err)
	}
	// Nudge at round 5, second nudge at round 10, termination at round 15.
	if rounds != 15 {
		t.Errorf("expected termination at round 15, got round %d", rounds)
	}
	if len(mock.requests) != 15 {
		t.Errorf("expected 15 LLM calls, got %d", len(mock.requests))
	}

	// Conversation should contain exactly 2 nudges before termination.
	nudgeCount := 0
	for _, msg := range conversation {
		if msg.Role == llm.RoleUser && strings.Contains(msg.Text(), "[PIPELINE ENGINE]") {
			nudgeCount++
		}
	}
	if nudgeCount != 2 {
		t.Errorf("expected exactly 2 nudges before termination, got %d", nudgeCount)
	}
}

// --- runLoop tests ---

func TestRunLoop_TextOnlyResponse(t *testing.T) {
	mock := &mockCompleter{responses: []llm.Response{textResponse("hello")}}
	conversation := []llm.Message{llm.SystemMessage("sys"), llm.UserMessage("prompt")}
	toolDefs := testRegistry().Definitions()

	result, err := runLoop(context.Background(), mock, "test-model", conversation, toolDefs, testRegistry(), t.TempDir(), loopConfig{MaxRounds: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Text != "hello" {
		t.Errorf("text = %q, want %q", result.Text, "hello")
	}
	if result.Rounds != 1 {
		t.Errorf("rounds = %d, want 1", result.Rounds)
	}
}

func TestRunLoop_ToolCallThenText(t *testing.T) {
	mock := &mockCompleter{responses: []llm.Response{
		toolCallResponse("c1", "read_file", map[string]interface{}{"path": "x.go"}),
		textResponse("done"),
	}}
	conversation := []llm.Message{llm.SystemMessage("sys"), llm.UserMessage("prompt")}
	toolDefs := testRegistry().Definitions()

	result, err := runLoop(context.Background(), mock, "test-model", conversation, toolDefs, testRegistry(), t.TempDir(), loopConfig{MaxRounds: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Rounds != 2 {
		t.Errorf("rounds = %d, want 2", result.Rounds)
	}
	if result.Usage.TotalTokens != 45 {
		t.Errorf("total tokens = %d, want 45 (30+15)", result.Usage.TotalTokens)
	}
}

func TestRunLoop_RoundLimit(t *testing.T) {
	mock := &mixedToolCallCompleter{}
	conversation := []llm.Message{llm.SystemMessage("sys"), llm.UserMessage("prompt")}
	toolDefs := testRegistry().Definitions()

	_, err := runLoop(context.Background(), mock, "test-model", conversation, toolDefs, testRegistry(), t.TempDir(), loopConfig{MaxRounds: 5})
	if !errors.Is(err, ErrRoundLimitReached) {
		t.Fatalf("expected ErrRoundLimitReached, got: %v", err)
	}
}

func TestRunLoop_ReadLoopDetection(t *testing.T) {
	mock := &infiniteToolCallCompleter{}
	conversation := []llm.Message{llm.SystemMessage("sys"), llm.UserMessage("prompt")}
	toolDefs := testRegistry().Definitions()

	result, err := runLoop(context.Background(), mock, "test-model", conversation, toolDefs, testRegistry(), t.TempDir(), loopConfig{
		MaxRounds:      20,
		DetectReadLoop: true,
	})
	if !errors.Is(err, ErrReadLoopDetected) {
		t.Fatalf("expected ErrReadLoopDetected, got: %v", err)
	}
	if result.Rounds != 15 {
		t.Errorf("rounds = %d, want 15", result.Rounds)
	}
}

func TestRunLoop_ReadLoopDisabled(t *testing.T) {
	mock := &infiniteToolCallCompleter{}
	conversation := []llm.Message{llm.SystemMessage("sys"), llm.UserMessage("prompt")}
	toolDefs := testRegistry().Definitions()

	_, err := runLoop(context.Background(), mock, "test-model", conversation, toolDefs, testRegistry(), t.TempDir(), loopConfig{
		MaxRounds:      7,
		DetectReadLoop: false,
	})
	if !errors.Is(err, ErrRoundLimitReached) {
		t.Fatalf("expected ErrRoundLimitReached (not read-loop), got: %v", err)
	}
}

func TestRunLoop_Callbacks(t *testing.T) {
	mock := &mockCompleter{responses: []llm.Response{
		toolCallResponse("c1", "read_file", map[string]interface{}{"path": "x.go"}),
		textResponse("all done"),
	}}
	conversation := []llm.Message{llm.SystemMessage("sys"), llm.UserMessage("prompt")}
	toolDefs := testRegistry().Definitions()

	var textCalls []string
	var toolCalls []string

	_, err := runLoop(context.Background(), mock, "test-model", conversation, toolDefs, testRegistry(), t.TempDir(), loopConfig{
		MaxRounds:    10,
		OnText:       func(text string) { textCalls = append(textCalls, text) },
		OnToolResult: func(name, summary string) { toolCalls = append(toolCalls, name) },
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(textCalls) != 1 || textCalls[0] != "all done" {
		t.Errorf("textCalls = %v, want [all done]", textCalls)
	}
	if len(toolCalls) != 1 || toolCalls[0] != "read_file" {
		t.Errorf("toolCalls = %v, want [read_file]", toolCalls)
	}
}

func TestRunTaskCapture_ReadLoopResetsOnWrite(t *testing.T) {
	// 4 read rounds, then 1 write, then 4 more reads, then text.
	// Should NOT trigger read-loop detection (never hits 5 consecutive).
	responses := []llm.Response{}
	for i := 0; i < 4; i++ {
		responses = append(responses, toolCallResponse(
			fmt.Sprintf("r%d", i), "read_file", map[string]interface{}{"path": "file.go"},
		))
	}
	responses = append(responses, toolCallResponse(
		"w1", "write_file", map[string]interface{}{"file_path": "out.go", "content": "pkg"},
	))
	for i := 0; i < 4; i++ {
		responses = append(responses, toolCallResponse(
			fmt.Sprintf("r2_%d", i), "read_file", map[string]interface{}{"path": "other.go"},
		))
	}
	responses = append(responses, textResponse("Done."))

	mock := &mockCompleter{responses: responses}
	text, _, rounds, _, err := RunTaskCapture(
		context.Background(), mock, "test-model", "task", t.TempDir(), 0, testRegistry(),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "Done." {
		t.Errorf("expected 'Done.', got %q", text)
	}
	if rounds != 10 {
		t.Errorf("expected 10 rounds (4 read + 1 write + 4 read + 1 text), got %d", rounds)
	}
}

func TestRunTaskCapture_CustomMaxRounds(t *testing.T) {
	customLimit := 3
	mock := &infiniteToolCallCompleter{}

	_, _, rounds, _, err := RunTaskCapture(
		context.Background(), mock, "test-model", "do work", t.TempDir(), customLimit, testRegistry(),
	)

	if !errors.Is(err, ErrRoundLimitReached) {
		t.Fatalf("expected ErrRoundLimitReached, got: %v", err)
	}
	if diff := cmp.Diff(customLimit, rounds); diff != "" {
		t.Errorf("rounds should equal custom limit (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(customLimit, mock.callCount); diff != "" {
		t.Errorf("LLM calls should equal custom limit (-want +got):\n%s", diff)
	}
}
