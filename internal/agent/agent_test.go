package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/campallison/attractor/internal/llm"
	"github.com/google/go-cmp/cmp"
)

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

	err := RunTask(context.Background(), mock, "test-model", "say hello", t.TempDir())
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

	err := RunTask(context.Background(), mock, "test-model", "create hello.txt", dir)
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

	text, usage, rounds, conversation, err := RunTaskCapture(context.Background(), mock, "test-model", "create test.txt", t.TempDir(), 0)
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

	text, usage, rounds, _, err := RunTaskCapture(context.Background(), mock, "test-model", "answer me", t.TempDir(), 0)
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

	err := RunTask(context.Background(), mock, "test-model", "do something", t.TempDir())
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

func TestRunTaskCapture_RoundLimitReached(t *testing.T) {
	mock := &infiniteToolCallCompleter{}

	text, usage, rounds, conversation, err := RunTaskCapture(
		context.Background(), mock, "test-model", "do infinite work", t.TempDir(), 0,
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

	_, _, _, _, err := RunTaskCapture(
		context.Background(), mock, "test-model", "analyze code", t.TempDir(), 10,
	)

	if !errors.Is(err, ErrRoundLimitReached) {
		t.Fatalf("expected ErrRoundLimitReached, got: %v", err)
	}
	// The infiniteToolCallCompleter only issues read_file calls, so the
	// read-loop detection should fire at round 5 (readLoopThreshold).
	// We can't directly assert on slog output in a unit test, but we
	// verify the agent still reaches round limit — detection is logging
	// only in C1, not terminating.
	if mock.callCount != 10 {
		t.Errorf("expected 10 LLM calls (all rounds should run), got %d", mock.callCount)
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
		context.Background(), mock, "test-model", "task", t.TempDir(), 0,
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
		context.Background(), mock, "test-model", "do work", t.TempDir(), customLimit,
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
