package pipeline

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/campallison/attractor/internal/dot"
	"github.com/google/go-cmp/cmp"
)

func TestStartHandler(t *testing.T) {
	h := StartHandler{}
	node := &dot.Node{ID: "s", Attrs: map[string]string{"shape": "Mdiamond"}}
	out := h.Execute(node, NewContext(), &dot.Graph{Attrs: map[string]string{}}, "")
	if diff := cmp.Diff(StatusSuccess, out.Status); diff != "" {
		t.Errorf("status mismatch (-want +got):\n%s", diff)
	}
}

func TestExitHandler(t *testing.T) {
	h := ExitHandler{}
	node := &dot.Node{ID: "e", Attrs: map[string]string{"shape": "Msquare"}}
	out := h.Execute(node, NewContext(), &dot.Graph{Attrs: map[string]string{}}, "")
	if diff := cmp.Diff(StatusSuccess, out.Status); diff != "" {
		t.Errorf("status mismatch (-want +got):\n%s", diff)
	}
}

func TestConditionalHandler(t *testing.T) {
	h := ConditionalHandler{}
	node := &dot.Node{ID: "gate", Attrs: map[string]string{"shape": "diamond"}}
	out := h.Execute(node, NewContext(), &dot.Graph{Attrs: map[string]string{}}, "")
	if diff := cmp.Diff(StatusSuccess, out.Status); diff != "" {
		t.Errorf("status mismatch (-want +got):\n%s", diff)
	}
	if !strings.Contains(out.Notes, "gate") {
		t.Errorf("expected notes to mention node ID, got %q", out.Notes)
	}
}

func TestCodergenHandler_SimulatedMode(t *testing.T) {
	logsRoot := t.TempDir()
	h := CodergenHandler{Backend: nil}
	node := &dot.Node{ID: "plan", Attrs: map[string]string{
		"shape":  "box",
		"prompt": "Plan the feature for: $goal",
	}}
	g := &dot.Graph{Attrs: map[string]string{"goal": "build a widget"}}
	ctx := NewContext()

	out := h.Execute(node, ctx, g, logsRoot)

	if diff := cmp.Diff(StatusSuccess, out.Status); diff != "" {
		t.Errorf("status mismatch (-want +got):\n%s", diff)
	}

	// Check prompt file was written with expanded variable.
	promptData, err := os.ReadFile(filepath.Join(logsRoot, "plan", "prompt.md"))
	if err != nil {
		t.Fatalf("prompt.md not written: %v", err)
	}
	if diff := cmp.Diff("Plan the feature for: build a widget", string(promptData)); diff != "" {
		t.Errorf("prompt content mismatch (-want +got):\n%s", diff)
	}

	// Check response file.
	respData, err := os.ReadFile(filepath.Join(logsRoot, "plan", "response.md"))
	if err != nil {
		t.Fatalf("response.md not written: %v", err)
	}
	if !strings.Contains(string(respData), "plan") {
		t.Errorf("expected simulated response to mention stage ID, got %q", string(respData))
	}

	// Check status.json exists.
	if _, err := os.Stat(filepath.Join(logsRoot, "plan", "status.json")); err != nil {
		t.Errorf("status.json not written: %v", err)
	}

	// Check context updates.
	if diff := cmp.Diff("plan", out.ContextUpdates["last_stage"]); diff != "" {
		t.Errorf("last_stage mismatch (-want +got):\n%s", diff)
	}
}

func TestCodergenHandler_WithBackend(t *testing.T) {
	logsRoot := t.TempDir()
	backend := SimulatedBackend{}
	h := CodergenHandler{Backend: backend}
	node := &dot.Node{ID: "impl", Attrs: map[string]string{
		"shape":  "box",
		"prompt": "Implement it",
	}}
	g := &dot.Graph{Attrs: map[string]string{}}
	ctx := NewContext()

	out := h.Execute(node, ctx, g, logsRoot)

	if diff := cmp.Diff(StatusSuccess, out.Status); diff != "" {
		t.Errorf("status mismatch (-want +got):\n%s", diff)
	}

	respData, err := os.ReadFile(filepath.Join(logsRoot, "impl", "response.md"))
	if err != nil {
		t.Fatalf("response.md not written: %v", err)
	}
	if !strings.Contains(string(respData), "impl") {
		t.Errorf("backend response should mention stage ID, got %q", string(respData))
	}
}

type failingBackend struct{ msg string }

func (b failingBackend) Run(_ *dot.Node, _ string, _ *Context) (string, error) {
	return "", fmt.Errorf("%s", b.msg)
}

