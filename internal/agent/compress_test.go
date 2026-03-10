package agent

import (
	"encoding/json"
	"testing"

	"github.com/campallison/attractor/internal/llm"
	"github.com/google/go-cmp/cmp"
)

// makeToolCallMsg builds an assistant message containing one or more tool calls.
func makeToolCallMsg(calls ...llm.ToolCall) llm.Message {
	parts := make([]llm.ContentPart, len(calls))
	for i, tc := range calls {
		tc := tc
		parts[i] = llm.ContentPart{Kind: llm.KindToolCall, ToolCall: &tc}
	}
	return llm.Message{Role: llm.RoleAssistant, Parts: parts}
}

// makeToolResultMsg builds a tool result message with the given content.
func makeToolResultMsg(callID, content string) llm.Message {
	return llm.ToolResultMessage(callID, content, false)
}

// argsJSON is a helper that marshals a map to json.RawMessage.
func argsJSON(m map[string]any) json.RawMessage {
	b, _ := json.Marshal(m)
	return b
}

func TestCompressHistory(t *testing.T) {
	sysMsg := llm.SystemMessage("You are a helpful assistant.")
	userMsg := llm.UserMessage("Build a web app.")

	// 6 rounds of tool calls to test compression with keepFullRounds=2
	round0Assistant := makeToolCallMsg(llm.ToolCall{
		ID: "call_0", Name: "read_file",
		Arguments: argsJSON(map[string]any{"path": "go.mod"}),
	})
	round0Result := makeToolResultMsg("call_0", "module example.com/app\n\ngo 1.22")

	round1Assistant := makeToolCallMsg(llm.ToolCall{
		ID: "call_1", Name: "write_file",
		Arguments: argsJSON(map[string]any{"path": "main.go", "content": "package main"}),
	})
	round1Result := makeToolResultMsg("call_1", "File written: main.go")

	round2Assistant := makeToolCallMsg(llm.ToolCall{
		ID: "call_2", Name: "shell",
		Arguments: argsJSON(map[string]any{"command": "go build ./..."}),
	})
	round2Result := makeToolResultMsg("call_2", "Build succeeded")

	round3Assistant := makeToolCallMsg(llm.ToolCall{
		ID: "call_3", Name: "edit_file",
		Arguments: argsJSON(map[string]any{"path": "main.go", "old_string": "foo", "new_string": "bar"}),
	})
	round3Result := makeToolResultMsg("call_3", "File edited: main.go")

	round4Assistant := makeToolCallMsg(llm.ToolCall{
		ID: "call_4", Name: "grep",
		Arguments: argsJSON(map[string]any{"pattern": "TODO", "path": "."}),
	})
	round4Result := makeToolResultMsg("call_4", "main.go:10: // TODO: implement\nmain.go:20: // TODO: test")

	round5Assistant := makeToolCallMsg(llm.ToolCall{
		ID: "call_5", Name: "glob",
		Arguments: argsJSON(map[string]any{"pattern": "*.go"}),
	})
	round5Result := makeToolResultMsg("call_5", "main.go\nserver.go\nhandlers.go")

	fullConversation := []llm.Message{
		sysMsg, userMsg,
		round0Assistant, round0Result,
		round1Assistant, round1Result,
		round2Assistant, round2Result,
		round3Assistant, round3Result,
		round4Assistant, round4Result,
		round5Assistant, round5Result,
	}

	tests := []struct {
		name           string
		conversation   []llm.Message
		keepFullRounds int
		wantSameLength bool
		wantUnchanged  bool // entire output should be identical to input
		checkCompressed map[string]string // callID -> expected compressed content
		checkFull       []string          // callIDs that should retain original content
	}{
		{
			name:           "fewer rounds than threshold returns unchanged",
			conversation:   fullConversation[:6], // sys + user + 2 rounds
			keepFullRounds: 4,
			wantSameLength: true,
			wantUnchanged:  true,
		},
		{
			name:           "exactly at threshold returns unchanged",
			conversation:   fullConversation[:10], // sys + user + 4 rounds
			keepFullRounds: 4,
			wantSameLength: true,
			wantUnchanged:  true,
		},
		{
			name:           "one round over threshold compresses oldest",
			conversation:   fullConversation[:12], // sys + user + 5 rounds
			keepFullRounds: 4,
			wantSameLength: true,
			checkCompressed: map[string]string{
				"call_0": "[previously read: go.mod]",
			},
			checkFull: []string{"call_1", "call_2", "call_3", "call_4"},
		},
		{
			name:           "keep 2 compresses first 4 of 6 rounds",
			conversation:   fullConversation,
			keepFullRounds: 2,
			wantSameLength: true,
			checkCompressed: map[string]string{
				"call_0": "[previously read: go.mod]",
				"call_1": "[previously wrote: main.go]",
				"call_2": "[previously ran: go build ./...]",
				"call_3": "[previously edited: main.go]",
			},
			checkFull: []string{"call_4", "call_5"},
		},
		{
			name:           "keep 0 compresses everything",
			conversation:   fullConversation,
			keepFullRounds: 0,
			wantSameLength: true,
			checkCompressed: map[string]string{
				"call_0": "[previously read: go.mod]",
				"call_1": "[previously wrote: main.go]",
				"call_2": "[previously ran: go build ./...]",
				"call_3": "[previously edited: main.go]",
				"call_4": "[previously searched: grep TODO]",
				"call_5": "[previously searched: glob *.go]",
			},
		},
		{
			name:           "empty conversation",
			conversation:   nil,
			keepFullRounds: 4,
			wantSameLength: true,
			wantUnchanged:  true,
		},
		{
			name:           "system and user only",
			conversation:   fullConversation[:2],
			keepFullRounds: 4,
			wantSameLength: true,
			wantUnchanged:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := compressHistory(tc.conversation, tc.keepFullRounds)

			if tc.wantSameLength {
				if diff := cmp.Diff(len(tc.conversation), len(result)); diff != "" {
					t.Fatalf("length mismatch (-want +got):\n%s", diff)
				}
			}

			if tc.wantUnchanged {
				// When unchanged, the function returns the original slice.
				if len(tc.conversation) > 0 && &result[0] != &tc.conversation[0] {
					// It returned a copy; verify contents are identical.
					for i := range tc.conversation {
						if diff := cmp.Diff(tc.conversation[i].Role, result[i].Role); diff != "" {
							t.Errorf("msg[%d] role mismatch: %s", i, diff)
						}
					}
				}
				return
			}

			// Verify compressed tool results have expected summaries.
			for _, msg := range result {
				if msg.Role != llm.RoleTool || msg.ToolCallID == "" {
					continue
				}
				content := msg.Parts[0].ToolResult.Content

				if expected, ok := tc.checkCompressed[msg.ToolCallID]; ok {
					if diff := cmp.Diff(expected, content); diff != "" {
						t.Errorf("compressed content for %s (-want +got):\n%s", msg.ToolCallID, diff)
					}
				}
			}

			// Verify full (uncompressed) tool results still have original content.
			for _, msg := range result {
				if msg.Role != llm.RoleTool || msg.ToolCallID == "" {
					continue
				}
				for _, fullID := range tc.checkFull {
					if msg.ToolCallID == fullID {
						content := msg.Parts[0].ToolResult.Content
						if _, isCompressed := tc.checkCompressed[fullID]; isCompressed {
							t.Errorf("expected %s to be full but it was compressed", fullID)
						}
						if len(content) < 5 {
							t.Errorf("expected %s to have substantial content, got %q", fullID, content)
						}
					}
				}
			}
		})
	}
}

