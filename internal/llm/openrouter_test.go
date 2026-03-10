package llm

import (
	"encoding/json"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestTranslateToolChoice(t *testing.T) {
	tests := []struct {
		name   string
		choice *ToolChoice
		want   interface{}
	}{
		{name: "nil defaults to auto", choice: nil, want: "auto"},
		{name: "auto", choice: &ToolChoice{Mode: "auto"}, want: "auto"},
		{name: "none", choice: &ToolChoice{Mode: "none"}, want: "none"},
		{name: "required", choice: &ToolChoice{Mode: "required"}, want: "required"},
		{
			name:   "named",
			choice: &ToolChoice{Mode: "named", ToolName: "get_weather"},
			want: map[string]interface{}{
				"type": "function",
				"function": map[string]string{
					"name": "get_weather",
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := translateToolChoice(tt.choice)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("translateToolChoice() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestMapFinishReason(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{"stop", "stop"},
		{"length", "length"},
		{"tool_calls", "tool_calls"},
		{"content_filter", "content_filter"},
		{"", "stop"},
		{"some_unknown_reason", "other"},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			got := mapFinishReason(tt.raw)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("mapFinishReason(%q) mismatch (-want +got):\n%s", tt.raw, diff)
			}
		})
	}
}

func TestTranslateMessage(t *testing.T) {
	tests := []struct {
		name    string
		msg     Message
		want    []orMessage
		wantErr bool
	}{
		{
			name: "system message",
			msg:  SystemMessage("be helpful"),
			want: []orMessage{{Role: "system", Content: "be helpful"}},
		},
		{
			name: "user message",
			msg:  UserMessage("hello"),
			want: []orMessage{{Role: "user", Content: "hello"}},
		},
		{
			name: "assistant text only",
			msg:  AssistantMessage("the answer is 4"),
			want: []orMessage{{Role: "assistant", Content: "the answer is 4"}},
		},
		{
			name: "assistant with tool calls",
			msg: Message{
				Role: RoleAssistant,
				Parts: []ContentPart{
					{Kind: KindToolCall, ToolCall: &ToolCall{
						ID:        "call_abc",
						Name:      "get_weather",
						Arguments: json.RawMessage(`{"location":"Tokyo"}`),
					}},
				},
			},
			want: []orMessage{{
				Role: "assistant",
				ToolCalls: []orToolCall{{
					ID:   "call_abc",
					Type: "function",
					Function: struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					}{
						Name:      "get_weather",
						Arguments: `{"location":"Tokyo"}`,
					},
				}},
			}},
		},
		{
			name: "tool result message",
			msg:  ToolResultMessage("call_abc", "72F and sunny", false),
			want: []orMessage{{
				Role:       "tool",
				Content:    "72F and sunny",
				ToolCallID: "call_abc",
			}},
		},
		{
			name:    "unsupported role",
			msg:     Message{Role: "unknown"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := translateMessage(tt.msg, false)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("translateMessage() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestTranslateTools(t *testing.T) {
	params := json.RawMessage(`{"type":"object","properties":{"loc":{"type":"string"}}}`)
	tools := []ToolDefinition{{
		Name:        "get_weather",
		Description: "Get the weather",
		Parameters:  params,
	}}

	got := translateTools(tools)
	want := []orTool{{
		Type: "function",
		Function: orFunction{
			Name:        "get_weather",
			Description: "Get the weather",
			Parameters:  params,
		},
	}}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("translateTools() mismatch (-want +got):\n%s", diff)
	}
}

func TestTranslateMessages_CachingAnthropicModel(t *testing.T) {
	msgs := []Message{
		SystemMessage("You are a coding agent."),
		UserMessage("Build me a web app."),
		AssistantMessage("I'll start by reading the codebase."),
	}

	got, err := translateMessages(msgs, true, "anthropic/claude-opus-4.6")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(got) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(got))
	}

	// System and user messages should be content arrays with cache_control.
	for i, label := range []string{"system", "user"} {
		parts, ok := got[i].Content.([]orContentPart)
		if !ok {
			t.Fatalf("%s message: expected []orContentPart, got %T", label, got[i].Content)
		}
		if len(parts) != 1 {
			t.Fatalf("%s message: expected 1 content part, got %d", label, len(parts))
		}
		if parts[0].CacheControl == nil {
			t.Errorf("%s message: expected cache_control to be set", label)
		} else if parts[0].CacheControl.Type != "ephemeral" {
			t.Errorf("%s message: expected cache_control.type=\"ephemeral\", got %q", label, parts[0].CacheControl.Type)
		}
	}

	// Assistant message should remain a plain string.
	if _, ok := got[2].Content.(string); !ok {
		t.Errorf("assistant message: expected string content, got %T", got[2].Content)
	}
}

func TestTranslateMessages_CachingNonAnthropicModel(t *testing.T) {
	msgs := []Message{
		SystemMessage("You are a coding agent."),
		UserMessage("Build me a web app."),
	}

	got, err := translateMessages(msgs, true, "google/gemini-2.5-flash")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Even with promptCaching=true, non-Anthropic models should use plain strings.
	for i, label := range []string{"system", "user"} {
		if _, ok := got[i].Content.(string); !ok {
			t.Errorf("%s message: expected plain string content for non-Anthropic model, got %T", label, got[i].Content)
		}
	}
}

func TestTranslateMessages_CachingDisabled(t *testing.T) {
	msgs := []Message{
		SystemMessage("You are a coding agent."),
		UserMessage("Build me a web app."),
	}

	got, err := translateMessages(msgs, false, "anthropic/claude-opus-4.6")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// With caching disabled, even Anthropic models should use plain strings.
	for i, label := range []string{"system", "user"} {
		if _, ok := got[i].Content.(string); !ok {
			t.Errorf("%s message: expected plain string content when caching disabled, got %T", label, got[i].Content)
		}
	}
}

func TestParseORResponse(t *testing.T) {
	tests := []struct {
		name           string
		body           string
		requestedModel string
		want           Response
		wantErr        bool
	}{
		{
			name: "simple text response",
			body: `{
				"id": "gen-123",
				"model": "openai/gpt-4o-mini",
				"choices": [{
					"message": {"role": "assistant", "content": "4"},
					"finish_reason": "stop"
				}],
				"usage": {"prompt_tokens": 10, "completion_tokens": 1, "total_tokens": 11}
			}`,
			requestedModel: "openai/gpt-4o-mini",
			want: Response{
				ID:       "gen-123",
				Model:    "openai/gpt-4o-mini",
				Provider: "openrouter",
				Message: Message{
					Role:  RoleAssistant,
					Parts: []ContentPart{{Kind: KindText, Text: "4"}},
				},
				FinishReason: FinishReason{Reason: "stop", Raw: "stop"},
				Usage:        Usage{InputTokens: 10, OutputTokens: 1, TotalTokens: 11},
			},
		},
		{
			name: "response with tool calls",
			body: `{
				"id": "gen-456",
				"model": "openai/gpt-4o-mini",
				"choices": [{
					"message": {
						"role": "assistant",
						"content": null,
						"tool_calls": [{
							"id": "call_xyz",
							"type": "function",
							"function": {
								"name": "get_weather",
								"arguments": "{\"location\":\"Tokyo\"}"
							}
						}]
					},
					"finish_reason": "tool_calls"
				}],
				"usage": {"prompt_tokens": 50, "completion_tokens": 10, "total_tokens": 60}
			}`,
			requestedModel: "openai/gpt-4o-mini",
			want: Response{
				ID:       "gen-456",
				Model:    "openai/gpt-4o-mini",
				Provider: "openrouter",
				Message: Message{
					Role: RoleAssistant,
					Parts: []ContentPart{{
						Kind: KindToolCall,
						ToolCall: &ToolCall{
							ID:        "call_xyz",
							Name:      "get_weather",
							Arguments: json.RawMessage(`{"location":"Tokyo"}`),
						},
					}},
				},
				FinishReason: FinishReason{Reason: "tool_calls", Raw: "tool_calls"},
				Usage:        Usage{InputTokens: 50, OutputTokens: 10, TotalTokens: 60},
			},
		},
		{
			name: "fallback to requested model when response model empty",
			body: `{
				"id": "gen-789",
				"model": "",
				"choices": [{
					"message": {"role": "assistant", "content": "ok"},
					"finish_reason": "stop"
				}],
				"usage": {"prompt_tokens": 5, "completion_tokens": 1, "total_tokens": 6}
			}`,
			requestedModel: "my-model",
			want: Response{
				ID:       "gen-789",
				Model:    "my-model",
				Provider: "openrouter",
				Message: Message{
					Role:  RoleAssistant,
					Parts: []ContentPart{{Kind: KindText, Text: "ok"}},
				},
				FinishReason: FinishReason{Reason: "stop", Raw: "stop"},
				Usage:        Usage{InputTokens: 5, OutputTokens: 1, TotalTokens: 6},
			},
		},
		{
			name: "response with prompt cache stats",
			body: `{
				"id": "gen-cache",
				"model": "anthropic/claude-opus-4.6",
				"choices": [{
					"message": {"role": "assistant", "content": "cached response"},
					"finish_reason": "stop"
				}],
				"usage": {
					"prompt_tokens": 5000,
					"completion_tokens": 100,
					"total_tokens": 5100,
					"prompt_tokens_details": {
						"cached_tokens": 4800,
						"cache_write_tokens": 200
					}
				}
			}`,
			requestedModel: "anthropic/claude-opus-4.6",
			want: Response{
				ID:       "gen-cache",
				Model:    "anthropic/claude-opus-4.6",
				Provider: "openrouter",
				Message: Message{
					Role:  RoleAssistant,
					Parts: []ContentPart{{Kind: KindText, Text: "cached response"}},
				},
				FinishReason: FinishReason{Reason: "stop", Raw: "stop"},
				Usage: Usage{
					InputTokens:        5000,
					OutputTokens:       100,
					TotalTokens:        5100,
					CacheReadTokens:    4800,
					CacheCreationTokens: 200,
				},
			},
		},
		{
			name:           "empty choices array",
			body:           `{"id":"gen-000","model":"m","choices":[],"usage":{}}`,
			requestedModel: "m",
			wantErr:        true,
		},
		{
			name:           "invalid JSON",
			body:           `not json at all`,
			requestedModel: "m",
			wantErr:        true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseORResponse([]byte(tt.body), tt.requestedModel)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("parseORResponse() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
