package llm

import (
	"encoding/json"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestMessageConstructors(t *testing.T) {
	tests := []struct {
		name string
		msg  Message
		want Message
	}{
		{
			name: "SystemMessage",
			msg:  SystemMessage("be helpful"),
			want: Message{
				Role:  RoleSystem,
				Parts: []ContentPart{{Kind: KindText, Text: "be helpful"}},
			},
		},
		{
			name: "UserMessage",
			msg:  UserMessage("hello"),
			want: Message{
				Role:  RoleUser,
				Parts: []ContentPart{{Kind: KindText, Text: "hello"}},
			},
		},
		{
			name: "AssistantMessage",
			msg:  AssistantMessage("hi there"),
			want: Message{
				Role:  RoleAssistant,
				Parts: []ContentPart{{Kind: KindText, Text: "hi there"}},
			},
		},
		{
			name: "ToolResultMessage success",
			msg:  ToolResultMessage("call_1", "72F and sunny", false),
			want: Message{
				Role:       RoleTool,
				ToolCallID: "call_1",
				Parts: []ContentPart{{
					Kind: KindToolResult,
					ToolResult: &ToolResultData{
						ToolCallID: "call_1",
						Content:    "72F and sunny",
						IsError:    false,
					},
				}},
			},
		},
		{
			name: "ToolResultMessage error",
			msg:  ToolResultMessage("call_2", "file not found", true),
			want: Message{
				Role:       RoleTool,
				ToolCallID: "call_2",
				Parts: []ContentPart{{
					Kind: KindToolResult,
					ToolResult: &ToolResultData{
						ToolCallID: "call_2",
						Content:    "file not found",
						IsError:    true,
					},
				}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if diff := cmp.Diff(tt.want, tt.msg); diff != "" {
				t.Errorf("constructor mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestMessageText(t *testing.T) {
	tests := []struct {
		name string
		msg  Message
		want string
	}{
		{
			name: "empty message",
			msg:  Message{Role: RoleAssistant},
			want: "",
		},
		{
			name: "single text part",
			msg:  UserMessage("hello"),
			want: "hello",
		},
		{
			name: "multiple text parts concatenated",
			msg: Message{
				Role: RoleAssistant,
				Parts: []ContentPart{
					{Kind: KindText, Text: "hello "},
					{Kind: KindText, Text: "world"},
				},
			},
			want: "hello world",
		},
		{
			name: "only tool call parts returns empty",
			msg: Message{
				Role: RoleAssistant,
				Parts: []ContentPart{
					{Kind: KindToolCall, ToolCall: &ToolCall{ID: "1", Name: "foo"}},
				},
			},
			want: "",
		},
		{
			name: "mixed text and tool call parts",
			msg: Message{
				Role: RoleAssistant,
				Parts: []ContentPart{
					{Kind: KindText, Text: "thinking..."},
					{Kind: KindToolCall, ToolCall: &ToolCall{ID: "1", Name: "foo"}},
				},
			},
			want: "thinking...",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.msg.Text()
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("Text() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestMessageToolCalls(t *testing.T) {
	tc1 := ToolCall{ID: "call_1", Name: "read_file", Arguments: json.RawMessage(`{"path":"a.txt"}`)}
	tc2 := ToolCall{ID: "call_2", Name: "write_file", Arguments: json.RawMessage(`{"path":"b.txt"}`)}

	tests := []struct {
		name string
		msg  Message
		want []ToolCall
	}{
		{
			name: "no tool calls",
			msg:  AssistantMessage("just text"),
			want: nil,
		},
		{
			name: "one tool call",
			msg: Message{
				Role: RoleAssistant,
				Parts: []ContentPart{
					{Kind: KindToolCall, ToolCall: &tc1},
				},
			},
			want: []ToolCall{tc1},
		},
		{
			name: "multiple tool calls",
			msg: Message{
				Role: RoleAssistant,
				Parts: []ContentPart{
					{Kind: KindToolCall, ToolCall: &tc1},
					{Kind: KindToolCall, ToolCall: &tc2},
				},
			},
			want: []ToolCall{tc1, tc2},
		},
		{
			name: "mixed parts extracts only tool calls",
			msg: Message{
				Role: RoleAssistant,
				Parts: []ContentPart{
					{Kind: KindText, Text: "let me check"},
					{Kind: KindToolCall, ToolCall: &tc1},
					{Kind: KindText, Text: "and also"},
					{Kind: KindToolCall, ToolCall: &tc2},
				},
			},
			want: []ToolCall{tc1, tc2},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.msg.ToolCalls()
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("ToolCalls() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestUsageAdd(t *testing.T) {
	tests := []struct {
		name string
		a, b Usage
		want Usage
	}{
		{
			name: "basic addition",
			a:    Usage{InputTokens: 10, OutputTokens: 20, TotalTokens: 30},
			b:    Usage{InputTokens: 5, OutputTokens: 15, TotalTokens: 20},
			want: Usage{InputTokens: 15, OutputTokens: 35, TotalTokens: 50},
		},
		{
			name: "zero values",
			a:    Usage{},
			b:    Usage{},
			want: Usage{},
		},
		{
			name: "add to zero",
			a:    Usage{},
			b:    Usage{InputTokens: 100, OutputTokens: 200, TotalTokens: 300},
			want: Usage{InputTokens: 100, OutputTokens: 200, TotalTokens: 300},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.a.Add(tt.b)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("Add() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
