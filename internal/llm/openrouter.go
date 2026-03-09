package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
)

const (
	openRouterBaseURL = "https://openrouter.ai/api/v1"
	openRouterName    = "openrouter"
)

// --- Wire format types (OpenAI Chat Completions shape) ---

// orMessage is the wire format for a single message sent to OpenRouter.
type orMessage struct {
	Role       string      `json:"role"`
	Content    interface{} `json:"content"` // string or []orContentPart
	ToolCallID string      `json:"tool_call_id,omitempty"`
	ToolCalls  []orToolCall `json:"tool_calls,omitempty"`
}

// orContentPart is a single part inside a content array (for assistant messages with tool calls).
type orContentPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// orToolCall is a tool call as returned in an assistant message.
type orToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// orTool is the wire format for a tool definition.
type orTool struct {
	Type     string     `json:"type"`
	Function orFunction `json:"function"`
}

// orFunction holds the function metadata within a tool definition.
type orFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// orRequest is the full request body sent to the OpenRouter chat completions endpoint.
type orRequest struct {
	Model      string      `json:"model"`
	Messages   []orMessage `json:"messages"`
	Tools      []orTool    `json:"tools,omitempty"`
	ToolChoice interface{} `json:"tool_choice,omitempty"` // string or object
	MaxTokens  int         `json:"max_tokens,omitempty"`
	Temperature *float64   `json:"temperature,omitempty"`
}

