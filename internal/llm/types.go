// Package llm provides a minimal unified LLM client for the Attractor coding agent.
// Layer 1: data model and core types, following the Attractor unified LLM spec.
package llm

import "encoding/json"

// Role identifies the author of a message in a conversation.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// ContentKind is the discriminator tag for a ContentPart.
type ContentKind string

const (
	KindText       ContentKind = "text"
	KindToolCall   ContentKind = "tool_call"
	KindToolResult ContentKind = "tool_result"
)

// ContentPart is a single element of a message body. A message contains one or
// more parts, enabling multimodal content and structured assistant responses.
type ContentPart struct {
	Kind ContentKind

	// Populated when Kind == KindText.
	Text string

	// Populated when Kind == KindToolCall.
	ToolCall *ToolCall

	// Populated when Kind == KindToolResult.
	ToolResult *ToolResultData
}

// ToolCall represents a single tool invocation requested by the model.
type ToolCall struct {
	ID        string          // provider-assigned unique identifier
	Name      string          // tool name
	Arguments json.RawMessage // parsed JSON arguments
}

// ToolResultData is the payload carried by a KindToolResult ContentPart.
type ToolResultData struct {
	ToolCallID string // links this result back to its ToolCall.ID
	Content    string // the tool's output (text)
	IsError    bool   // true if tool execution failed
}

// Message is the fundamental unit of a conversation.
type Message struct {
	Role       Role
	Parts      []ContentPart
	ToolCallID string // non-empty only for RoleTool messages
}

// Text returns the concatenation of all text parts in the message.
func (m Message) Text() string {
	var out string
	for _, p := range m.Parts {
		if p.Kind == KindText {
			out += p.Text
		}
	}
	return out
}

// ToolCalls returns all tool call parts from an assistant message.
func (m Message) ToolCalls() []ToolCall {
	var calls []ToolCall
	for _, p := range m.Parts {
		if p.Kind == KindToolCall && p.ToolCall != nil {
			calls = append(calls, *p.ToolCall)
		}
	}
	return calls
}

// SystemMessage creates a system-role message with a single text part.
func SystemMessage(text string) Message {
	return Message{
		Role:  RoleSystem,
		Parts: []ContentPart{{Kind: KindText, Text: text}},
	}
}

// UserMessage creates a user-role message with a single text part.
func UserMessage(text string) Message {
	return Message{
		Role:  RoleUser,
		Parts: []ContentPart{{Kind: KindText, Text: text}},
	}
}

// AssistantMessage creates an assistant-role message with a single text part.
func AssistantMessage(text string) Message {
	return Message{
		Role:  RoleAssistant,
		Parts: []ContentPart{{Kind: KindText, Text: text}},
	}
}

// ToolResultMessage creates a tool-role message carrying a tool execution result.
func ToolResultMessage(toolCallID, content string, isError bool) Message {
	return Message{
		Role:       RoleTool,
		ToolCallID: toolCallID,
		Parts: []ContentPart{{
			Kind: KindToolResult,
			ToolResult: &ToolResultData{
				ToolCallID: toolCallID,
				Content:    content,
				IsError:    isError,
			},
		}},
	}
}

// ToolDefinition describes a tool the model may invoke.
type ToolDefinition struct {
	Name        string          // unique identifier; must match [a-zA-Z][a-zA-Z0-9_]*, max 64 chars
	Description string          // human-readable description for the model
	Parameters  json.RawMessage // JSON Schema object defining the input (root must be "object")
}

// ToolChoice controls whether and how the model uses tools.
type ToolChoice struct {
	Mode     string // "auto", "none", "required", "named"
	ToolName string // required when Mode == "named"
}

// Request is the single input type for Client.Complete().
type Request struct {
	Model              string
	Messages           []Message
	Tools              []ToolDefinition
	ToolChoice         *ToolChoice
	MaxTokens          int
	ReasoningMaxTokens int // cap on thinking tokens (subset of MaxTokens); 0 = provider default
	Temperature        *float64
}

// FinishReason records why the model stopped generating.
type FinishReason struct {
	Reason string // unified: "stop", "length", "tool_calls", "content_filter", "error", "other"
	Raw    string // provider's native finish reason string
}

// Usage records token counts for a single request.
type Usage struct {
	InputTokens  int
	OutputTokens int
	TotalTokens  int

	// Prompt caching statistics (Anthropic via OpenRouter).
	// CacheReadTokens is the number of input tokens served from cache (discounted).
	// CacheCreationTokens is the number of input tokens written to a new cache entry.
	CacheReadTokens     int
	CacheCreationTokens int
}

// Add returns the element-wise sum of two Usage values.
func (u Usage) Add(other Usage) Usage {
	return Usage{
		InputTokens:        u.InputTokens + other.InputTokens,
		OutputTokens:       u.OutputTokens + other.OutputTokens,
		TotalTokens:        u.TotalTokens + other.TotalTokens,
		CacheReadTokens:    u.CacheReadTokens + other.CacheReadTokens,
		CacheCreationTokens: u.CacheCreationTokens + other.CacheCreationTokens,
	}
}

// Response is the output of Client.Complete().
type Response struct {
	ID           string
	Model        string
	Provider     string
	Message      Message
	FinishReason FinishReason
	Usage        Usage
}

// Text returns the concatenated text content of the response message.
func (r Response) Text() string {
	return r.Message.Text()
}

// ToolCalls returns all tool calls from the response message.
func (r Response) ToolCalls() []ToolCall {
	return r.Message.ToolCalls()
}
