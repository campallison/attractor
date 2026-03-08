package pipeline

import (
	"context"
	"testing"

	"github.com/campallison/attractor/internal/dot"
	"github.com/campallison/attractor/internal/llm"
	"github.com/google/go-cmp/cmp"
)

// modelCapturingClient records the model from the most recent Complete call
// and returns a plain text response so the agent loop terminates immediately.
type modelCapturingClient struct {
	capturedModel string
}

func (c *modelCapturingClient) Complete(_ context.Context, req llm.Request) (llm.Response, error) {
	c.capturedModel = req.Model
	return llm.Response{
		Message: llm.AssistantMessage("done"),
	}, nil
}

func TestAgentBackend_ModelOverride(t *testing.T) {
	tests := []struct {
		name          string
		backendModel  string
		nodeAttrs     map[string]string
		expectedModel string
	}{
		{
			name:          "node with model attribute overrides backend default",
			backendModel:  "default/model",
			nodeAttrs:     map[string]string{"model": "anthropic/claude-sonnet-4"},
			expectedModel: "anthropic/claude-sonnet-4",
		},
		{
			name:          "node without model attribute uses backend default",
			backendModel:  "default/model",
			nodeAttrs:     map[string]string{},
			expectedModel: "default/model",
		},
		{
			name:          "node with empty model attribute uses backend default",
			backendModel:  "default/model",
			nodeAttrs:     map[string]string{"model": ""},
			expectedModel: "default/model",
		},
		{
			name:          "node with different model than backend",
			backendModel:  "anthropic/claude-sonnet-4",
			nodeAttrs:     map[string]string{"model": "anthropic/claude-3.5-haiku"},
			expectedModel: "anthropic/claude-3.5-haiku",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &modelCapturingClient{}
			backend := AgentBackend{
				Client:  client,
				Model:   tt.backendModel,
				WorkDir: t.TempDir(),
			}
			node := &dot.Node{ID: "test_node", Attrs: tt.nodeAttrs}

			_, _ = backend.Run(node, "test prompt", nil)

			if diff := cmp.Diff(tt.expectedModel, client.capturedModel); diff != "" {
				t.Errorf("model mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