func TestCompressHistory_DoesNotMutateOriginal(t *testing.T) {
	sysMsg := llm.SystemMessage("system")
	userMsg := llm.UserMessage("prompt")

	originalContent := "module example.com/app\n\ngo 1.22\n" +
		"require (\n\tgithub.com/jackc/pgx/v5 v5.7.2\n)"

	conversation := []llm.Message{
		sysMsg, userMsg,
		makeToolCallMsg(llm.ToolCall{
			ID: "c0", Name: "read_file",
			Arguments: argsJSON(map[string]any{"path": "go.mod"}),
		}),
		makeToolResultMsg("c0", originalContent),
		makeToolCallMsg(llm.ToolCall{
			ID: "c1", Name: "read_file",
			Arguments: argsJSON(map[string]any{"path": "main.go"}),
		}),
		makeToolResultMsg("c1", "package main"),
		makeToolCallMsg(llm.ToolCall{
			ID: "c2", Name: "shell",
			Arguments: argsJSON(map[string]any{"command": "go build"}),
		}),
		makeToolResultMsg("c2", "ok"),
	}

	// keepFullRounds=1 means rounds 0 and 1 get compressed
	_ = compressHistory(conversation, 1)

	// Original conversation must be unchanged.
	toolResult0 := conversation[3]
	if diff := cmp.Diff(originalContent, toolResult0.Parts[0].ToolResult.Content); diff != "" {
		t.Errorf("original conversation was mutated (-want +got):\n%s", diff)
	}
}

