package main

import "testing"

func TestDummyValue(t *testing.T) {
	tests := []struct {
		name      string
		inputType string
		want      string
	}{
		{"email", "text", "test@example.com"},
		{"user-email", "text", "test@example.com"},
		{"field", "email", "test@example.com"},
		{"password", "text", "testpass123"},
		{"pass", "password", "testpass123"},
		{"team-name", "text", "Test User"},
		{"display-name", "text", "Test User"},
		{"website-url", "text", "https://example.com"},
		{"link", "url", "https://example.com"},
		{"quantity", "number", "42"},
		{"csrf", "hidden", "test-hidden"},
		{"content", "text", "test-value"},
		{"title", "text", "test-value"},
	}

	for _, tt := range tests {
		t.Run(tt.name+"/"+tt.inputType, func(t *testing.T) {
			got := dummyValue(tt.name, tt.inputType)
			if got != tt.want {
				t.Errorf("dummyValue(%q, %q) = %q, want %q",
					tt.name, tt.inputType, got, tt.want)
			}
		})
	}
}

func TestBuildFormMap(t *testing.T) {
	m := buildFormMap(t.TempDir())
	if m == nil {
		t.Error("buildFormMap on empty dir returned nil, want empty map")
	}
	if len(m) != 0 {
		t.Errorf("buildFormMap on empty dir returned %d entries, want 0", len(m))
	}
}