// orResponse is the response body from the OpenRouter chat completions endpoint.
type orResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Message      orMessage `json:"message"`
		FinishReason string    `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// --- Request translation ---

// buildORRequest converts a unified Request into the OpenRouter wire format.
func buildORRequest(req Request) (orRequest, error) {
	messages, err := translateMessages(req.Messages)
	if err != nil {
		return orRequest{}, err
	}

	orReq := orRequest{
		Model:       req.Model,
		Messages:    messages,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
	}

	if len(req.Tools) > 0 {
		orReq.Tools = translateTools(req.Tools)
		orReq.ToolChoice = translateToolChoice(req.ToolChoice)
	}

	return orReq, nil
}

// translateMessages converts unified Messages to OpenRouter wire messages.
func translateMessages(msgs []Message) ([]orMessage, error) {
	out := make([]orMessage, 0, len(msgs))
	for _, m := range msgs {
		wire, err := translateMessage(m)
		if err != nil {
			return nil, err
		}
		out = append(out, wire...)
	}
	return out, nil
}

// translateMessage converts a single unified Message to one or more wire messages.
// An assistant message with both text and tool calls is represented as a single
// message with the content field set to the text (may be empty) and tool_calls
// carrying the call objects.
func translateMessage(m Message) ([]orMessage, error) {
	switch m.Role {
	case RoleSystem:
		return []orMessage{{Role: "system", Content: m.Text()}}, nil

	case RoleUser:
		return []orMessage{{Role: "user", Content: m.Text()}}, nil

	case RoleAssistant:
		wire := orMessage{Role: "assistant"}
		textContent := m.Text()
		if textContent != "" {
			wire.Content = textContent
		}
		for _, p := range m.Parts {
			if p.Kind == KindToolCall && p.ToolCall != nil {
				tc := orToolCall{
					ID:   p.ToolCall.ID,
					Type: "function",
				}
				tc.Function.Name = p.ToolCall.Name
				tc.Function.Arguments = string(p.ToolCall.Arguments)
				wire.ToolCalls = append(wire.ToolCalls, tc)
			}
		}
		return []orMessage{wire}, nil

	case RoleTool:
		// Each tool result becomes its own "tool" role message.
		var wireMessages []orMessage
		for _, p := range m.Parts {
			if p.Kind == KindToolResult && p.ToolResult != nil {
				wireMessages = append(wireMessages, orMessage{
					Role:       "tool",
					Content:    p.ToolResult.Content,
					ToolCallID: p.ToolResult.ToolCallID,
				})
			}
		}
		if len(wireMessages) == 0 {
			return nil, fmt.Errorf("tool message has no tool result parts")
		}
		return wireMessages, nil

	default:
		return nil, fmt.Errorf("unsupported role: %s", m.Role)
	}
}

// translateTools converts ToolDefinitions to the OpenRouter wire format.
func translateTools(tools []ToolDefinition) []orTool {
	out := make([]orTool, len(tools))
	for i, t := range tools {
		out[i] = orTool{
			Type: "function",
			Function: orFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		}
	}
	return out
}

// translateToolChoice converts a ToolChoice to the OpenRouter wire format.
// When choice is nil, defaults to "auto".
func translateToolChoice(choice *ToolChoice) interface{} {
	if choice == nil {
		return "auto"
	}
	switch choice.Mode {
	case "none":
		return "none"
	case "required":
		return "required"
	case "named":
		return map[string]interface{}{
			"type": "function",
			"function": map[string]string{
				"name": choice.ToolName,
			},
		}
	default:
		return "auto"
	}
}

// --- Response parsing ---

// parseORResponse converts an OpenRouter response body into a unified Response.
func parseORResponse(body []byte, requestedModel string) (Response, error) {
	var raw orResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return Response{}, fmt.Errorf("failed to parse OpenRouter response: %w", err)
	}

	if len(raw.Choices) == 0 {
		return Response{}, fmt.Errorf("OpenRouter response contained no choices")
	}

	choice := raw.Choices[0]
	msg, err := parseORMessage(choice.Message)
	if err != nil {
		return Response{}, fmt.Errorf("failed to parse response message: %w", err)
	}

	model := raw.Model
	if model == "" {
		model = requestedModel
	}

	return Response{
		ID:       raw.ID,
		Model:    model,
		Provider: openRouterName,
		Message:  msg,
		FinishReason: FinishReason{
			Reason: mapFinishReason(choice.FinishReason),
			Raw:    choice.FinishReason,
		},
		Usage: Usage{
			InputTokens:  raw.Usage.PromptTokens,
			OutputTokens: raw.Usage.CompletionTokens,
			TotalTokens:  raw.Usage.TotalTokens,
		},
	}, nil
}

// parseORMessage converts a wire-format assistant message to a unified Message.
func parseORMessage(wire orMessage) (Message, error) {
	msg := Message{Role: RoleAssistant}

	// Extract text content. The content field may be a string or null.
	if wire.Content != nil {
		switch v := wire.Content.(type) {
		case string:
			if v != "" {
				msg.Parts = append(msg.Parts, ContentPart{Kind: KindText, Text: v})
			}
		}
	}

	// Extract tool calls.
	for _, tc := range wire.ToolCalls {
		args := json.RawMessage(tc.Function.Arguments)
		msg.Parts = append(msg.Parts, ContentPart{
			Kind: KindToolCall,
			ToolCall: &ToolCall{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: args,
			},
		})
	}

	return msg, nil
}

// mapFinishReason converts a provider finish reason string to the unified set.
func mapFinishReason(raw string) string {
	switch raw {
	case "stop":
		return "stop"
	case "length":
		return "length"
	case "tool_calls":
		return "tool_calls"
	case "content_filter":
		return "content_filter"
	default:
		if raw == "" {
			return "stop"
		}
		return "other"
	}
}

// --- HTTP execution ---

// doRequest sends an HTTP POST to the OpenRouter chat completions endpoint and
// returns the raw response body. It maps HTTP errors to typed errors.
func doRequest(ctx context.Context, httpClient *http.Client, baseURL, apiKey string, orReq orRequest) ([]byte, error) {
	payload, err := json.Marshal(orReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := baseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, &NetworkError{Message: "failed to build HTTP request", Cause: err}
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	// OpenRouter recommends these headers for identification.
	httpReq.Header.Set("HTTP-Referer", "https://github.com/campallison/attractor")
	httpReq.Header.Set("X-Title", "attractor")

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, &NetworkError{Message: "HTTP request failed", Cause: err}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &NetworkError{Message: "failed to read response body", Cause: err}
	}

	if resp.StatusCode != http.StatusOK {
		slog.Warn("llm.http.error", "status", resp.StatusCode, "body_bytes", len(body))
		return nil, classifyHTTPError(openRouterName, resp.StatusCode, body)
	}

	slog.Debug("llm.http", "req_bytes", len(payload), "resp_bytes", len(body), "status", resp.StatusCode)
	return body, nil
}