func TestCodergenHandler_BackendError(t *testing.T) {
	logsRoot := t.TempDir()
	h := CodergenHandler{Backend: failingBackend{msg: "LLM unavailable"}}
	node := &dot.Node{ID: "fail_node", Attrs: map[string]string{
		"shape":  "box",
		"prompt": "Do something",
	}}
	g := &dot.Graph{Attrs: map[string]string{}}

	out := h.Execute(node, NewContext(), g, logsRoot)

	if diff := cmp.Diff(StatusFail, out.Status); diff != "" {
		t.Errorf("status mismatch (-want +got):\n%s", diff)
	}
	if !strings.Contains(out.FailureReason, "LLM unavailable") {
		t.Errorf("expected failure reason to contain error, got %q", out.FailureReason)
	}
}

func TestCodergenHandler_FallbackToLabel(t *testing.T) {
	logsRoot := t.TempDir()
	h := CodergenHandler{Backend: nil}
	node := &dot.Node{ID: "review", Attrs: map[string]string{
		"shape": "box",
		"label": "Review the code",
	}}
	g := &dot.Graph{Attrs: map[string]string{}}

	h.Execute(node, NewContext(), g, logsRoot)

	promptData, err := os.ReadFile(filepath.Join(logsRoot, "review", "prompt.md"))
	if err != nil {
		t.Fatalf("prompt.md not written: %v", err)
	}
	if diff := cmp.Diff("Review the code", string(promptData)); diff != "" {
		t.Errorf("should fall back to label when prompt is empty (-want +got):\n%s", diff)
	}
}

func TestHandlerRegistry_Resolve(t *testing.T) {
	registry := DefaultHandlerRegistry(nil)

	tests := []struct {
		name     string
		node     *dot.Node
		wantType string
	}{
		{
			name:     "start by shape",
			node:     &dot.Node{ID: "s", Attrs: map[string]string{"shape": "Mdiamond"}},
			wantType: "pipeline.StartHandler",
		},
		{
			name:     "exit by shape",
			node:     &dot.Node{ID: "e", Attrs: map[string]string{"shape": "Msquare"}},
			wantType: "pipeline.ExitHandler",
		},
		{
			name:     "box defaults to codergen",
			node:     &dot.Node{ID: "w", Attrs: map[string]string{"shape": "box"}},
			wantType: "pipeline.CodergenHandler",
		},
		{
			name:     "diamond to conditional",
			node:     &dot.Node{ID: "g", Attrs: map[string]string{"shape": "diamond"}},
			wantType: "pipeline.ConditionalHandler",
		},
		{
			name:     "explicit type overrides shape",
			node:     &dot.Node{ID: "x", Attrs: map[string]string{"shape": "box", "type": "conditional"}},
			wantType: "pipeline.ConditionalHandler",
		},
		{
			name:     "unknown shape falls back to default",
			node:     &dot.Node{ID: "u", Attrs: map[string]string{"shape": "oval"}},
			wantType: "pipeline.CodergenHandler",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := registry.Resolve(tt.node)
			got := handlerTypeName(h)
			if diff := cmp.Diff(tt.wantType, got); diff != "" {
				t.Errorf("handler type mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func handlerTypeName(h Handler) string {
	return fmt.Sprintf("%T", h)
}

func TestSanitizeNodeID(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want string
	}{
		{name: "simple id", id: "plan", want: "plan"},
		{name: "id with hyphen", id: "code-review", want: "code-review"},
		{name: "dot-dot escape", id: "../escape", want: "__escape"},
		{name: "deep dot-dot escape", id: "../../etc", want: "____etc"},
		{name: "slash in id", id: "a/b/c", want: "a_b_c"},
		{name: "dot-dot and slash combined", id: "../../etc/passwd", want: "____etc_passwd"},
		{name: "empty string", id: "", want: "_unnamed"},
		{name: "just dots", id: "..", want: "_"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeNodeID(tt.id)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("sanitizeNodeID(%q) mismatch (-want +got):\n%s", tt.id, diff)
			}
		})
	}
}

func TestCodergenHandler_PathTraversalNodeID(t *testing.T) {
	logsRoot := t.TempDir()
	h := CodergenHandler{Backend: nil}
	node := &dot.Node{ID: "../../etc", Attrs: map[string]string{
		"shape":  "box",
		"prompt": "try to escape",
	}}
	g := &dot.Graph{Attrs: map[string]string{}}

	h.Execute(node, NewContext(), g, logsRoot)

	// The sanitized directory should be inside logsRoot, not above it.
	sanitized := sanitizeNodeID("../../etc")
	expectedDir := filepath.Join(logsRoot, sanitized)
	if _, err := os.Stat(expectedDir); err != nil {
		t.Fatalf("expected sanitized directory %q to exist: %v", expectedDir, err)
	}
	if _, err := os.Stat(filepath.Join(expectedDir, "prompt.md")); err != nil {
		t.Fatalf("prompt.md should exist in sanitized directory: %v", err)
	}

	// Verify nothing was written above logsRoot by checking the parent.
	parent := filepath.Dir(logsRoot)
	escaped := filepath.Join(parent, "etc")
	if _, err := os.Stat(escaped); err == nil {
		t.Errorf("path traversal succeeded: directory %q should not exist", escaped)
	}
}
