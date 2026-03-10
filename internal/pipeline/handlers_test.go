package pipeline

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/campallison/attractor/internal/dot"
	"github.com/campallison/attractor/internal/llm"
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

func (b failingBackend) Run(_ *dot.Node, _ string, _ *Context) (BackendResult, error) {
	return BackendResult{}, fmt.Errorf("%s", b.msg)
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
	registry := DefaultHandlerRegistry(CodergenHandler{})

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

type exhaustedBackend struct {
	rounds int
}

func (b exhaustedBackend) Run(node *dot.Node, _ string, _ *Context) (BackendResult, error) {
	return BackendResult{
		Response:  "Let me read the handler files...",
		Usage:     llm.Usage{InputTokens: 50000, OutputTokens: 8000, TotalTokens: 58000},
		Model:     "anthropic/claude-opus-4.6",
		Rounds:    b.rounds,
		Exhausted: true,
	}, nil
}

func TestCodergenHandler_ExhaustedBackend(t *testing.T) {
	logsRoot := t.TempDir()
	h := CodergenHandler{Backend: exhaustedBackend{rounds: 50}}
	node := &dot.Node{ID: "stuck_stage", Attrs: map[string]string{
		"shape":  "box",
		"prompt": "Implement the feature",
	}}
	g := &dot.Graph{Attrs: map[string]string{}}

	out := h.Execute(node, NewContext(), g, logsRoot)

	if diff := cmp.Diff(StatusFail, out.Status); diff != "" {
		t.Errorf("status mismatch (-want +got):\n%s", diff)
	}
	if !strings.Contains(out.FailureReason, "exhausted round limit") {
		t.Errorf("expected failure reason to mention round limit exhaustion, got %q", out.FailureReason)
	}
	if !strings.Contains(out.FailureReason, "50") {
		t.Errorf("expected failure reason to include round count, got %q", out.FailureReason)
	}
	if out.Usage == nil {
		t.Fatal("expected usage to be recorded even on exhaustion")
	}
	if diff := cmp.Diff(58000, out.Usage.TotalTokens); diff != "" {
		t.Errorf("usage total tokens mismatch (-want +got):\n%s", diff)
	}

	// Artifacts should still be written for post-mortem.
	stageDir := filepath.Join(logsRoot, "stuck_stage")
	if _, err := os.Stat(filepath.Join(stageDir, "prompt.md")); err != nil {
		t.Errorf("prompt.md should be written even on exhaustion: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stageDir, "response.md")); err != nil {
		t.Errorf("response.md should be written even on exhaustion: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stageDir, "status.json")); err != nil {
		t.Errorf("status.json should be written even on exhaustion: %v", err)
	}

	// Verify status.json has the correct outcome.
	statusData, err := os.ReadFile(filepath.Join(stageDir, "status.json"))
	if err != nil {
		t.Fatalf("reading status.json: %v", err)
	}
	var sj statusJSON
	if err := json.Unmarshal(statusData, &sj); err != nil {
		t.Fatalf("parsing status.json: %v", err)
	}
	if diff := cmp.Diff("fail", sj.Outcome); diff != "" {
		t.Errorf("status.json outcome mismatch (-want +got):\n%s", diff)
	}
}

type usageBackend struct{}

func (b usageBackend) Run(node *dot.Node, _ string, _ *Context) (BackendResult, error) {
	return BackendResult{
		Response: "generated code for " + node.ID,
		Usage:    llm.Usage{InputTokens: 5000, OutputTokens: 1200, TotalTokens: 6200},
		Model:    "anthropic/claude-opus-4.6",
		Rounds:   7,
	}, nil
}

func TestCodergenHandler_WritesUsageJSON(t *testing.T) {
	logsRoot := t.TempDir()
	h := CodergenHandler{Backend: usageBackend{}}
	node := &dot.Node{ID: "gen", Attrs: map[string]string{
		"shape":  "box",
		"prompt": "Generate code",
	}}
	g := &dot.Graph{Attrs: map[string]string{}}

	out := h.Execute(node, NewContext(), g, logsRoot)

	if diff := cmp.Diff(StatusSuccess, out.Status); diff != "" {
		t.Fatalf("status mismatch (-want +got):\n%s", diff)
	}

	// Verify usage.json was written.
	usagePath := filepath.Join(logsRoot, "gen", "usage.json")
	data, err := os.ReadFile(usagePath)
	if err != nil {
		t.Fatalf("usage.json not written: %v", err)
	}

	var su StageUsage
	if err := json.Unmarshal(data, &su); err != nil {
		t.Fatalf("invalid usage.json: %v", err)
	}

	if diff := cmp.Diff("anthropic/claude-opus-4.6", su.Model); diff != "" {
		t.Errorf("model mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(7, su.Rounds); diff != "" {
		t.Errorf("rounds mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(5000, su.InputTokens); diff != "" {
		t.Errorf("input_tokens mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(1200, su.OutputTokens); diff != "" {
		t.Errorf("output_tokens mismatch (-want +got):\n%s", diff)
	}

	// Verify Outcome.Usage is populated.
	if out.Usage == nil {
		t.Fatal("expected Outcome.Usage to be non-nil")
	}
	if diff := cmp.Diff(6200, out.Usage.TotalTokens); diff != "" {
		t.Errorf("outcome usage total mismatch (-want +got):\n%s", diff)
	}
}

func TestCodergenHandler_SimulatedMode_NoUsageJSON(t *testing.T) {
	logsRoot := t.TempDir()
	h := CodergenHandler{Backend: nil}
	node := &dot.Node{ID: "sim", Attrs: map[string]string{
		"shape":  "box",
		"prompt": "Simulate",
	}}
	g := &dot.Graph{Attrs: map[string]string{}}

	out := h.Execute(node, NewContext(), g, logsRoot)

	if out.Usage != nil {
		t.Error("expected nil Usage for simulated (nil backend) handler")
	}
	usagePath := filepath.Join(logsRoot, "sim", "usage.json")
	if _, err := os.Stat(usagePath); err == nil {
		t.Error("usage.json should not be written when backend is nil")
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

// --- Build gate tests ---

func TestCodergenHandler_BuildGatePass(t *testing.T) {
	logsRoot := t.TempDir()
	h := CodergenHandler{
		Backend: SimulatedBackend{},
		CheckRunner: func(cmd string) (string, error) {
			return "", nil // check passes
		},
	}
	node := &dot.Node{ID: "build_ok", Attrs: map[string]string{
		"shape":     "box",
		"prompt":    "Implement something",
		"check_cmd": "go build ./...",
	}}
	g := &dot.Graph{Attrs: map[string]string{}}

	out := h.Execute(node, NewContext(), g, logsRoot)

	if diff := cmp.Diff(StatusSuccess, out.Status); diff != "" {
		t.Errorf("status mismatch (-want +got):\n%s", diff)
	}
}

// buildGateBackend tracks how many times Run is called and fails the check
// on the first N attempts.
type buildGateBackend struct {
	calls int
}

func (b *buildGateBackend) Run(node *dot.Node, prompt string, _ *Context) (BackendResult, error) {
	b.calls++
	return BackendResult{
		Response: fmt.Sprintf("attempt %d for %s", b.calls, node.ID),
		Usage:    llm.Usage{InputTokens: 1000, OutputTokens: 200, TotalTokens: 1200},
		Model:    "test-model",
		Rounds:   3,
	}, nil
}

func TestCodergenHandler_BuildGateFailThenFix(t *testing.T) {
	logsRoot := t.TempDir()
	checkAttempts := 0
	backend := &buildGateBackend{}
	h := CodergenHandler{
		Backend: backend,
		CheckRunner: func(cmd string) (string, error) {
			checkAttempts++
			if checkAttempts == 1 {
				return "internal/db/queries.go:15: undefined: models.Team", fmt.Errorf("exit status 1")
			}
			return "", nil // passes on second check
		},
	}
	node := &dot.Node{ID: "fixable", Attrs: map[string]string{
		"shape":     "box",
		"prompt":    "Build the DB layer",
		"check_cmd": "go build ./...",
	}}
	g := &dot.Graph{Attrs: map[string]string{}}

	out := h.Execute(node, NewContext(), g, logsRoot)

	if diff := cmp.Diff(StatusSuccess, out.Status); diff != "" {
		t.Errorf("status mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(2, backend.calls); diff != "" {
		t.Errorf("backend should be called twice (initial + fix) (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(2, checkAttempts); diff != "" {
		t.Errorf("check should run twice (-want +got):\n%s", diff)
	}
	// Usage should be accumulated from both backend calls.
	if out.Usage == nil {
		t.Fatal("expected usage to be recorded")
	}
	if diff := cmp.Diff(2400, out.Usage.TotalTokens); diff != "" {
		t.Errorf("total tokens should be sum of both calls (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(6, out.Usage.Rounds); diff != "" {
		t.Errorf("rounds should be sum of both calls (-want +got):\n%s", diff)
	}

	// Build gate error log should exist.
	errorFile := filepath.Join(logsRoot, "fixable", "buildgate_attempt_1.txt")
	data, err := os.ReadFile(errorFile)
	if err != nil {
		t.Fatalf("buildgate_attempt_1.txt should be written: %v", err)
	}
	if !strings.Contains(string(data), "undefined: models.Team") {
		t.Errorf("error file should contain build error, got %q", string(data))
	}
}

func TestCodergenHandler_BuildGateExhausted(t *testing.T) {
	logsRoot := t.TempDir()
	backend := &buildGateBackend{}
	h := CodergenHandler{
		Backend: backend,
		CheckRunner: func(cmd string) (string, error) {
			return "always fails: undefined: foo", fmt.Errorf("exit status 1")
		},
	}
	node := &dot.Node{ID: "unfixable", Attrs: map[string]string{
		"shape":              "box",
		"prompt":             "Build something",
		"check_cmd":          "go build ./...",
		"check_max_retries":  "2",
	}}
	g := &dot.Graph{Attrs: map[string]string{}}

	out := h.Execute(node, NewContext(), g, logsRoot)

	if diff := cmp.Diff(StatusFail, out.Status); diff != "" {
		t.Errorf("status mismatch (-want +got):\n%s", diff)
	}
	if !strings.Contains(out.FailureReason, "build gate failed") {
		t.Errorf("failure reason should mention build gate, got %q", out.FailureReason)
	}
	// Initial call + 1 fix attempt (2 check attempts, fails on 2nd = max retries exhausted).
	// check_max_retries=2 means 2 check attempts total.
	if diff := cmp.Diff(2, backend.calls); diff != "" {
		t.Errorf("backend call count mismatch (-want +got):\n%s", diff)
	}
}

func TestCodergenHandler_NoCheckCmd(t *testing.T) {
	logsRoot := t.TempDir()
	checkCalled := false
	h := CodergenHandler{
		Backend: SimulatedBackend{},
		CheckRunner: func(cmd string) (string, error) {
			checkCalled = true
			return "", nil
		},
	}
	node := &dot.Node{ID: "no_gate", Attrs: map[string]string{
		"shape":  "box",
		"prompt": "No build gate here",
	}}
	g := &dot.Graph{Attrs: map[string]string{}}

	out := h.Execute(node, NewContext(), g, logsRoot)

	if diff := cmp.Diff(StatusSuccess, out.Status); diff != "" {
		t.Errorf("status mismatch (-want +got):\n%s", diff)
	}
	if checkCalled {
		t.Error("CheckRunner should not be called when node has no check_cmd")
	}
}
