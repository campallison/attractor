package agent

import (
	"context"
	"encoding/json"
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

	text, usage, rounds, err := RunTaskCapture(context.Background(), mock, "test-model", "create test.txt", t.TempDir())
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
}

func TestRunTaskCapture_TextOnly(t *testing.T) {
	mock := &mockCompleter{
		responses: []llm.Response{
			textResponse("Direct answer."),
		},
	}

	text, usage, rounds, err := RunTaskCapture(context.Background(), mock, "test-model", "answer me", t.TempDir())
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
