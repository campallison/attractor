package llm

import (
	"bufio"
	"context"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

// Client is the entry point for making LLM requests. Currently backed by OpenRouter.
type Client struct {
	apiKey        string
	baseURL       string
	httpClient    *http.Client
	zdr           bool // enforce Zero Data Retention routing
	promptCaching bool // enable prompt caching for supported providers
}

// ClientOption is a functional option for configuring a Client.
type ClientOption func(*Client)

// WithBaseURL overrides the default OpenRouter base URL. Useful for testing.
func WithBaseURL(url string) ClientOption {
	return func(c *Client) {
		c.baseURL = url
	}
}

// WithHTTPClient replaces the default HTTP client.
func WithHTTPClient(hc *http.Client) ClientOption {
	return func(c *Client) {
		c.httpClient = hc
	}
}

// WithZDR enables Zero Data Retention enforcement. When set, every request
// includes provider preferences that restrict routing to ZDR-compliant
// endpoints on OpenRouter.
func WithZDR() ClientOption {
	return func(c *Client) {
		c.zdr = true
	}
}

// WithPromptCaching enables prompt caching for supported providers. Currently
// only Anthropic models via OpenRouter support this. System and user messages
// are sent with cache_control breakpoints, allowing subsequent rounds in an
// agent loop to read cached prompt tokens at ~10% of the normal input cost.
func WithPromptCaching() ClientOption {
	return func(c *Client) {
		c.promptCaching = true
	}
}

// NewClientFromEnv creates a Client by reading OPENROUTER_API_KEY from the
// environment. It also attempts to load a .env file from the current working
// directory so that local development works without pre-exporting variables.
func NewClientFromEnv(opts ...ClientOption) (*Client, error) {
	loadDotEnv()

	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" {
		return nil, &ConfigurationError{
			Message: "OPENROUTER_API_KEY environment variable is not set",
		}
	}

	return NewClient(apiKey, opts...)
}

// NewClient creates a Client with an explicit API key.
func NewClient(apiKey string, opts ...ClientOption) (*Client, error) {
	if apiKey == "" {
		return nil, &ConfigurationError{Message: "apiKey must not be empty"}
	}

	c := &Client{
		apiKey:  apiKey,
		baseURL: openRouterBaseURL,
		httpClient: &http.Client{
			Timeout: 360 * time.Second,
		},
	}

	for _, opt := range opts {
		opt(c)
	}

	return c, nil
}

// Complete sends a blocking chat completion request and returns the full response.
// The context controls cancellation and deadline.
func (c *Client) Complete(ctx context.Context, req Request) (Response, error) {
	orReq, err := buildORRequest(req, c.zdr, c.promptCaching)
	if err != nil {
		return Response{}, err
	}

	start := time.Now()
	body, err := doRequest(ctx, c.httpClient, c.baseURL, c.apiKey, orReq)
	latency := time.Since(start)

	if err != nil {
		slog.Warn("llm.call.error", "model", req.Model, "latency_ms", latency.Milliseconds(), "error", err)
		return Response{}, err
	}

	resp, err := parseORResponse(body, req.Model)
	if err != nil {
		slog.Warn("llm.parse.error", "model", req.Model, "error", err)
		return Response{}, err
	}

	logAttrs := []any{
		"model", resp.Model,
		"latency_ms", latency.Milliseconds(),
		"tokens_in", resp.Usage.InputTokens,
		"tokens_out", resp.Usage.OutputTokens,
		"finish", resp.FinishReason.Raw,
	}
	if resp.Usage.CacheReadTokens > 0 || resp.Usage.CacheCreationTokens > 0 {
		logAttrs = append(logAttrs,
			"cache_read", resp.Usage.CacheReadTokens,
			"cache_write", resp.Usage.CacheCreationTokens,
		)
	}
	slog.Info("llm.call", logAttrs...)

	return resp, nil
}

// loadDotEnv reads a .env file from the current working directory and sets any
// key=value pairs as environment variables (only if the key is not already set).
// It silently ignores missing files and malformed lines.
func loadDotEnv() {
	f, err := os.Open(".env")
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		// Strip surrounding quotes if present.
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') ||
			(value[0] == '\'' && value[len(value)-1] == '\'')) {
			value = value[1 : len(value)-1]
		}
		if os.Getenv(key) == "" {
			os.Setenv(key, value)
		}
	}
}