func TestCompressHistory_SystemAndUserPreserved(t *testing.T) {
	sysMsg := llm.SystemMessage("You are a coding agent.")
	userMsg := llm.UserMessage("Build the web application.")

	conversation := []llm.Message{
		sysMsg, userMsg,
		makeToolCallMsg(llm.ToolCall{
			ID: "c0", Name: "read_file",
			Arguments: argsJSON(map[string]any{"path": "go.mod"}),
		}),
		makeToolResultMsg("c0", "module example.com/app"),
		makeToolCallMsg(llm.ToolCall{
			ID: "c1", Name: "shell",
			Arguments: argsJSON(map[string]any{"command": "ls"}),
		}),
		makeToolResultMsg("c1", "main.go"),
	}

	result := compressHistory(conversation, 0)

	if diff := cmp.Diff(llm.RoleSystem, result[0].Role); diff != "" {
		t.Errorf("system message role: %s", diff)
	}
	if diff := cmp.Diff("You are a coding agent.", result[0].Text()); diff != "" {
		t.Errorf("system message content: %s", diff)
	}
	if diff := cmp.Diff(llm.RoleUser, result[1].Role); diff != "" {
		t.Errorf("user message role: %s", diff)
	}
	if diff := cmp.Diff("Build the web application.", result[1].Text()); diff != "" {
		t.Errorf("user message content: %s", diff)
	}
}

func TestSummarizeToolCall(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		args     map[string]any
		want     string
	}{
		{
			name:     "read_file",
			toolName: "read_file",
			args:     map[string]any{"path": "internal/models/models.go"},
			want:     "[previously read: internal/models/models.go]",
		},
		{
			name:     "write_file",
			toolName: "write_file",
			args:     map[string]any{"path": "main.go", "content": "package main"},
			want:     "[previously wrote: main.go]",
		},
		{
			name:     "edit_file",
			toolName: "edit_file",
			args:     map[string]any{"path": "server.go", "old_string": "a", "new_string": "b"},
			want:     "[previously edited: server.go]",
		},
		{
			name:     "shell short command",
			toolName: "shell",
			args:     map[string]any{"command": "go mod tidy"},
			want:     "[previously ran: go mod tidy]",
		},
		{
			name:     "shell long command truncated",
			toolName: "shell",
			args: map[string]any{
				"command": "very long command that exceeds eighty characters in length and should be truncated to avoid bloating the summary text",
			},
			want: "[previously ran: very long command that exceeds eighty characters in length and should be truncat...]",
		},
		{
			name:     "grep",
			toolName: "grep",
			args:     map[string]any{"pattern": "func main", "path": "."},
			want:     "[previously searched: grep func main]",
		},
		{
			name:     "glob",
			toolName: "glob",
			args:     map[string]any{"pattern": "**/*.go"},
			want:     "[previously searched: glob **/*.go]",
		},
		{
			name:     "unknown tool",
			toolName: "some_new_tool",
			args:     map[string]any{"key": "value"},
			want:     "[previously called: some_new_tool]",
		},
		{
			name:     "read_file missing path",
			toolName: "read_file",
			args:     map[string]any{},
			want:     "[previously called: read_file]",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			args := argsJSON(tc.args)
			got := summarizeToolCall(tc.toolName, args)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("summary mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestSummarizeToolCall_InvalidJSON(t *testing.T) {
	got := summarizeToolCall("read_file", json.RawMessage(`{invalid`))
	want := "[previously called: read_file]"
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("mismatch (-want +got):\n%s", diff)
	}
}

func TestCompressHistory_MultipleToolCallsPerRound(t *testing.T) {
	sysMsg := llm.SystemMessage("system")
	userMsg := llm.UserMessage("prompt")

	// Round 0: assistant makes 2 tool calls at once
	round0Assistant := makeToolCallMsg(
		llm.ToolCall{
			ID: "c0a", Name: "read_file",
			Arguments: argsJSON(map[string]any{"path": "a.go"}),
		},
		llm.ToolCall{
			ID: "c0b", Name: "read_file",
			Arguments: argsJSON(map[string]any{"path": "b.go"}),
		},
	)
	round0ResultA := makeToolResultMsg("c0a", "package a\n// lots of code here")
	round0ResultB := makeToolResultMsg("c0b", "package b\n// lots of code here")

	// Round 1: single tool call (this one stays full with keepFullRounds=1)
	round1Assistant := makeToolCallMsg(llm.ToolCall{
		ID: "c1", Name: "shell",
		Arguments: argsJSON(map[string]any{"command": "go build"}),
	})
	round1Result := makeToolResultMsg("c1", "Build ok")

	conversation := []llm.Message{
		sysMsg, userMsg,
		round0Assistant, round0ResultA, round0ResultB,
		round1Assistant, round1Result,
	}

	result := compressHistory(conversation, 1)

	// Both round-0 tool results should be compressed
	r0a := result[3]
	if diff := cmp.Diff("[previously read: a.go]", r0a.Parts[0].ToolResult.Content); diff != "" {
		t.Errorf("c0a not compressed (-want +got):\n%s", diff)
	}
	r0b := result[4]
	if diff := cmp.Diff("[previously read: b.go]", r0b.Parts[0].ToolResult.Content); diff != "" {
		t.Errorf("c0b not compressed (-want +got):\n%s", diff)
	}

	// Round 1 should remain full
	r1 := result[6]
	if diff := cmp.Diff("Build ok", r1.Parts[0].ToolResult.Content); diff != "" {
		t.Errorf("c1 should be full (-want +got):\n%s", diff)
	}
}
