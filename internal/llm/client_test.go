package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func TestNewClient(t *testing.T) {
	tests := []struct {
		name    string
		apiKey  string
		wantErr bool
	}{
		{
			name:   "valid API key",
			apiKey: "sk-test-key",
		},
		{
			name:    "empty API key",
			apiKey:  "",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewClient(tt.apiKey)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				var cfgErr *ConfigurationError
				if !errors.As(err, &cfgErr) {
					t.Errorf("expected *ConfigurationError, got %T", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if client == nil {
				t.Fatal("expected non-nil client")
			}
		})
	}
}

func TestNewClientOptions(t *testing.T) {
	customHTTP := &http.Client{Timeout: 5 * time.Second}

	client, err := NewClient("sk-test",
		WithBaseURL("https://custom.example.com/v1"),
		WithHTTPClient(customHTTP),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if diff := cmp.Diff("https://custom.example.com/v1", client.baseURL); diff != "" {
		t.Errorf("baseURL mismatch (-want +got):\n%s", diff)
	}
	if client.httpClient != customHTTP {
		t.Error("expected custom HTTP client to be applied")
	}
}

func TestNewClientFromEnv(t *testing.T) {
	tests := []struct {
		name    string
		envKey  string
		envVal  string
		wantErr bool
	}{
		{
			name:   "key present in env",
			envKey: "OPENROUTER_API_KEY",
			envVal: "sk-from-env",
		},
		{
			name:    "key missing from env",
			envKey:  "OPENROUTER_API_KEY",
			envVal:  "",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envVal != "" {
				t.Setenv("OPENROUTER_API_KEY", tt.envVal)
			} else {
				t.Setenv("OPENROUTER_API_KEY", "")
			}

			client, err := NewClientFromEnv()
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if client == nil {
				t.Fatal("expected non-nil client")
			}
		})
	}
}

func TestCompleteRoundTrip(t *testing.T) {
	// Stand up a fake OpenRouter server that returns a canned response.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request basics.
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/chat/completions" {
			t.Errorf("expected /chat/completions, got %s", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer sk-test" {
			t.Errorf("expected 'Bearer sk-test', got %q", auth)
		}

		resp := `{
			"id": "gen-test-123",
			"model": "test-model",
			"choices": [{
				"message": {"role": "assistant", "content": "42"},
				"finish_reason": "stop"
			}],
			"usage": {"prompt_tokens": 10, "completion_tokens": 1, "total_tokens": 11}
		}`
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(resp))
	}))
	defer server.Close()

	client, err := NewClient("sk-test", WithBaseURL(server.URL))
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	got, err := client.Complete(context.Background(), Request{
		Model:    "test-model",
		Messages: []Message{UserMessage("What is the answer?")},
	})
	if err != nil {
		t.Fatalf("Complete() error: %v", err)
	}

	want := Response{
		ID:       "gen-test-123",
		Model:    "test-model",
		Provider: "openrouter",
		Message: Message{
			Role:  RoleAssistant,
			Parts: []ContentPart{{Kind: KindText, Text: "42"}},
		},
		FinishReason: FinishReason{Reason: "stop", Raw: "stop"},
		Usage:        Usage{InputTokens: 10, OutputTokens: 1, TotalTokens: 11},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Complete() mismatch (-want +got):\n%s", diff)
	}
}

func TestCompleteWithToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify that tool definitions are included in the request body.
		var reqBody orRequest
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Errorf("failed to decode request body: %v", err)
		}
		if len(reqBody.Tools) != 1 {
			t.Errorf("expected 1 tool, got %d", len(reqBody.Tools))
		}

		resp := `{
			"id": "gen-tool-456",
			"model": "test-model",
			"choices": [{
				"message": {
					"role": "assistant",
					"content": null,
					"tool_calls": [{
						"id": "call_abc",
						"type": "function",
						"function": {"name": "get_weather", "arguments": "{\"location\":\"NYC\"}"}
					}]
				},
				"finish_reason": "tool_calls"
			}],
			"usage": {"prompt_tokens": 50, "completion_tokens": 15, "total_tokens": 65}
		}`
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(resp))
	}))
	defer server.Close()

	client, err := NewClient("sk-test", WithBaseURL(server.URL))
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	got, err := client.Complete(context.Background(), Request{
		Model:    "test-model",
		Messages: []Message{UserMessage("weather in NYC?")},
		Tools: []ToolDefinition{{
			Name:        "get_weather",
			Description: "Get weather",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"location":{"type":"string"}}}`),
		}},
	})
	if err != nil {
		t.Fatalf("Complete() error: %v", err)
	}

	if diff := cmp.Diff("tool_calls", got.FinishReason.Reason); diff != "" {
		t.Errorf("FinishReason mismatch (-want +got):\n%s", diff)
	}

	calls := got.ToolCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(calls))
	}
	if diff := cmp.Diff("get_weather", calls[0].Name); diff != "" {
		t.Errorf("tool call name mismatch (-want +got):\n%s", diff)
	}
}

func TestCompleteHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
	}))
	defer server.Close()

	client, err := NewClient("sk-bad-key", WithBaseURL(server.URL))
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	_, err = client.Complete(context.Background(), Request{
		Model:    "test-model",
		Messages: []Message{UserMessage("hello")},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	var authErr *AuthenticationError
	if !errors.As(err, &authErr) {
		t.Errorf("expected *AuthenticationError, got %T: %v", err, err)
	}
}

func TestLoadDotEnv(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		wantKey  string
		wantVal  string
		presetEnv map[string]string
	}{
		{
			name:    "simple key=value",
			content: "TEST_LOAD_A=hello",
			wantKey: "TEST_LOAD_A",
			wantVal: "hello",
		},
		{
			name:    "double-quoted value",
			content: `TEST_LOAD_B="quoted value"`,
			wantKey: "TEST_LOAD_B",
			wantVal: "quoted value",
		},
		{
			name:    "single-quoted value",
			content: `TEST_LOAD_C='single quoted'`,
			wantKey: "TEST_LOAD_C",
			wantVal: "single quoted",
		},
		{
			name:    "skips comments",
			content: "# comment\nTEST_LOAD_D=after_comment",
			wantKey: "TEST_LOAD_D",
			wantVal: "after_comment",
		},
		{
			name:    "skips blank lines",
			content: "\n\nTEST_LOAD_E=after_blanks\n\n",
			wantKey: "TEST_LOAD_E",
			wantVal: "after_blanks",
		},
		{
			name:      "does not override existing env",
			content:   "TEST_LOAD_F=from_file",
			wantKey:   "TEST_LOAD_F",
			wantVal:   "already_set",
			presetEnv: map[string]string{"TEST_LOAD_F": "already_set"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Write a .env file in a temp directory and chdir to it.
			dir := t.TempDir()
			envPath := filepath.Join(dir, ".env")
			if err := os.WriteFile(envPath, []byte(tt.content), 0o644); err != nil {
				t.Fatalf("failed to write .env: %v", err)
			}

			origDir, _ := os.Getwd()
			if err := os.Chdir(dir); err != nil {
				t.Fatalf("failed to chdir: %v", err)
			}
			t.Cleanup(func() { os.Chdir(origDir) })

			// Preset env vars if needed.
			for k, v := range tt.presetEnv {
				t.Setenv(k, v)
			}

			// Clear the target key if not preset (so loadDotEnv can set it).
			if _, preset := tt.presetEnv[tt.wantKey]; !preset {
				t.Setenv(tt.wantKey, "")
				os.Unsetenv(tt.wantKey)
			}

			loadDotEnv()

			got := os.Getenv(tt.wantKey)
			if diff := cmp.Diff(tt.wantVal, got); diff != "" {
				t.Errorf("env var %s mismatch (-want +got):\n%s", tt.wantKey, diff)
			}
		})
	}
}
