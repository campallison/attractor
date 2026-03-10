package agent

import (
	"encoding/json"
	"strings"
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

// makeToolResultErrorMsg builds a tool result message marked as an error.
func makeToolResultErrorMsg(callID, content string) llm.Message {
	return llm.ToolResultMessage(callID, content, true)
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
		name            string
		conversation    []llm.Message
		keepFullRounds  int
		wantSameLength  bool
		wantUnchanged   bool // entire output should be identical to input
		checkCompressed map[string]string // callID -> expected compressed content
		checkFull       []string          // callIDs that should retain original content
	}{
		{
			// No old rounds, but write_file result in round 1 still gets
			// write-and-forget compression.
			name:           "fewer rounds than threshold compresses write_file only",
			conversation:   fullConversation[:6], // sys + user + 2 rounds (read + write)
			keepFullRounds: 4,
			wantSameLength: true,
			checkCompressed: map[string]string{
				"call_1": "[previously wrote: main.go]",
			},
			checkFull: []string{"call_0"},
		},
		{
			// 4 rounds <= keepFullRounds(4), but write and edit results
			// are still compressed via write-and-forget.
			name:           "at threshold compresses write and edit via write-and-forget",
			conversation:   fullConversation[:10], // sys + user + 4 rounds
			keepFullRounds: 4,
			wantSameLength: true,
			checkCompressed: map[string]string{
				"call_1": "[previously wrote: main.go]",
				"call_3": "[previously edited: main.go]",
			},
			checkFull: []string{"call_0", "call_2"},
		},
		{
			// Round 0 is in the old zone; rounds 1-4 in keep window.
			// Round 0 read_file gets skeleton summary (short content).
			// Round 1 write_file gets write-and-forget.
			// Round 3 edit_file gets write-and-forget.
			name:           "one round over threshold compresses oldest plus write-forget",
			conversation:   fullConversation[:12], // sys + user + 5 rounds
			keepFullRounds: 4,
			wantSameLength: true,
			checkCompressed: map[string]string{
				"call_0": "[previously read: go.mod]\nmodule example.com/app\n\ngo 1.22",
				"call_1": "[previously wrote: main.go]",
				"call_3": "[previously edited: main.go]",
			},
			checkFull: []string{"call_2", "call_4"},
		},
		{
			// Rounds 0-3 in old zone, rounds 4-5 in keep window.
			// write/edit are compressed regardless of zone.
			// shell "Build succeeded" is short (< 300 chars) so preserved.
			// read_file go.mod content is short, gets inline summary.
			name:           "keep 2 compresses first 4 rounds smartly",
			conversation:   fullConversation,
			keepFullRounds: 2,
			wantSameLength: true,
			checkCompressed: map[string]string{
				"call_0": "[previously read: go.mod]\nmodule example.com/app\n\ngo 1.22",
				"call_1": "[previously wrote: main.go]",
				"call_3": "[previously edited: main.go]",
			},
			// call_2 (shell, short output) stays full even in old zone;
			// call_4 and call_5 are in keep window.
			checkFull: []string{"call_2", "call_4", "call_5"},
		},
		{
			name:           "keep 0 compresses everything",
			conversation:   fullConversation,
			keepFullRounds: 0,
			wantSameLength: true,
			checkCompressed: map[string]string{
				"call_0": "[previously read: go.mod]\nmodule example.com/app\n\ngo 1.22",
				"call_1": "[previously wrote: main.go]",
				"call_3": "[previously edited: main.go]",
				"call_4": "[previously searched: grep TODO]",
				"call_5": "[previously searched: glob *.go]",
			},
			// call_2 shell output "Build succeeded" is short, preserved.
			checkFull: []string{"call_2"},
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
				if len(tc.conversation) > 0 && &result[0] != &tc.conversation[0] {
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
						if len(content) < 2 {
							t.Errorf("expected %s to have substantial content, got %q", fullID, content)
						}
					}
				}
			}
		})
	}
}

