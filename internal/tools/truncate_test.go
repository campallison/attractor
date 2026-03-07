package tools

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestTruncateOutput(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxChars int
		want     string
	}{
		{
			name:     "under limit returned unchanged",
			input:    "short",
			maxChars: 100,
			want:     "short",
		},
		{
			name:     "exactly at limit returned unchanged",
			input:    "12345",
			maxChars: 5,
			want:     "12345",
		},
		{
			name:     "empty output returned as-is",
			input:    "",
			maxChars: 100,
			want:     "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TruncateOutput(tt.input, tt.maxChars)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("TruncateOutput() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestTruncateOutputOverLimit(t *testing.T) {
	input := strings.Repeat("x", 100)
	got := TruncateOutput(input, 20)

	// The output should start with the first 10 chars.
	if !strings.HasPrefix(got, "xxxxxxxxxx") {
		t.Errorf("expected output to start with first 10 x's, got prefix: %q", got[:20])
	}

	// The output should end with the last 10 chars.
	if !strings.HasSuffix(got, "xxxxxxxxxx") {
		t.Errorf("expected output to end with last 10 x's")
	}

	// The output should contain the truncation warning.
	if !strings.Contains(got, "WARNING") {
		t.Error("expected output to contain 'WARNING'")
	}
	if !strings.Contains(got, "80 characters were removed") {
		t.Error("expected output to mention '80 characters were removed'")
	}
}

func TestTruncateOutputEdgeCaseBoundary(t *testing.T) {
	input := "abcdefghij"
	got := TruncateOutput(input, 6)

	// half=3, so first 3 chars + warning + last 3 chars
	if !strings.HasPrefix(got, "abc") {
		t.Errorf("expected prefix 'abc', got %q", got[:3])
	}
	if !strings.HasSuffix(got, "hij") {
		t.Errorf("expected suffix 'hij'")
	}
	if !strings.Contains(got, "WARNING") {
		t.Error("expected truncation warning")
	}
}

func TestTruncateToolOutput(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		input    string
		changed  bool
	}{
		{
			name:     "known tool within limit",
			toolName: "write_file",
			input:    "Wrote 10 bytes",
			changed:  false,
		},
		{
			name:     "known tool exceeds limit",
			toolName: "write_file",
			input:    strings.Repeat("x", 2000),
			changed:  true,
		},
		{
			name:     "unknown tool returns unchanged",
			toolName: "unknown_tool",
			input:    strings.Repeat("x", 100000),
			changed:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TruncateToolOutput(tt.input, tt.toolName)
			if tt.changed && got == tt.input {
				t.Error("expected output to be truncated, but it was unchanged")
			}
			if !tt.changed && got != tt.input {
				t.Error("expected output to be unchanged, but it was modified")
			}
		})
	}
}
