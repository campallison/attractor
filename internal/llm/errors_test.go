package llm

import (
	"errors"
	"strings"
	"testing"
)

func TestClassifyHTTPError(t *testing.T) {
	jsonBody := func(msg string) []byte {
		return []byte(`{"error":{"message":"` + msg + `","code":"test_code"}}`)
	}

	tests := []struct {
		name       string
		statusCode int
		body       []byte
		wantType   string
		retryable  bool
		wantMsg    string
	}{
		{
			name:       "400 InvalidRequestError",
			statusCode: 400,
			body:       jsonBody("bad request"),
			wantType:   "InvalidRequestError",
			retryable:  false,
			wantMsg:    "bad request",
		},
		{
			name:       "422 InvalidRequestError",
			statusCode: 422,
			body:       jsonBody("unprocessable"),
			wantType:   "InvalidRequestError",
			retryable:  false,
			wantMsg:    "unprocessable",
		},
		{
			name:       "401 AuthenticationError",
			statusCode: 401,
			body:       jsonBody("invalid api key"),
			wantType:   "AuthenticationError",
			retryable:  false,
			wantMsg:    "invalid api key",
		},
		{
			name:       "403 AccessDeniedError",
			statusCode: 403,
			body:       jsonBody("forbidden"),
			wantType:   "AccessDeniedError",
			retryable:  false,
			wantMsg:    "forbidden",
		},
		{
			name:       "404 NotFoundError",
			statusCode: 404,
			body:       jsonBody("model not found"),
			wantType:   "NotFoundError",
			retryable:  false,
			wantMsg:    "model not found",
		},
		{
			name:       "429 RateLimitError",
			statusCode: 429,
			body:       jsonBody("rate limited"),
			wantType:   "RateLimitError",
			retryable:  true,
			wantMsg:    "rate limited",
		},
		{
			name:       "500 ServerError",
			statusCode: 500,
			body:       jsonBody("internal error"),
			wantType:   "ServerError",
			retryable:  true,
			wantMsg:    "internal error",
		},
		{
			name:       "502 ServerError",
			statusCode: 502,
			body:       jsonBody("bad gateway"),
			wantType:   "ServerError",
			retryable:  true,
			wantMsg:    "bad gateway",
		},
		{
			name:       "503 ServerError",
			statusCode: 503,
			body:       jsonBody("unavailable"),
			wantType:   "ServerError",
			retryable:  true,
			wantMsg:    "unavailable",
		},
		{
			name:       "plain text body fallback",
			statusCode: 401,
			body:       []byte("Unauthorized"),
			wantType:   "AuthenticationError",
			retryable:  false,
			wantMsg:    "Unauthorized",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := classifyHTTPError("openrouter", tt.statusCode, tt.body)
			if err == nil {
				t.Fatal("expected error, got nil")
			}

			// Verify the error message contains the expected substring.
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("error message %q does not contain %q", err.Error(), tt.wantMsg)
			}

			// Verify correct error type via errors.As.
			switch tt.wantType {
			case "InvalidRequestError":
				var target *InvalidRequestError
				if !errors.As(err, &target) {
					t.Errorf("expected *InvalidRequestError, got %T", err)
				} else if target.Retryable != tt.retryable {
					t.Errorf("Retryable = %v, want %v", target.Retryable, tt.retryable)
				}
			case "AuthenticationError":
				var target *AuthenticationError
				if !errors.As(err, &target) {
					t.Errorf("expected *AuthenticationError, got %T", err)
				} else if target.Retryable != tt.retryable {
					t.Errorf("Retryable = %v, want %v", target.Retryable, tt.retryable)
				}
			case "AccessDeniedError":
				var target *AccessDeniedError
				if !errors.As(err, &target) {
					t.Errorf("expected *AccessDeniedError, got %T", err)
				} else if target.Retryable != tt.retryable {
					t.Errorf("Retryable = %v, want %v", target.Retryable, tt.retryable)
				}
			case "NotFoundError":
				var target *NotFoundError
				if !errors.As(err, &target) {
					t.Errorf("expected *NotFoundError, got %T", err)
				} else if target.Retryable != tt.retryable {
					t.Errorf("Retryable = %v, want %v", target.Retryable, tt.retryable)
				}
			case "RateLimitError":
				var target *RateLimitError
				if !errors.As(err, &target) {
					t.Errorf("expected *RateLimitError, got %T", err)
				} else if target.Retryable != tt.retryable {
					t.Errorf("Retryable = %v, want %v", target.Retryable, tt.retryable)
				}
			case "ServerError":
				var target *ServerError
				if !errors.As(err, &target) {
					t.Errorf("expected *ServerError, got %T", err)
				} else if target.Retryable != tt.retryable {
					t.Errorf("Retryable = %v, want %v", target.Retryable, tt.retryable)
				}
			}
		})
	}
}

func TestClassifyHTTPErrorUnknownStatus(t *testing.T) {
	err := classifyHTTPError("openrouter", 418, []byte("I'm a teapot"))
	var pe *ProviderError
	if !errors.As(err, &pe) {
		t.Fatalf("expected *ProviderError, got %T", err)
	}
	if !pe.Retryable {
		t.Error("unknown status codes should default to retryable=true")
	}
}

func TestClassifyHTTPErrorExtractsCode(t *testing.T) {
	body := []byte(`{"error":{"message":"bad","code":"invalid_model","type":"invalid_request_error"}}`)
	err := classifyHTTPError("openrouter", 400, body)
	var target *InvalidRequestError
	if !errors.As(err, &target) {
		t.Fatalf("expected *InvalidRequestError, got %T", err)
	}
	if target.ErrorCode != "invalid_model" {
		t.Errorf("ErrorCode = %q, want %q", target.ErrorCode, "invalid_model")
	}
}

func TestClassifyHTTPErrorFallsBackToType(t *testing.T) {
	body := []byte(`{"error":{"message":"bad","type":"invalid_request_error"}}`)
	err := classifyHTTPError("openrouter", 400, body)
	var target *InvalidRequestError
	if !errors.As(err, &target) {
		t.Fatalf("expected *InvalidRequestError, got %T", err)
	}
	if target.ErrorCode != "invalid_request_error" {
		t.Errorf("ErrorCode = %q, want %q", target.ErrorCode, "invalid_request_error")
	}
}