func TestCompressHistory_WriteForgetInKeepWindow(t *testing.T) {
	sysMsg := llm.SystemMessage("system")
	userMsg := llm.UserMessage("prompt")

	// 2 rounds, both in keep window (keepFullRounds=4).
	// Round 0: write_file (should be compressed via write-and-forget).
	// Round 1: read_file (should be kept full).
	conversation := []llm.Message{
		sysMsg, userMsg,
		makeToolCallMsg(llm.ToolCall{
			ID: "w0", Name: "write_file",
			Arguments: argsJSON(map[string]any{"path": "handler.go", "content": "package handlers\n\nfunc Handle() {}"}),
		}),
		makeToolResultMsg("w0", "File written: handler.go (3 lines)"),
		makeToolCallMsg(llm.ToolCall{
			ID: "r0", Name: "read_file",
			Arguments: argsJSON(map[string]any{"path": "go.mod"}),
		}),
		makeToolResultMsg("r0", "module example.com"),
	}

	result := compressHistory(conversation, 4)

	// write_file result should be compressed even though it's in keep window.
	w0Content := result[3].Parts[0].ToolResult.Content
	if diff := cmp.Diff("[previously wrote: handler.go]", w0Content); diff != "" {
		t.Errorf("write_file not compressed via write-and-forget (-want +got):\n%s", diff)
	}

	// read_file result should remain full.
	r0Content := result[5].Parts[0].ToolResult.Content
	if diff := cmp.Diff("module example.com", r0Content); diff != "" {
		t.Errorf("read_file should be full (-want +got):\n%s", diff)
	}
}

func TestCompressHistory_WriteForgetPreservesErrors(t *testing.T) {
	sysMsg := llm.SystemMessage("system")
	userMsg := llm.UserMessage("prompt")

	conversation := []llm.Message{
		sysMsg, userMsg,
		makeToolCallMsg(llm.ToolCall{
			ID: "w0", Name: "write_file",
			Arguments: argsJSON(map[string]any{"path": "bad.go", "content": "x"}),
		}),
		makeToolResultErrorMsg("w0", "Tool error (write_file): permission denied"),
		makeToolCallMsg(llm.ToolCall{
			ID: "r0", Name: "read_file",
			Arguments: argsJSON(map[string]any{"path": "go.mod"}),
		}),
		makeToolResultMsg("r0", "module example.com"),
	}

	result := compressHistory(conversation, 4)

	// Error write_file results should NOT be compressed — the model needs
	// to see the error to adjust its approach.
	w0 := result[3].Parts[0].ToolResult
	if diff := cmp.Diff("Tool error (write_file): permission denied", w0.Content); diff != "" {
		t.Errorf("error write_file should be preserved (-want +got):\n%s", diff)
	}
	if !w0.IsError {
		t.Error("expected IsError=true for failed write_file")
	}
}

func TestCompressHistory_ShortShellPreservedInOldZone(t *testing.T) {
	sysMsg := llm.SystemMessage("system")
	userMsg := llm.UserMessage("prompt")

	conversation := []llm.Message{
		sysMsg, userMsg,
		// Round 0: short shell output
		makeToolCallMsg(llm.ToolCall{
			ID: "s0", Name: "shell",
			Arguments: argsJSON(map[string]any{"command": "go build ./..."}),
		}),
		makeToolResultMsg("s0", ""),
		// Round 1: keep window
		makeToolCallMsg(llm.ToolCall{
			ID: "r1", Name: "read_file",
			Arguments: argsJSON(map[string]any{"path": "main.go"}),
		}),
		makeToolResultMsg("r1", "package main"),
	}

	result := compressHistory(conversation, 1) // Round 0 in old zone

	// Short shell result should be preserved verbatim even in old zone.
	s0Content := result[3].Parts[0].ToolResult.Content
	if diff := cmp.Diff("", s0Content); diff != "" {
		t.Errorf("short shell should be preserved (-want +got):\n%s", diff)
	}
}

