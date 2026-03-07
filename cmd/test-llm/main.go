// test-llm is a smoke test for the Layer 1 LLM client.
// Run from the project root (where .env lives):
//
//	go run ./cmd/test-llm
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/campallison/attractor/internal/llm"
)

func main() {
	client, err := llm.NewClientFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create client: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	ok := true
	ok = testPlainText(ctx, client) && ok
	ok = testToolCall(ctx, client) && ok

	if !ok {
		os.Exit(1)
	}
	fmt.Println("\nAll tests passed.")
}

// testPlainText sends a simple question and verifies a non-empty text response.
func testPlainText(ctx context.Context, client *llm.Client) bool {
	fmt.Println("=== Test 1: Plain text ===")

	resp, err := client.Complete(ctx, llm.Request{
		Model: "openai/gpt-4o-mini",
		Messages: []llm.Message{
			llm.UserMessage("What is 2+2? Reply with only the number."),
		},
		MaxTokens: 16,
	})
	if err != nil {
		fmt.Printf("FAIL: request error: %v\n", err)
		return false
	}

	text := resp.Text()
	fmt.Printf("Model:         %s\n", resp.Model)
	fmt.Printf("Finish reason: %s (raw: %s)\n", resp.FinishReason.Reason, resp.FinishReason.Raw)
	fmt.Printf("Usage:         in=%d out=%d total=%d\n",
		resp.Usage.InputTokens, resp.Usage.OutputTokens, resp.Usage.TotalTokens)
	fmt.Printf("Response text: %q\n", text)

	if text == "" {
		fmt.Println("FAIL: expected non-empty text response")
		return false
	}
	fmt.Println("PASS")
	return true
}

// testToolCall sends a message with a get_weather tool and verifies the model
// responds with a tool call rather than plain text.
func testToolCall(ctx context.Context, client *llm.Client) bool {
	fmt.Println("\n=== Test 2: Tool call ===")

	weatherParams := json.RawMessage(`{
		"type": "object",
		"properties": {
			"location": {
				"type": "string",
				"description": "City name, e.g. 'San Francisco, CA'"
			},
			"unit": {
				"type": "string",
				"enum": ["celsius", "fahrenheit"],
				"description": "Temperature unit"
			}
		},
		"required": ["location"]
	}`)

	resp, err := client.Complete(ctx, llm.Request{
		Model: "openai/gpt-4o-mini",
		Messages: []llm.Message{
			llm.UserMessage("What's the weather like in Tokyo right now?"),
		},
		Tools: []llm.ToolDefinition{
			{
				Name:        "get_weather",
				Description: "Get the current weather for a location",
				Parameters:  weatherParams,
			},
		},
		MaxTokens: 256,
	})
	if err != nil {
		fmt.Printf("FAIL: request error: %v\n", err)
		return false
	}

	fmt.Printf("Model:         %s\n", resp.Model)
	fmt.Printf("Finish reason: %s (raw: %s)\n", resp.FinishReason.Reason, resp.FinishReason.Raw)
	fmt.Printf("Usage:         in=%d out=%d total=%d\n",
		resp.Usage.InputTokens, resp.Usage.OutputTokens, resp.Usage.TotalTokens)

	calls := resp.ToolCalls()
	if resp.FinishReason.Reason != "tool_calls" {
		fmt.Printf("FAIL: expected finish_reason=tool_calls, got %q\n", resp.FinishReason.Reason)
		return false
	}
	if len(calls) == 0 {
		fmt.Println("FAIL: expected at least one tool call")
		return false
	}

	for i, call := range calls {
		fmt.Printf("Tool call %d:   id=%s name=%s args=%s\n", i+1, call.ID, call.Name, string(call.Arguments))
	}

	if calls[0].Name != "get_weather" {
		fmt.Printf("FAIL: expected tool name 'get_weather', got %q\n", calls[0].Name)
		return false
	}

	fmt.Println("PASS")
	return true
}
