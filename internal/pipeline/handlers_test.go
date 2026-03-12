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
	rounds           int
	exhaustionReason string
}

func (b exhaustedBackend) Run(node *dot.Node, _ string, _ *Context) (BackendResult, error) {
	reason := b.exhaustionReason
	if reason == "" {
		reason = ExhaustionRoundLimit
	}
	return BackendResult{
		Response:         "Let me read the handler files...",
		Usage:            llm.Usage{InputTokens: 50000, OutputTokens: 8000, TotalTokens: 58000},
		Model:            "anthropic/claude-opus-4.6",
		Rounds:           b.rounds,
		Exhausted:        true,
		ExhaustionReason: reason,
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

func TestCodergenHandler_ReadLoopExhaustion(t *testing.T) {
	logsRoot := t.TempDir()
	h := CodergenHandler{Backend: exhaustedBackend{rounds: 10, exhaustionReason: ExhaustionReadLoop}}
	node := &dot.Node{ID: "read_loop_stage", Attrs: map[string]string{
		"shape":  "box",
		"prompt": "Analyze the codebase",
	}}
	g := &dot.Graph{Attrs: map[string]string{}}

	out := h.Execute(node, NewContext(), g, logsRoot)

	if diff := cmp.Diff(StatusFail, out.Status); diff != "" {
		t.Errorf("status mismatch (-want +got):\n%s", diff)
	}
	if !strings.Contains(out.FailureReason, "read-loop") {
		t.Errorf("expected failure reason to mention read-loop, got %q", out.FailureReason)
	}
	if !strings.Contains(out.FailureReason, "10") {
		t.Errorf("expected failure reason to include round count, got %q", out.FailureReason)
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

// buildGateBackend tracks how many times Run is called and captures prompts.
type buildGateBackend struct {
	calls   int
	prompts []string
}

func (b *buildGateBackend) Run(node *dot.Node, prompt string, _ *Context) (BackendResult, error) {
	b.calls++
	b.prompts = append(b.prompts, prompt)
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

	// Retry prompt should contain the scratch hint.
	if len(backend.prompts) < 2 {
		t.Fatal("expected at least 2 prompts (initial + retry)")
	}
	retryPrompt := backend.prompts[1]
	if !strings.Contains(retryPrompt, "_scratch/") {
		t.Error("retry prompt should contain scratch directory hint")
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

// --- Context carryover tests ---

func TestExtractFileList(t *testing.T) {
	tests := []struct {
		name         string
		conversation []llm.Message
		want         []string
	}{
		{
			name:         "empty conversation",
			conversation: nil,
			want:         nil,
		},
		{
			name: "no write or edit calls",
			conversation: []llm.Message{
				llm.SystemMessage("system"),
				llm.UserMessage("prompt"),
				{
					Role: llm.RoleAssistant,
					Parts: []llm.ContentPart{{
						Kind: llm.KindToolCall,
						ToolCall: &llm.ToolCall{
							ID: "c1", Name: "read_file",
							Arguments: json.RawMessage(`{"path":"main.go"}`),
						},
					}},
				},
			},
			want: nil,
		},
		{
			name: "write and edit calls extracted",
			conversation: []llm.Message{
				llm.SystemMessage("system"),
				{
					Role: llm.RoleAssistant,
					Parts: []llm.ContentPart{{
						Kind: llm.KindToolCall,
						ToolCall: &llm.ToolCall{
							ID: "c1", Name: "write_file",
							Arguments: json.RawMessage(`{"path":"go.mod","content":"module x"}`),
						},
					}},
				},
				llm.ToolResultMessage("c1", "wrote go.mod", false),
				{
					Role: llm.RoleAssistant,
					Parts: []llm.ContentPart{{
						Kind: llm.KindToolCall,
						ToolCall: &llm.ToolCall{
							ID: "c2", Name: "edit_file",
							Arguments: json.RawMessage(`{"path":"main.go","old_string":"a","new_string":"b"}`),
						},
					}},
				},
				llm.ToolResultMessage("c2", "edited main.go", false),
				{
					Role: llm.RoleAssistant,
					Parts: []llm.ContentPart{{
						Kind: llm.KindToolCall,
						ToolCall: &llm.ToolCall{
							ID: "c3", Name: "write_file",
							Arguments: json.RawMessage(`{"path":"internal/db/repo.go","content":"package db"}`),
						},
					}},
				},
			},
			want: []string{"go.mod", "main.go", "internal/db/repo.go"},
		},
		{
			name: "duplicate paths are deduplicated",
			conversation: []llm.Message{
				{
					Role: llm.RoleAssistant,
					Parts: []llm.ContentPart{{
						Kind: llm.KindToolCall,
						ToolCall: &llm.ToolCall{
							ID: "c1", Name: "write_file",
							Arguments: json.RawMessage(`{"path":"main.go","content":"v1"}`),
						},
					}},
				},
				{
					Role: llm.RoleAssistant,
					Parts: []llm.ContentPart{{
						Kind: llm.KindToolCall,
						ToolCall: &llm.ToolCall{
							ID: "c2", Name: "edit_file",
							Arguments: json.RawMessage(`{"path":"main.go","old_string":"v1","new_string":"v2"}`),
						},
					}},
				},
			},
			want: []string{"main.go"},
		},
		{
			name: "invalid JSON args are skipped",
			conversation: []llm.Message{
				{
					Role: llm.RoleAssistant,
					Parts: []llm.ContentPart{{
						Kind: llm.KindToolCall,
						ToolCall: &llm.ToolCall{
							ID: "c1", Name: "write_file",
							Arguments: json.RawMessage(`{invalid`),
						},
					}},
				},
			},
			want: nil,
		},
		{
			name: "scratch paths are excluded",
			conversation: []llm.Message{
				{
					Role: llm.RoleAssistant,
					Parts: []llm.ContentPart{{
						Kind: llm.KindToolCall,
						ToolCall: &llm.ToolCall{
							ID: "c1", Name: "write_file",
							Arguments: json.RawMessage(`{"path":"main.go","content":"package main"}`),
						},
					}},
				},
				{
					Role: llm.RoleAssistant,
					Parts: []llm.ContentPart{{
						Kind: llm.KindToolCall,
						ToolCall: &llm.ToolCall{
							ID: "c2", Name: "write_file",
							Arguments: json.RawMessage(`{"path":"_scratch/notes.md","content":"notes"}`),
						},
					}},
				},
				{
					Role: llm.RoleAssistant,
					Parts: []llm.ContentPart{{
						Kind: llm.KindToolCall,
						ToolCall: &llm.ToolCall{
							ID: "c3", Name: "write_file",
							Arguments: json.RawMessage(`{"path":"_scratch/SUMMARY.md","content":"summary"}`),
						},
					}},
				},
				{
					Role: llm.RoleAssistant,
					Parts: []llm.ContentPart{{
						Kind: llm.KindToolCall,
						ToolCall: &llm.ToolCall{
							ID: "c4", Name: "write_file",
							Arguments: json.RawMessage(`{"path":"/work/project/_scratch/plan.md","content":"plan"}`),
						},
					}},
				},
				{
					Role: llm.RoleAssistant,
					Parts: []llm.ContentPart{{
						Kind: llm.KindToolCall,
						ToolCall: &llm.ToolCall{
							ID: "c5", Name: "write_file",
							Arguments: json.RawMessage(`{"path":"internal/server.go","content":"package internal"}`),
						},
					}},
				},
			},
			want: []string{"main.go", "internal/server.go"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := extractFileList(tc.conversation)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("extractFileList mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestIsScratchPath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"_scratch/notes.md", true},
		{"_scratch/SUMMARY.md", true},
		{"_scratch/prior/analyze_summary.md", true},
		{"/work/project/_scratch/plan.md", true},
		{"main.go", false},
		{"internal/db/repo.go", false},
		{"scratch/notes.md", false},
		{"my_scratch_file.go", false},
		{"_scratch", true},
		{"../_scratch/foo", true},
		{"", false},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			got := isScratchPath(tc.path)
			if got != tc.want {
				t.Errorf("isScratchPath(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestBuildStageSummary(t *testing.T) {
	tests := []struct {
		name           string
		nodeID         string
		files          []string
		response       string
		scratchSummary string
		checks         func(t *testing.T, got string)
	}{
		{
			name:     "with files and response",
			nodeID:   "design",
			files:    []string{"go.mod", "internal/models/models.go"},
			response: "I designed the contract interfaces.",
			checks: func(t *testing.T, got string) {
				if !strings.HasPrefix(got, "[Stage: design] completed.") {
					t.Errorf("missing header, got: %s", got)
				}
				if !strings.Contains(got, "go.mod, internal/models/models.go") {
					t.Error("missing file list")
				}
				if !strings.Contains(got, "I designed the contract interfaces.") {
					t.Error("missing response summary")
				}
			},
		},
		{
			name:     "no files",
			nodeID:   "analyze",
			files:    nil,
			response: "Analyzed the codebase.",
			checks: func(t *testing.T, got string) {
				if !strings.HasPrefix(got, "[Stage: analyze] completed.") {
					t.Errorf("missing header, got: %s", got)
				}
				if strings.Contains(got, "Files created") {
					t.Error("should not contain file list when no files")
				}
				if !strings.Contains(got, "Analyzed the codebase.") {
					t.Error("missing response summary")
				}
			},
		},
		{
			name:     "long response is truncated",
			nodeID:   "verbose",
			files:    []string{"a.go"},
			response: strings.Repeat("x", 500),
			checks: func(t *testing.T, got string) {
				if !strings.Contains(got, "...") {
					t.Error("long response should be truncated with ...")
				}
			},
		},
		{
			name:     "empty response",
			nodeID:   "quiet",
			files:    []string{"b.go"},
			response: "",
			checks: func(t *testing.T, got string) {
				if strings.Contains(got, "Summary:") {
					t.Error("should not contain Summary: when response is empty")
				}
			},
		},
		{
			name:           "with scratch summary",
			nodeID:         "analyze",
			files:          []string{"FEATURE_SPEC.md"},
			response:       "Done.",
			scratchSummary: "Identified 5 core entities and 3 API endpoints.",
			checks: func(t *testing.T, got string) {
				if !strings.Contains(got, "Stage notes: Identified 5 core entities") {
					t.Error("missing scratch summary in output")
				}
			},
		},
		{
			name:           "long scratch summary is truncated",
			nodeID:         "analyze",
			files:          nil,
			response:       "Done.",
			scratchSummary: strings.Repeat("note ", 300),
			checks: func(t *testing.T, got string) {
				if !strings.Contains(got, "Stage notes:") {
					t.Error("missing Stage notes label")
				}
				if !strings.Contains(got, "...") {
					t.Error("long scratch summary should be truncated")
				}
			},
		},
		{
			name:           "empty scratch summary omitted",
			nodeID:         "design",
			files:          []string{"a.go"},
			response:       "Done.",
			scratchSummary: "",
			checks: func(t *testing.T, got string) {
				if strings.Contains(got, "Stage notes:") {
					t.Error("should not contain Stage notes when scratch summary is empty")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := buildStageSummary(tc.nodeID, tc.files, tc.response, tc.scratchSummary)
			tc.checks(t, got)
		})
	}
}

func TestBuildPriorContext(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(ctx *Context)
		checks func(t *testing.T, got string)
	}{
		{
			name:  "no completed stages",
			setup: func(ctx *Context) {},
			checks: func(t *testing.T, got string) {
				if got != "" {
					t.Errorf("expected empty string, got %q", got)
				}
			},
		},
		{
			name: "one completed stage",
			setup: func(ctx *Context) {
				ctx.Set("completed_stages", "design")
				ctx.Set("stage_summary:design", "[Stage: design] completed.\nFiles created/modified: go.mod")
			},
			checks: func(t *testing.T, got string) {
				if !strings.HasPrefix(got, "=== PRIOR STAGE CONTEXT ===") {
					t.Error("missing header")
				}
				if !strings.Contains(got, "[Stage: design]") {
					t.Error("missing design summary")
				}
				if !strings.HasSuffix(got, "=== END PRIOR STAGE CONTEXT ===\n\n") {
					t.Error("missing footer")
				}
			},
		},
		{
			name: "multiple completed stages in order",
			setup: func(ctx *Context) {
				ctx.Set("completed_stages", "analyze,design,scaffold")
				ctx.Set("stage_summary:analyze", "[Stage: analyze] completed.")
				ctx.Set("stage_summary:design", "[Stage: design] completed.")
				ctx.Set("stage_summary:scaffold", "[Stage: scaffold] completed.")
			},
			checks: func(t *testing.T, got string) {
				analyzeIdx := strings.Index(got, "[Stage: analyze]")
				designIdx := strings.Index(got, "[Stage: design]")
				scaffoldIdx := strings.Index(got, "[Stage: scaffold]")
				if analyzeIdx < 0 || designIdx < 0 || scaffoldIdx < 0 {
					t.Fatalf("missing stage summaries in output: %s", got)
				}
				if analyzeIdx > designIdx || designIdx > scaffoldIdx {
					t.Error("stages should appear in order")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := NewContext()
			tc.setup(ctx)
			got := buildPriorContext(ctx)
			tc.checks(t, got)
		})
	}
}

func TestExpandVariables_PriorContext(t *testing.T) {
	tests := []struct {
		name   string
		prompt string
		setup  func(ctx *Context)
		checks func(t *testing.T, got string)
	}{
		{
			name:   "no prior context and no variable",
			prompt: "Do the thing for $goal",
			setup:  func(ctx *Context) {},
			checks: func(t *testing.T, got string) {
				if diff := cmp.Diff("Do the thing for build an app", got); diff != "" {
					t.Errorf("mismatch (-want +got):\n%s", diff)
				}
			},
		},
		{
			name:   "auto-prepend when no variable present",
			prompt: "Implement the DB layer",
			setup: func(ctx *Context) {
				ctx.Set("completed_stages", "design")
				ctx.Set("stage_summary:design", "[Stage: design] completed.\nFiles: go.mod")
			},
			checks: func(t *testing.T, got string) {
				if !strings.HasPrefix(got, "=== PRIOR STAGE CONTEXT ===") {
					t.Error("prior context should be prepended")
				}
				if !strings.Contains(got, "Implement the DB layer") {
					t.Error("original prompt should follow the context block")
				}
			},
		},
		{
			name:   "explicit $prior_context placement",
			prompt: "First the context:\n$prior_context\nNow implement it.",
			setup: func(ctx *Context) {
				ctx.Set("completed_stages", "design")
				ctx.Set("stage_summary:design", "[Stage: design] completed.")
			},
			checks: func(t *testing.T, got string) {
				if strings.HasPrefix(got, "=== PRIOR STAGE CONTEXT ===") {
					t.Error("should not be prepended when $prior_context is present")
				}
				if !strings.Contains(got, "First the context:") {
					t.Error("text before $prior_context should be preserved")
				}
				if !strings.Contains(got, "[Stage: design]") {
					t.Error("prior context should be inserted at $prior_context location")
				}
				if !strings.Contains(got, "Now implement it.") {
					t.Error("text after $prior_context should be preserved")
				}
			},
		},
		{
			name:   "no $prior_context variable and no summaries does not add block",
			prompt: "Just do it",
			setup:  func(ctx *Context) {},
			checks: func(t *testing.T, got string) {
				if diff := cmp.Diff("Just do it", got); diff != "" {
					t.Errorf("mismatch (-want +got):\n%s", diff)
				}
			},
		},
	}

	g := &dot.Graph{Attrs: map[string]string{"goal": "build an app"}}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := NewContext()
			tc.setup(ctx)
			got := expandVariables(tc.prompt, g, ctx)
			tc.checks(t, got)
		})
	}
}

// readOnlyConversationBackend returns a conversation with only read_file calls
// and no file writes — used to test empty stage detection.
type readOnlyConversationBackend struct{}

func (b readOnlyConversationBackend) Run(node *dot.Node, _ string, _ *Context) (BackendResult, error) {
	msgs := []llm.Message{
		{
			Role: llm.RoleAssistant,
			Parts: []llm.ContentPart{{
				Kind: llm.KindToolCall,
				ToolCall: &llm.ToolCall{
					ID:        "call_1",
					Name:      "read_file",
					Arguments: json.RawMessage(`{"path":"main.go"}`),
				},
			}},
		},
		llm.ToolResultMessage("call_1", "package main", false),
	}
	return BackendResult{
		Response:     "I analyzed the code but didn't write anything.",
		Usage:        llm.Usage{InputTokens: 1000, OutputTokens: 200, TotalTokens: 1200},
		Model:        "test-model",
		Rounds:       2,
		Conversation: msgs,
	}, nil
}

func TestCodergenHandler_EmptyOutputWarning(t *testing.T) {
	logsRoot := t.TempDir()
	h := CodergenHandler{Backend: readOnlyConversationBackend{}}
	node := &dot.Node{ID: "empty_stage", Attrs: map[string]string{
		"shape":  "box",
		"prompt": "Analyze the code",
	}}
	g := &dot.Graph{Attrs: map[string]string{}}

	out := h.Execute(node, NewContext(), g, logsRoot)

	// Stage should still succeed — empty output is a warning, not a failure.
	if diff := cmp.Diff(StatusSuccess, out.Status); diff != "" {
		t.Errorf("status mismatch (-want +got):\n%s", diff)
	}
}

func TestCodergenHandler_EmptyOutputSuppressed(t *testing.T) {
	logsRoot := t.TempDir()
	h := CodergenHandler{Backend: readOnlyConversationBackend{}}
	node := &dot.Node{ID: "text_only", Attrs: map[string]string{
		"shape":              "box",
		"prompt":             "Just analyze",
		"allow_empty_output": "true",
	}}
	g := &dot.Graph{Attrs: map[string]string{}}

	out := h.Execute(node, NewContext(), g, logsRoot)

	// Should succeed without warning (allow_empty_output suppresses it).
	if diff := cmp.Diff(StatusSuccess, out.Status); diff != "" {
		t.Errorf("status mismatch (-want +got):\n%s", diff)
	}
}

func TestCodergenHandler_NonEmptyOutputNoWarning(t *testing.T) {
	logsRoot := t.TempDir()
	h := CodergenHandler{Backend: conversationBackend{files: []string{"main.go"}}}
	node := &dot.Node{ID: "productive", Attrs: map[string]string{
		"shape":  "box",
		"prompt": "Build something",
	}}
	g := &dot.Graph{Attrs: map[string]string{}}

	out := h.Execute(node, NewContext(), g, logsRoot)

	if diff := cmp.Diff(StatusSuccess, out.Status); diff != "" {
		t.Errorf("status mismatch (-want +got):\n%s", diff)
	}
}

// conversationBackend returns a BackendResult with a pre-built conversation
// containing write_file and edit_file tool calls.
type conversationBackend struct {
	files []string
}

func (b conversationBackend) Run(node *dot.Node, _ string, _ *Context) (BackendResult, error) {
	var msgs []llm.Message
	for i, path := range b.files {
		callID := fmt.Sprintf("call_%d", i)
		msgs = append(msgs, llm.Message{
			Role: llm.RoleAssistant,
			Parts: []llm.ContentPart{{
				Kind: llm.KindToolCall,
				ToolCall: &llm.ToolCall{
					ID:        callID,
					Name:      "write_file",
					Arguments: json.RawMessage(fmt.Sprintf(`{"path":%q,"content":"package x"}`, path)),
				},
			}},
		})
		msgs = append(msgs, llm.ToolResultMessage(callID, "wrote "+path, false))
	}
	return BackendResult{
		Response:     "completed stage " + node.ID,
		Usage:        llm.Usage{InputTokens: 1000, OutputTokens: 200, TotalTokens: 1200},
		Model:        "test-model",
		Rounds:       3,
		Conversation: msgs,
	}, nil
}

// --- Filesystem observation tests ---

// fileWritingBackend simulates an agent that creates real files on disk.
type fileWritingBackend struct {
	workDir string
	files   map[string]string // relative path -> content
}

func (b fileWritingBackend) Run(node *dot.Node, _ string, _ *Context) (BackendResult, error) {
	var msgs []llm.Message
	i := 0
	for relPath, content := range b.files {
		fullPath := filepath.Join(b.workDir, relPath)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			return BackendResult{}, err
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			return BackendResult{}, err
		}
		callID := fmt.Sprintf("call_%d", i)
		msgs = append(msgs, llm.Message{
			Role: llm.RoleAssistant,
			Parts: []llm.ContentPart{{
				Kind: llm.KindToolCall,
				ToolCall: &llm.ToolCall{
					ID:        callID,
					Name:      "write_file",
					Arguments: json.RawMessage(fmt.Sprintf(`{"path":%q,"content":%q}`, relPath, content)),
				},
			}},
		})
		i++
	}
	return BackendResult{
		Response:     "wrote files for " + node.ID,
		Usage:        llm.Usage{InputTokens: 1000, OutputTokens: 200, TotalTokens: 1200},
		Model:        "test-model",
		Rounds:       2,
		Conversation: msgs,
	}, nil
}

func TestCodergenHandler_FilesystemDiffWritten(t *testing.T) {
	logsRoot := t.TempDir()
	workDir := t.TempDir()

	// Pre-existing file in work dir.
	writeFile(t, workDir, "existing.go", "package main")

	backend := fileWritingBackend{
		workDir: workDir,
		files: map[string]string{
			"new.go":          "package new",
			"internal/api.go": "package internal",
		},
	}
	h := CodergenHandler{Backend: backend, WorkDir: workDir}
	node := &dot.Node{ID: "scaffold", Attrs: map[string]string{
		"shape":  "box",
		"prompt": "Scaffold the project",
	}}
	g := &dot.Graph{Attrs: map[string]string{}}

	out := h.Execute(node, NewContext(), g, logsRoot)
	if diff := cmp.Diff(StatusSuccess, out.Status); diff != "" {
		t.Fatalf("status mismatch (-want +got):\n%s", diff)
	}

	// Verify filesystem_diff.txt was written.
	diffPath := filepath.Join(logsRoot, "scaffold", "filesystem_diff.txt")
	diffData, err := os.ReadFile(diffPath)
	if err != nil {
		t.Fatalf("filesystem_diff.txt not written: %v", err)
	}
	diffStr := string(diffData)
	if !strings.Contains(diffStr, "new.go") {
		t.Errorf("diff should mention new.go, got: %s", diffStr)
	}
	if !strings.Contains(diffStr, "internal/api.go") {
		t.Errorf("diff should mention internal/api.go, got: %s", diffStr)
	}
	if !strings.Contains(diffStr, "Added") {
		t.Error("diff should show Added section")
	}
}

func TestCodergenHandler_FilesystemDiffEmpty(t *testing.T) {
	logsRoot := t.TempDir()
	workDir := t.TempDir()

	// Backend that doesn't write any real files.
	h := CodergenHandler{Backend: readOnlyConversationBackend{}, WorkDir: workDir}
	node := &dot.Node{ID: "analyze", Attrs: map[string]string{
		"shape":  "box",
		"prompt": "Analyze",
	}}
	g := &dot.Graph{Attrs: map[string]string{}}

	out := h.Execute(node, NewContext(), g, logsRoot)
	if diff := cmp.Diff(StatusSuccess, out.Status); diff != "" {
		t.Fatalf("status mismatch (-want +got):\n%s", diff)
	}

	diffPath := filepath.Join(logsRoot, "analyze", "filesystem_diff.txt")
	diffData, err := os.ReadFile(diffPath)
	if err != nil {
		t.Fatalf("filesystem_diff.txt not written: %v", err)
	}
	if diff := cmp.Diff("(no filesystem changes)", string(diffData)); diff != "" {
		t.Errorf("empty diff mismatch (-want +got):\n%s", diff)
	}
}

func TestCodergenHandler_SimulateMode_NoSnapshot(t *testing.T) {
	logsRoot := t.TempDir()

	// WorkDir is empty in simulate mode — no snapshots should be taken.
	h := CodergenHandler{Backend: nil, WorkDir: ""}
	node := &dot.Node{ID: "sim_stage", Attrs: map[string]string{
		"shape":  "box",
		"prompt": "Simulate",
	}}
	g := &dot.Graph{Attrs: map[string]string{}}

	out := h.Execute(node, NewContext(), g, logsRoot)
	if diff := cmp.Diff(StatusSuccess, out.Status); diff != "" {
		t.Fatalf("status mismatch (-want +got):\n%s", diff)
	}

	diffPath := filepath.Join(logsRoot, "sim_stage", "filesystem_diff.txt")
	if _, err := os.Stat(diffPath); err == nil {
		t.Error("filesystem_diff.txt should not be written in simulate mode")
	}
}

func TestCodergenHandler_EmptyConversationButFSChanged(t *testing.T) {
	logsRoot := t.TempDir()
	workDir := t.TempDir()

	// Backend writes files to disk but returns no write_file tool calls in
	// the conversation. This tests the case where filesystem observation
	// catches changes the conversation-based detection misses.
	backend := &diskOnlyBackend{workDir: workDir}
	h := CodergenHandler{Backend: backend, WorkDir: workDir}
	node := &dot.Node{ID: "sneaky", Attrs: map[string]string{
		"shape":  "box",
		"prompt": "Do work",
	}}
	g := &dot.Graph{Attrs: map[string]string{}}

	out := h.Execute(node, NewContext(), g, logsRoot)
	if diff := cmp.Diff(StatusSuccess, out.Status); diff != "" {
		t.Fatalf("status mismatch (-want +got):\n%s", diff)
	}

	// The filesystem diff should show changes even though conversation is empty.
	diffPath := filepath.Join(logsRoot, "sneaky", "filesystem_diff.txt")
	diffData, err := os.ReadFile(diffPath)
	if err != nil {
		t.Fatalf("filesystem_diff.txt not written: %v", err)
	}
	if strings.Contains(string(diffData), "(no filesystem changes)") {
		t.Error("filesystem diff should show changes even though conversation had no write calls")
	}
	if !strings.Contains(string(diffData), "secret.go") {
		t.Errorf("diff should mention secret.go, got: %s", string(diffData))
	}
}

// diskOnlyBackend writes files to disk but returns a conversation with no
// write_file tool calls — simulating unconventional file creation.
type diskOnlyBackend struct {
	workDir string
}

func (b *diskOnlyBackend) Run(node *dot.Node, _ string, _ *Context) (BackendResult, error) {
	// Write a file directly to disk (via shell tool or similar).
	if err := os.WriteFile(filepath.Join(b.workDir, "secret.go"), []byte("package secret"), 0o644); err != nil {
		return BackendResult{}, err
	}
	return BackendResult{
		Response:     "I created files using the shell tool.",
		Usage:        llm.Usage{InputTokens: 500, OutputTokens: 100, TotalTokens: 600},
		Model:        "test-model",
		Rounds:       1,
		Conversation: []llm.Message{}, // no write_file calls
	}, nil
}

func TestCodergenHandler_ContextCarryover(t *testing.T) {
	logsRoot := t.TempDir()
	g := &dot.Graph{Attrs: map[string]string{"goal": "build an app"}}
	ctx := NewContext()

	// Stage 1: design — creates contract files.
	designBackend := conversationBackend{
		files: []string{"go.mod", "internal/models/models.go", "internal/db/repository.go"},
	}
	h1 := CodergenHandler{Backend: designBackend}
	node1 := &dot.Node{ID: "design", Attrs: map[string]string{
		"shape":  "box",
		"prompt": "Design the contracts.",
	}}

	out1 := h1.Execute(node1, ctx, g, logsRoot)
	if diff := cmp.Diff(StatusSuccess, out1.Status); diff != "" {
		t.Fatalf("stage 1 status (-want +got):\n%s", diff)
	}

	// Apply context updates (normally done by the engine).
	ctx.ApplyUpdates(out1.ContextUpdates)

	// Verify context was populated.
	if diff := cmp.Diff("design", ctx.GetString("completed_stages")); diff != "" {
		t.Errorf("completed_stages mismatch (-want +got):\n%s", diff)
	}
	summary := ctx.GetString("stage_summary:design")
	if !strings.Contains(summary, "go.mod") {
		t.Errorf("summary should contain go.mod, got: %s", summary)
	}
	if !strings.Contains(summary, "internal/models/models.go") {
		t.Errorf("summary should contain models.go, got: %s", summary)
	}

	// Stage 2: scaffold — its prompt should receive the design summary.
	scaffoldBackend := conversationBackend{
		files: []string{"cmd/server/main.go", "internal/server/server.go"},
	}
	h2 := CodergenHandler{Backend: scaffoldBackend}
	node2 := &dot.Node{ID: "scaffold", Attrs: map[string]string{
		"shape":  "box",
		"prompt": "Scaffold the project.",
	}}

	out2 := h2.Execute(node2, ctx, g, logsRoot)
	if diff := cmp.Diff(StatusSuccess, out2.Status); diff != "" {
		t.Fatalf("stage 2 status (-want +got):\n%s", diff)
	}

	// Check that the prompt.md for scaffold contains the prior context.
	promptData, err := os.ReadFile(filepath.Join(logsRoot, "scaffold", "prompt.md"))
	if err != nil {
		t.Fatalf("reading scaffold prompt.md: %v", err)
	}
	promptStr := string(promptData)
	if !strings.Contains(promptStr, "=== PRIOR STAGE CONTEXT ===") {
		t.Error("scaffold prompt should contain prior context header")
	}
	if !strings.Contains(promptStr, "[Stage: design]") {
		t.Error("scaffold prompt should contain design stage summary")
	}
	if !strings.Contains(promptStr, "go.mod") {
		t.Error("scaffold prompt should list design stage files")
	}
	if !strings.Contains(promptStr, "Scaffold the project.") {
		t.Error("scaffold prompt should contain the original prompt text")
	}

	// Apply stage 2 context updates.
	ctx.ApplyUpdates(out2.ContextUpdates)

	// Verify completed_stages is accumulated.
	if diff := cmp.Diff("design,scaffold", ctx.GetString("completed_stages")); diff != "" {
		t.Errorf("completed_stages after 2 stages (-want +got):\n%s", diff)
	}
}