func TestCompressHistory_LongShellCompressedInOldZone(t *testing.T) {
	sysMsg := llm.SystemMessage("system")
	userMsg := llm.UserMessage("prompt")

	longOutput := strings.Repeat("line of output\n", 30) // > 300 chars
	conversation := []llm.Message{
		sysMsg, userMsg,
		// Round 0: long shell output
		makeToolCallMsg(llm.ToolCall{
			ID: "s0", Name: "shell",
			Arguments: argsJSON(map[string]any{"command": "go test -v ./..."}),
		}),
		makeToolResultMsg("s0", longOutput),
		// Round 1: keep window
		makeToolCallMsg(llm.ToolCall{
			ID: "r1", Name: "read_file",
			Arguments: argsJSON(map[string]any{"path": "main.go"}),
		}),
		makeToolResultMsg("r1", "package main"),
	}

	result := compressHistory(conversation, 1) // Round 0 in old zone

	// Long shell result should be compressed to the summary.
	s0Content := result[3].Parts[0].ToolResult.Content
	if diff := cmp.Diff("[previously ran: go test -v ./...]", s0Content); diff != "" {
		t.Errorf("long shell should be compressed (-want +got):\n%s", diff)
	}
}

func TestCompressHistory_ReadFileSkeletonForLargeFiles(t *testing.T) {
	sysMsg := llm.SystemMessage("system")
	userMsg := llm.UserMessage("prompt")

	// Build a file content that exceeds readFileSkeletonThreshold (500 chars).
	var lines []string
	lines = append(lines, "package models", "", "import \"time\"")
	for i := 0; i < 30; i++ {
		lines = append(lines, "// filler line for testing compression")
	}
	lines = append(lines, "type User struct {", "\tID int", "}")
	largeContent := strings.Join(lines, "\n")

	conversation := []llm.Message{
		sysMsg, userMsg,
		makeToolCallMsg(llm.ToolCall{
			ID: "r0", Name: "read_file",
			Arguments: argsJSON(map[string]any{"path": "internal/models/user.go"}),
		}),
		makeToolResultMsg("r0", largeContent),
		// Round 1: keep window
		makeToolCallMsg(llm.ToolCall{
			ID: "s1", Name: "shell",
			Arguments: argsJSON(map[string]any{"command": "go build"}),
		}),
		makeToolResultMsg("s1", "ok"),
	}

	result := compressHistory(conversation, 1) // Round 0 in old zone

	r0Content := result[3].Parts[0].ToolResult.Content

	// Should start with the skeleton header.
	if !strings.HasPrefix(r0Content, "[previously read: internal/models/user.go") {
		t.Errorf("expected skeleton header, got: %s", r0Content[:80])
	}

	// Should contain the head lines (package, blank, import).
	if !strings.Contains(r0Content, "package models") {
		t.Error("skeleton should contain head line: package models")
	}
	if !strings.Contains(r0Content, `import "time"`) {
		t.Error("skeleton should contain head line: import")
	}

	// Should contain the tail lines.
	if !strings.Contains(r0Content, "type User struct {") {
		t.Error("skeleton should contain tail line: type User struct")
	}
	if !strings.Contains(r0Content, "\tID int") {
		t.Error("skeleton should contain tail line: ID int")
	}

	// Should contain the omitted marker.
	if !strings.Contains(r0Content, "lines omitted") {
		t.Error("skeleton should contain '... N lines omitted ...'")
	}

	// Should NOT contain the filler lines.
	if strings.Count(r0Content, "filler line") > 0 {
		t.Error("skeleton should not contain filler lines from middle of file")
	}
}

