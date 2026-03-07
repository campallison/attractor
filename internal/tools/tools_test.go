package tools

import (
	"encoding/json"
	"testing"

	"github.com/campallison/attractor/internal/llm"
	"github.com/google/go-cmp/cmp"
)

func TestRegistryRegisterAndGet(t *testing.T) {
	tests := []struct {
		name      string
		register  []string
		lookup    string
		wantFound bool
	}{
		{
			name:      "registered tool is found",
			register:  []string{"read_file"},
			lookup:    "read_file",
			wantFound: true,
		},
		{
			name:      "unregistered tool is not found",
			register:  []string{"read_file"},
			lookup:    "write_file",
			wantFound: false,
		},
		{
			name:      "empty registry returns not found",
			register:  nil,
			lookup:    "read_file",
			wantFound: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewRegistry()
			for _, name := range tt.register {
				r.Register(RegisteredTool{
					Definition: llm.ToolDefinition{Name: name},
					Execute:    func(args json.RawMessage, workDir string) (string, error) { return "", nil },
				})
			}
			_, found := r.Get(tt.lookup)
			if found != tt.wantFound {
				t.Errorf("Get(%q) found=%v, want %v", tt.lookup, found, tt.wantFound)
			}
		})
	}
}

func TestRegistryDefinitions(t *testing.T) {
	r := NewRegistry()
	r.Register(RegisteredTool{
		Definition: llm.ToolDefinition{Name: "alpha", Description: "tool a"},
		Execute:    func(args json.RawMessage, workDir string) (string, error) { return "", nil },
	})
	r.Register(RegisteredTool{
		Definition: llm.ToolDefinition{Name: "beta", Description: "tool b"},
		Execute:    func(args json.RawMessage, workDir string) (string, error) { return "", nil },
	})

	defs := r.Definitions()
	if len(defs) != 2 {
		t.Fatalf("Definitions() returned %d items, want 2", len(defs))
	}

	names := map[string]bool{}
	for _, d := range defs {
		names[d.Name] = true
	}
	want := map[string]bool{"alpha": true, "beta": true}
	if diff := cmp.Diff(want, names); diff != "" {
		t.Errorf("Definitions() names mismatch (-want +got):\n%s", diff)
	}
}
