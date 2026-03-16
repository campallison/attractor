package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
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

// orCacheControl marks a content block as a prompt caching breakpoint
// for Anthropic models routed through OpenRouter.
type orCacheControl struct {
	Type string `json:"type"` // "ephemeral"
}

// orContentPart is a single part inside a content array.
type orContentPart struct {
	Type         string          `json:"type"`
	Text         string          `json:"text,omitempty"`
	CacheControl *orCacheControl `json:"cache_control,omitempty"`
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

// orProviderPreferences controls provider routing behaviour on OpenRouter.
type orProviderPreferences struct {
	ZDR            bool     `json:"zdr,omitempty"`
	DataCollection string   `json:"data_collection,omitempty"` // "allow" or "deny"
	Order          []string `json:"order,omitempty"`
	Ignore         []string `json:"ignore,omitempty"`
}

// orReasoning controls thinking/reasoning token allocation on OpenRouter.
// For Anthropic models this maps to thinking.budget_tokens.
type orReasoning struct {
	MaxTokens int `json:"max_tokens,omitempty"`
}

// orRequest is the full request body sent to the OpenRouter chat completions endpoint.
type orRequest struct {
	Model       string                 `json:"model"`
	Messages    []orMessage            `json:"messages"`
	Tools       []orTool               `json:"tools,omitempty"`
	ToolChoice  interface{}            `json:"tool_choice,omitempty"` // string or object
	MaxTokens   int                    `json:"max_tokens,omitempty"`
	Temperature *float64               `json:"temperature,omitempty"`
	Provider    *orProviderPreferences `json:"provider,omitempty"`
	Reasoning   *orReasoning           `json:"reasoning,omitempty"`
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
		PromptTokensDetails *struct {
			CachedTokens     int `json:"cached_tokens"`
			CacheWriteTokens int `json:"cache_write_tokens"`
		} `json:"prompt_tokens_details,omitempty"`
	} `json:"usage"`
}

// --- Request translation ---

// buildORRequest converts a unified Request into the OpenRouter wire format.
// When zdr is true, the request includes provider preferences that enforce
// Zero Data Retention routing on OpenRouter.
// When promptCaching is true and the model is an Anthropic model, system and
// user messages are sent in content-array format with cache_control breakpoints.
func buildORRequest(req Request, zdr, promptCaching bool) (orRequest, error) {
	messages, err := translateMessages(req.Messages, promptCaching, req.Model)
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

	if req.ReasoningMaxTokens > 0 {
		orReq.Reasoning = &orReasoning{MaxTokens: req.ReasoningMaxTokens}
	}

	// Prefer Anthropic's direct API; Bedrock drops connections on large outputs.
	prov := &orProviderPreferences{
		Order: []string{"anthropic"},
	}
	if zdr {
		prov.ZDR = true
		prov.DataCollection = "deny"
	}
	orReq.Provider = prov

	return orReq, nil
}

// maxCacheBreakpoints is the Anthropic API limit on cache_control blocks.
const maxCacheBreakpoints = 4

// translateMessages converts unified Messages to OpenRouter wire messages.
// When promptCaching is true and the model is an Anthropic model, up to
// maxCacheBreakpoints messages get cache_control breakpoints, placed at the
// system message, first user message, and the last user messages.
func translateMessages(msgs []Message, promptCaching bool, model string) ([]orMessage, error) {
	cache := promptCaching && strings.HasPrefix(model, "anthropic/")

	cacheSet := map[int]bool{}
	if cache {
		cacheSet = selectCacheBreakpoints(msgs)
	}

	out := make([]orMessage, 0, len(msgs))
	for i, m := range msgs {
		wire, err := translateMessage(m, cacheSet[i])
		if err != nil {
			return nil, err
		}
		out = append(out, wire...)
	}
	return out, nil
}

// selectCacheBreakpoints chooses which message indices should receive
// cache_control markers, respecting the maxCacheBreakpoints limit.
// Strategy: system message (stable across all rounds) + first user message
// (original prompt, stable) + last user messages (growing conversation prefix).
func selectCacheBreakpoints(msgs []Message) map[int]bool {
	marks := map[int]bool{}

	for i, m := range msgs {
		if m.Role == RoleSystem {
			marks[i] = true
			break
		}
	}

	var userIndices []int
	for i, m := range msgs {
		if m.Role == RoleUser {
			userIndices = append(userIndices, i)
		}
	}

	if len(userIndices) > 0 {
		marks[userIndices[0]] = true
	}

	remaining := maxCacheBreakpoints - len(marks)
	for i := len(userIndices) - 1; i >= 0 && remaining > 0; i-- {
		if !marks[userIndices[i]] {
			marks[userIndices[i]] = true
			remaining--
		}
	}

	return marks
}

// translateMessage converts a single unified Message to one or more wire messages.
// An assistant message with both text and tool calls is represented as a single
// message with the content field set to the text (may be empty) and tool_calls
// carrying the call objects.
// When cacheControl is true, system and user messages use the content-array
// format with a cache_control breakpoint on the text block.
func translateMessage(m Message, cacheControl bool) ([]orMessage, error) {
	switch m.Role {
	case RoleSystem:
		if cacheControl {
			return []orMessage{{Role: "system", Content: []orContentPart{{
				Type:         "text",
				Text:         m.Text(),
				CacheControl: &orCacheControl{Type: "ephemeral"},
			}}}}, nil
		}
		return []orMessage{{Role: "system", Content: m.Text()}}, nil

	case RoleUser:
		if cacheControl {
			return []orMessage{{Role: "user", Content: []orContentPart{{
				Type:         "text",
				Text:         m.Text(),
				CacheControl: &orCacheControl{Type: "ephemeral"},
			}}}}, nil
		}
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

	// Diagnostic: log when finish reason is unexpected. "length" means
	// MaxTokens was reached — log at Debug since it's a normal budget event.
	switch choice.FinishReason {
	case "stop", "tool_calls":
		// normal
	case "length":
		slog.Debug("llm.response.truncated", "finish_reason", "length")
	default:
		slog.Warn("llm.response.diagnostic",
			"finish_reason", choice.FinishReason,
			"body", string(body),
		)
	}
	msg, err := parseORMessage(choice.Message)
	if err != nil {
		return Response{}, fmt.Errorf("failed to parse response message: %w", err)
	}

	model := raw.Model
	if model == "" {
		model = requestedModel
	}

	usage := Usage{
		InputTokens:  raw.Usage.PromptTokens,
		OutputTokens: raw.Usage.CompletionTokens,
		TotalTokens:  raw.Usage.TotalTokens,
	}
	if d := raw.Usage.PromptTokensDetails; d != nil {
		usage.CacheReadTokens = d.CachedTokens
		usage.CacheCreationTokens = d.CacheWriteTokens
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
		Usage: usage,
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

	// Diagnostic: log whether max_tokens and reasoning are present in the request
	if orReq.MaxTokens > 0 || orReq.Reasoning != nil {
		var reasoningBudget int
		if orReq.Reasoning != nil {
			reasoningBudget = orReq.Reasoning.MaxTokens
		}
		slog.Debug("llm.request.tokens", "max_tokens", orReq.MaxTokens, "reasoning_max_tokens", reasoningBudget)
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