func TestCompressHistory_ReadFileSmallContentPreservedInOldZone(t *testing.T) {
	sysMsg := llm.SystemMessage("system")
	userMsg := llm.UserMessage("prompt")

	shortContent := "package main\n\nfunc main() {}"
	conversation := []llm.Message{
		sysMsg, userMsg,
		makeToolCallMsg(llm.ToolCall{
			ID: "r0", Name: "read_file",
			Arguments: argsJSON(map[string]any{"path": "main.go"}),
		}),
		makeToolResultMsg("r0", shortContent),
		makeToolCallMsg(llm.ToolCall{
			ID: "s1", Name: "shell",
			Arguments: argsJSON(map[string]any{"command": "go build"}),
		}),
		makeToolResultMsg("s1", "ok"),
	}

	result := compressHistory(conversation, 1)

	// Short read_file content should be included inline with the header.
	r0Content := result[3].Parts[0].ToolResult.Content
	want := "[previously read: main.go]\npackage main\n\nfunc main() {}"
	if diff := cmp.Diff(want, r0Content); diff != "" {
		t.Errorf("short read_file in old zone (-want +got):\n%s", diff)
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

func TestSummarizeReadFile(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		content string
		checks  func(t *testing.T, got string)
	}{
		{
			name:    "short content preserved inline",
			path:    "main.go",
			content: "package main\n\nfunc main() {}",
			checks: func(t *testing.T, got string) {
				want := "[previously read: main.go]\npackage main\n\nfunc main() {}"
				if diff := cmp.Diff(want, got); diff != "" {
					t.Errorf("(-want +got):\n%s", diff)
				}
			},
		},
		{
			name:    "large content gets skeleton",
			path:    "big.go",
			content: buildLargeFile(40),
			checks: func(t *testing.T, got string) {
				if !strings.HasPrefix(got, "[previously read: big.go (") {
					t.Errorf("expected skeleton header, got prefix: %q", got[:50])
				}
				if !strings.Contains(got, "package big") {
					t.Error("missing head line: package big")
				}
				if !strings.Contains(got, "lines omitted") {
					t.Error("missing omitted marker")
				}
				if !strings.Contains(got, "func Last()") {
					t.Error("missing tail line")
				}
			},
		},
		{
			name:    "missing path falls back gracefully",
			path:    "",
			content: "some content",
			checks: func(t *testing.T, got string) {
				if !strings.Contains(got, "unknown file") {
					t.Errorf("expected 'unknown file' in summary, got: %s", got)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			args := argsJSON(map[string]any{"path": tc.path})
			if tc.path == "" {
				args = argsJSON(map[string]any{})
			}
			got := summarizeReadFile(args, tc.content)
			tc.checks(t, got)
		})
	}
}

// buildLargeFile creates a synthetic Go file with N filler lines.
func buildLargeFile(fillerLines int) string {
	var lines []string
	lines = append(lines, "package big", "", "import \"fmt\"")
	for i := 0; i < fillerLines; i++ {
		lines = append(lines, "// filler line for testing")
	}
	lines = append(lines, "func Last() {", "\tfmt.Println(\"end\")", "}")
	return strings.Join(lines, "\n")
}

func TestCompressHistory_MultipleToolCallsPerRound(t *testing.T) {
	sysMsg := llm.SystemMessage("system")
	userMsg := llm.UserMessage("prompt")

	// Round 0: assistant makes 2 read_file calls at once.
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

	// Round 1: single tool call (stays full with keepFullRounds=1).
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

	// Both round-0 read_file results should be compressed with inline content (short files).
	r0a := result[3]
	wantA := "[previously read: a.go]\npackage a\n// lots of code here"
	if diff := cmp.Diff(wantA, r0a.Parts[0].ToolResult.Content); diff != "" {
		t.Errorf("c0a not compressed correctly (-want +got):\n%s", diff)
	}
	r0b := result[4]
	wantB := "[previously read: b.go]\npackage b\n// lots of code here"
	if diff := cmp.Diff(wantB, r0b.Parts[0].ToolResult.Content); diff != "" {
		t.Errorf("c0b not compressed correctly (-want +got):\n%s", diff)
	}

	// Round 1 should remain full.
	r1 := result[6]
	if diff := cmp.Diff("Build ok", r1.Parts[0].ToolResult.Content); diff != "" {
		t.Errorf("c1 should be full (-want +got):\n%s", diff)
	}
}

func TestCompressHistory_NoRoundsWithWriteForgetOnly(t *testing.T) {
	sysMsg := llm.SystemMessage("system")
	userMsg := llm.UserMessage("prompt")

	// Single round with only a write_file call.
	// Even with keepFullRounds=10, write-and-forget should trigger.
	conversation := []llm.Message{
		sysMsg, userMsg,
		makeToolCallMsg(llm.ToolCall{
			ID: "w0", Name: "write_file",
			Arguments: argsJSON(map[string]any{"path": "out.go", "content": "x"}),
		}),
		makeToolResultMsg("w0", "File written: out.go (1 line)"),
	}

	result := compressHistory(conversation, 10)

	w0Content := result[3].Parts[0].ToolResult.Content
	if diff := cmp.Diff("[previously wrote: out.go]", w0Content); diff != "" {
		t.Errorf("single write_file should be compressed (-want +got):\n%s", diff)
	}
}
