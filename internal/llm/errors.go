package llm

import (
	"encoding/json"
	"fmt"
)

// ConfigurationError is returned when the client is misconfigured (e.g. missing API key).
type ConfigurationError struct {
	Message string
}

func (e *ConfigurationError) Error() string {
	return fmt.Sprintf("llm configuration error: %s", e.Message)
}

// NetworkError is returned on transport-level failures before an HTTP response is received.
type NetworkError struct {
	Message string
	Cause   error
}

func (e *NetworkError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("llm network error: %s: %v", e.Message, e.Cause)
	}
	return fmt.Sprintf("llm network error: %s", e.Message)
}

func (e *NetworkError) Unwrap() error { return e.Cause }

// ProviderError is the base type for errors returned by the LLM provider over HTTP.
type ProviderError struct {
	Provider   string
	StatusCode int
	Message    string
	ErrorCode  string
	Retryable  bool
	RetryAfter float64     // seconds to wait before retrying (0 = not specified)
	Raw        interface{} // raw response body for debugging
}

func (e *ProviderError) Error() string {
	if e.ErrorCode != "" {
		return fmt.Sprintf("llm provider error [%s] HTTP %d (%s): %s", e.Provider, e.StatusCode, e.ErrorCode, e.Message)
	}
	return fmt.Sprintf("llm provider error [%s] HTTP %d: %s", e.Provider, e.StatusCode, e.Message)
}

// AuthenticationError is returned on HTTP 401 (invalid API key or expired token).
type AuthenticationError struct{ ProviderError }

// AccessDeniedError is returned on HTTP 403 (insufficient permissions).
type AccessDeniedError struct{ ProviderError }

// NotFoundError is returned on HTTP 404 (model or endpoint not found).
type NotFoundError struct{ ProviderError }

// InvalidRequestError is returned on HTTP 400/422 (malformed request).
type InvalidRequestError struct{ ProviderError }

// RateLimitError is returned on HTTP 429 (rate limit exceeded).
type RateLimitError struct{ ProviderError }

// ServerError is returned on HTTP 5xx (provider internal error).
type ServerError struct{ ProviderError }

// classifyHTTPError maps an HTTP status code and raw response body to the
// appropriate typed error. It follows the mapping in spec Section 6.4.
func classifyHTTPError(provider string, statusCode int, body []byte) error {
	base := ProviderError{
		Provider:   provider,
		StatusCode: statusCode,
		Retryable:  defaultRetryable(statusCode),
	}

	// Attempt to extract a message from the response body.
	var errBody struct {
		Error struct {
			Message string `json:"message"`
			Code    string `json:"code"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &errBody); err == nil && errBody.Error.Message != "" {
		base.Message = errBody.Error.Message
		if errBody.Error.Code != "" {
			base.ErrorCode = errBody.Error.Code
		} else {
			base.ErrorCode = errBody.Error.Type
		}
	} else {
		base.Message = string(body)
	}
	base.Raw = string(body)

	switch statusCode {
	case 400, 422:
		return &InvalidRequestError{base}
	case 401:
		return &AuthenticationError{base}
	case 403:
		return &AccessDeniedError{base}
	case 404:
		return &NotFoundError{base}
	case 429:
		return &RateLimitError{base}
	default:
		if statusCode >= 500 {
			return &ServerError{base}
		}
		// Unknown status: return a plain ProviderError, defaulting to retryable.
		base.Retryable = true
		return &base
	}
}

// defaultRetryable returns true for errors that may succeed on retry.
func defaultRetryable(statusCode int) bool {
	switch statusCode {
	case 429:
		return true
	case 408:
		return true
	}
	if statusCode >= 500 && statusCode <= 504 {
		return true
	}
	return false
}
