package pipeline

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/campallison/attractor/internal/dot"
	"github.com/campallison/attractor/internal/llm"
	"github.com/google/go-cmp/cmp"
)

// --- Edge selection tests ---

func TestSelectEdge_ConditionMatch(t *testing.T) {
	g := &dot.Graph{
		Attrs: map[string]string{},
		Nodes: []*dot.Node{
			{ID: "gate", Attrs: map[string]string{}},
			{ID: "pass", Attrs: map[string]string{}},
			{ID: "fail_node", Attrs: map[string]string{}},
		},
		Edges: []*dot.Edge{
			{From: "gate", To: "pass", Attrs: map[string]string{"condition": "outcome=success"}},
			{From: "gate", To: "fail_node", Attrs: map[string]string{"condition": "outcome=fail"}},
		},
	}

	tests := []struct {
		name    string
		outcome Outcome
		wantTo  string
	}{
		{
			name:    "success routes to pass",
			outcome: Outcome{Status: StatusSuccess},
			wantTo:  "pass",
		},
		{
			name:    "fail routes to fail_node",
			outcome: Outcome{Status: StatusFail},
			wantTo:  "fail_node",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			edge := SelectEdge("gate", tt.outcome, NewContext(), g)
			if edge == nil {
				t.Fatal("expected an edge, got nil")
			}
			if diff := cmp.Diff(tt.wantTo, edge.To); diff != "" {
				t.Errorf("edge target mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestSelectEdge_PreferredLabel(t *testing.T) {
	g := &dot.Graph{
		Attrs: map[string]string{},
		Nodes: []*dot.Node{
			{ID: "review", Attrs: map[string]string{}},
			{ID: "approve", Attrs: map[string]string{}},
			{ID: "fix", Attrs: map[string]string{}},
		},
		Edges: []*dot.Edge{
			{From: "review", To: "approve", Attrs: map[string]string{"label": "Approve"}},
			{From: "review", To: "fix", Attrs: map[string]string{"label": "Fix"}},
		},
	}

	outcome := Outcome{Status: StatusSuccess, PreferredLabel: "Fix"}
	edge := SelectEdge("review", outcome, NewContext(), g)
	if edge == nil {
		t.Fatal("expected an edge, got nil")
	}
	if diff := cmp.Diff("fix", edge.To); diff != "" {
		t.Errorf("preferred label should select Fix edge (-want +got):\n%s", diff)
	}
}

func TestSelectEdge_SuggestedNextIDs(t *testing.T) {
	g := &dot.Graph{
		Attrs: map[string]string{},
		Nodes: []*dot.Node{
			{ID: "node", Attrs: map[string]string{}},
			{ID: "a", Attrs: map[string]string{}},
			{ID: "b", Attrs: map[string]string{}},
		},
		Edges: []*dot.Edge{
			{From: "node", To: "a", Attrs: map[string]string{}},
			{From: "node", To: "b", Attrs: map[string]string{}},
		},
	}

	outcome := Outcome{Status: StatusSuccess, SuggestedNextIDs: []string{"b"}}
	edge := SelectEdge("node", outcome, NewContext(), g)
	if edge == nil {
		t.Fatal("expected an edge, got nil")
	}
	if diff := cmp.Diff("b", edge.To); diff != "" {
		t.Errorf("suggested next ID should select edge to b (-want +got):\n%s", diff)
	}
}

func TestSelectEdge_WeightTiebreak(t *testing.T) {
	g := &dot.Graph{
		Attrs: map[string]string{},
		Nodes: []*dot.Node{
			{ID: "node", Attrs: map[string]string{}},
			{ID: "low", Attrs: map[string]string{}},
			{ID: "high", Attrs: map[string]string{}},
		},
		Edges: []*dot.Edge{
			{From: "node", To: "low", Attrs: map[string]string{"weight": "1"}},
			{From: "node", To: "high", Attrs: map[string]string{"weight": "10"}},
		},
	}

	edge := SelectEdge("node", Outcome{Status: StatusSuccess}, NewContext(), g)
	if edge == nil {
		t.Fatal("expected an edge, got nil")
	}
	if diff := cmp.Diff("high", edge.To); diff != "" {
		t.Errorf("higher weight should win (-want +got):\n%s", diff)
	}
}

func TestSelectEdge_LexicalTiebreak(t *testing.T) {
	g := &dot.Graph{
		Attrs: map[string]string{},
		Nodes: []*dot.Node{
			{ID: "node", Attrs: map[string]string{}},
			{ID: "beta", Attrs: map[string]string{}},
			{ID: "alpha", Attrs: map[string]string{}},
		},
		Edges: []*dot.Edge{
			{From: "node", To: "beta", Attrs: map[string]string{}},
			{From: "node", To: "alpha", Attrs: map[string]string{}},
		},
	}

	edge := SelectEdge("node", Outcome{Status: StatusSuccess}, NewContext(), g)
	if edge == nil {
		t.Fatal("expected an edge, got nil")
	}
	if diff := cmp.Diff("alpha", edge.To); diff != "" {
		t.Errorf("lexically first should win on equal weight (-want +got):\n%s", diff)
	}
}

func TestSelectEdge_ConditionBeatsWeight(t *testing.T) {
	g := &dot.Graph{
		Attrs: map[string]string{},
		Nodes: []*dot.Node{
			{ID: "node", Attrs: map[string]string{}},
			{ID: "cond", Attrs: map[string]string{}},
			{ID: "heavy", Attrs: map[string]string{}},
		},
		Edges: []*dot.Edge{
			{From: "node", To: "cond", Attrs: map[string]string{"condition": "outcome=success"}},
			{From: "node", To: "heavy", Attrs: map[string]string{"weight": "100"}},
		},
	}

	edge := SelectEdge("node", Outcome{Status: StatusSuccess}, NewContext(), g)
	if edge == nil {
		t.Fatal("expected an edge, got nil")
	}
	if diff := cmp.Diff("cond", edge.To); diff != "" {
		t.Errorf("condition match should beat weight (-want +got):\n%s", diff)
	}
}

func TestSelectEdge_NoEdges(t *testing.T) {
	g := &dot.Graph{
		Attrs: map[string]string{},
		Nodes: []*dot.Node{{ID: "lonely", Attrs: map[string]string{}}},
		Edges: []*dot.Edge{},
	}
	edge := SelectEdge("lonely", Outcome{Status: StatusSuccess}, NewContext(), g)
	if edge != nil {
		t.Error("expected nil for node with no outgoing edges")
	}
}

// --- Label normalization tests ---

func TestNormalizeLabel(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "Approve", want: "approve"},
		{input: "  Fix  ", want: "fix"},
		{input: "[Y] Yes, deploy", want: "yes, deploy"},
		{input: "Y) Yes, deploy", want: "yes, deploy"},
		{input: "Y - Yes, deploy", want: "yes, deploy"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := normalizeLabel(tt.input)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("normalizeLabel mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// --- Full pipeline execution tests ---

func linearGraph() *dot.Graph {
	return &dot.Graph{
		Name:  "Linear",
		Attrs: map[string]string{"goal": "test linear flow"},
		Nodes: []*dot.Node{
			{ID: "start", Attrs: map[string]string{"shape": "Mdiamond"}},
			{ID: "work", Attrs: map[string]string{"shape": "box", "prompt": "do work for $goal"}},
			{ID: "exit", Attrs: map[string]string{"shape": "Msquare"}},
		},
		Edges: []*dot.Edge{
			{From: "start", To: "work", Attrs: map[string]string{}},
			{From: "work", To: "exit", Attrs: map[string]string{}},
		},
	}
}

func TestRun_LinearPipeline(t *testing.T) {
	logsRoot := t.TempDir()
	result, err := Run(context.Background(), RunConfig{
		Graph:    linearGraph(),
		LogsRoot: logsRoot,
		Registry: DefaultHandlerRegistry(CodergenHandler{}),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if diff := cmp.Diff(StatusSuccess, result.Status); diff != "" {
		t.Errorf("status mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"start", "work"}, result.CompletedNodes); diff != "" {
		t.Errorf("completed nodes mismatch (-want +got):\n%s", diff)
	}
}

func TestRun_BranchingPipeline(t *testing.T) {
	g := &dot.Graph{
		Name:  "Branch",
		Attrs: map[string]string{"goal": "branch test"},
		Nodes: []*dot.Node{
			{ID: "start", Attrs: map[string]string{"shape": "Mdiamond"}},
			{ID: "work", Attrs: map[string]string{"shape": "box", "prompt": "do it"}},
			{ID: "gate", Attrs: map[string]string{"shape": "diamond"}},
			{ID: "exit", Attrs: map[string]string{"shape": "Msquare"}},
		},
		Edges: []*dot.Edge{
			{From: "start", To: "work", Attrs: map[string]string{}},
			{From: "work", To: "gate", Attrs: map[string]string{}},
			{From: "gate", To: "exit", Attrs: map[string]string{"condition": "outcome=success"}},
		},
	}

	logsRoot := t.TempDir()
	result, err := Run(context.Background(), RunConfig{
		Graph:    g,
		LogsRoot: logsRoot,
		Registry: DefaultHandlerRegistry(CodergenHandler{}),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if diff := cmp.Diff(StatusSuccess, result.Status); diff != "" {
		t.Errorf("status mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"start", "work", "gate"}, result.CompletedNodes); diff != "" {
		t.Errorf("completed nodes mismatch (-want +got):\n%s", diff)
	}
}

func TestRun_ContextFlows(t *testing.T) {
	g := linearGraph()
	logsRoot := t.TempDir()

	result, err := Run(context.Background(), RunConfig{
		Graph:    g,
		LogsRoot: logsRoot,
		Registry: DefaultHandlerRegistry(CodergenHandler{}),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	workOutcome, ok := result.NodeOutcomes["work"]
	if !ok {
		t.Fatal("work outcome not found")
	}
	if diff := cmp.Diff("work", workOutcome.ContextUpdates["last_stage"]); diff != "" {
		t.Errorf("context update last_stage mismatch (-want +got):\n%s", diff)
	}
}

func TestRun_GoalGateBlocks(t *testing.T) {
	g := &dot.Graph{
		Name:  "GoalGate",
		Attrs: map[string]string{},
		Nodes: []*dot.Node{
			{ID: "start", Attrs: map[string]string{"shape": "Mdiamond"}},
			{ID: "critical", Attrs: map[string]string{"shape": "box", "prompt": "fail", "goal_gate": "true"}},
			{ID: "exit", Attrs: map[string]string{"shape": "Msquare"}},
		},
		Edges: []*dot.Edge{
			{From: "start", To: "critical", Attrs: map[string]string{}},
			{From: "critical", To: "exit", Attrs: map[string]string{}},
		},
	}

	registry := NewHandlerRegistry(CodergenHandler{Backend: nil})
	registry.Register("start", StartHandler{})
	registry.Register("exit", ExitHandler{})
	registry.Register("codergen", CodergenHandler{Backend: failingBackend{msg: "broken"}})

	logsRoot := t.TempDir()
	result, err := Run(context.Background(), RunConfig{
		Graph:    g,
		LogsRoot: logsRoot,
		Registry: registry,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if diff := cmp.Diff(StatusFail, result.Status); diff != "" {
		t.Errorf("goal gate should cause pipeline failure (-want +got):\n%s", diff)
	}
}

func TestRun_GoalGateSuccess(t *testing.T) {
	g := &dot.Graph{
		Name:  "GoalGateOK",
		Attrs: map[string]string{},
		Nodes: []*dot.Node{
			{ID: "start", Attrs: map[string]string{"shape": "Mdiamond"}},
			{ID: "critical", Attrs: map[string]string{"shape": "box", "prompt": "pass", "goal_gate": "true"}},
			{ID: "exit", Attrs: map[string]string{"shape": "Msquare"}},
		},
		Edges: []*dot.Edge{
			{From: "start", To: "critical", Attrs: map[string]string{}},
			{From: "critical", To: "exit", Attrs: map[string]string{}},
		},
	}

	logsRoot := t.TempDir()
	result, err := Run(context.Background(), RunConfig{
		Graph:    g,
		LogsRoot: logsRoot,
		Registry: DefaultHandlerRegistry(CodergenHandler{Backend: SimulatedBackend{}}),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if diff := cmp.Diff(StatusSuccess, result.Status); diff != "" {
		t.Errorf("goal gate with success should allow exit (-want +got):\n%s", diff)
	}
}

func TestRun_CheckpointWritten(t *testing.T) {
	logsRoot := t.TempDir()
	_, err := Run(context.Background(), RunConfig{
		Graph:    linearGraph(),
		LogsRoot: logsRoot,
		Registry: DefaultHandlerRegistry(CodergenHandler{}),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cp, err := LoadCheckpoint(filepath.Join(logsRoot, "checkpoint.json"))
	if err != nil {
		t.Fatalf("checkpoint not written: %v", err)
	}
	if diff := cmp.Diff("work", cp.CurrentNode); diff != "" {
		t.Errorf("checkpoint current node mismatch (-want +got):\n%s", diff)
	}
}

func TestRun_GraphAttributesMirrored(t *testing.T) {
	g := &dot.Graph{
		Name:  "Mirror",
		Attrs: map[string]string{"goal": "test mirroring", "label": "Test"},
		Nodes: []*dot.Node{
			{ID: "start", Attrs: map[string]string{"shape": "Mdiamond"}},
			{ID: "exit", Attrs: map[string]string{"shape": "Msquare"}},
		},
		Edges: []*dot.Edge{
			{From: "start", To: "exit", Attrs: map[string]string{}},
		},
	}

	logsRoot := t.TempDir()
	_, err := Run(context.Background(), RunConfig{
		Graph:    g,
		LogsRoot: logsRoot,
		Registry: DefaultHandlerRegistry(CodergenHandler{}),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cp, _ := LoadCheckpoint(filepath.Join(logsRoot, "checkpoint.json"))
	if diff := cmp.Diff("test mirroring", cp.ContextValues["graph.goal"]); diff != "" {
		t.Errorf("graph.goal not mirrored into context (-want +got):\n%s", diff)
	}
}

func TestRun_NoStartNode(t *testing.T) {
	g := &dot.Graph{
		Name:  "NoStart",
		Attrs: map[string]string{},
		Nodes: []*dot.Node{
			{ID: "exit", Attrs: map[string]string{"shape": "Msquare"}},
		},
	}
	_, err := Run(context.Background(), RunConfig{
		Graph:    g,
		LogsRoot: t.TempDir(),
		Registry: DefaultHandlerRegistry(CodergenHandler{}),
	})
	if err == nil {
		t.Fatal("expected error for missing start node")
	}
}

func TestRun_FailWithNoOutgoingEdge(t *testing.T) {
	// A node that fails with no outgoing edges at all should terminate
	// the pipeline with a failure.
	g := &dot.Graph{
		Name:  "FailNoEdge",
		Attrs: map[string]string{},
		Nodes: []*dot.Node{
			{ID: "start", Attrs: map[string]string{"shape": "Mdiamond"}},
			{ID: "broken", Attrs: map[string]string{"shape": "box", "prompt": "fail"}},
			{ID: "exit", Attrs: map[string]string{"shape": "Msquare"}},
		},
		Edges: []*dot.Edge{
			{From: "start", To: "broken", Attrs: map[string]string{}},
			// No edge from broken -> anywhere
		},
	}

	registry := NewHandlerRegistry(CodergenHandler{Backend: nil})
	registry.Register("start", StartHandler{})
	registry.Register("exit", ExitHandler{})
	registry.Register("codergen", CodergenHandler{Backend: failingBackend{msg: "boom"}})

	result, err := Run(context.Background(), RunConfig{
		Graph:    g,
		LogsRoot: t.TempDir(),
		Registry: registry,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if diff := cmp.Diff(StatusFail, result.Status); diff != "" {
		t.Errorf("should fail when node fails with no outgoing edges (-want +got):\n%s", diff)
	}
}

func TestRun_FallbackEdgeSelection(t *testing.T) {
	// Per spec: when no conditions match, the fallback selects any edge.
	// Here the only edge has condition="outcome=success" but the node fails.
	// The fallback still picks it, so the pipeline reaches exit successfully.
	g := &dot.Graph{
		Name:  "Fallback",
		Attrs: map[string]string{},
		Nodes: []*dot.Node{
			{ID: "start", Attrs: map[string]string{"shape": "Mdiamond"}},
			{ID: "work", Attrs: map[string]string{"shape": "box", "prompt": "do"}},
			{ID: "exit", Attrs: map[string]string{"shape": "Msquare"}},
		},
		Edges: []*dot.Edge{
			{From: "start", To: "work", Attrs: map[string]string{}},
			{From: "work", To: "exit", Attrs: map[string]string{"condition": "outcome=success"}},
		},
	}

	registry := NewHandlerRegistry(CodergenHandler{Backend: nil})
	registry.Register("start", StartHandler{})
	registry.Register("exit", ExitHandler{})
	registry.Register("codergen", CodergenHandler{Backend: failingBackend{msg: "oops"}})

	result, err := Run(context.Background(), RunConfig{
		Graph:    g,
		LogsRoot: t.TempDir(),
		Registry: registry,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Fallback edge selection means the pipeline still reaches exit.
	if diff := cmp.Diff(StatusSuccess, result.Status); diff != "" {
		t.Errorf("fallback edge should route to exit (-want +got):\n%s", diff)
	}
}

func TestRun_CheckpointWarning(t *testing.T) {
	g := linearGraph()
	// Use a path that cannot be written to trigger a checkpoint save error.
	result, err := Run(context.Background(), RunConfig{
		Graph:    g,
		LogsRoot: "/dev/null/impossible",
		Registry: DefaultHandlerRegistry(CodergenHandler{}),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Warnings) == 0 {
		t.Fatal("expected at least one warning about checkpoint save failure")
	}
	foundCheckpoint := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "checkpoint") {
			foundCheckpoint = true
			break
		}
	}
	if !foundCheckpoint {
		t.Errorf("expected a warning mentioning 'checkpoint', got %v", result.Warnings)
	}
}

func TestRun_AggregatesUsage(t *testing.T) {
	g := &dot.Graph{
		Name:  "UsageTest",
		Attrs: map[string]string{},
		Nodes: []*dot.Node{
			{ID: "start", Attrs: map[string]string{"shape": "Mdiamond"}},
			{ID: "stage1", Attrs: map[string]string{"shape": "box", "prompt": "first"}},
			{ID: "stage2", Attrs: map[string]string{"shape": "box", "prompt": "second"}},
			{ID: "exit", Attrs: map[string]string{"shape": "Msquare"}},
		},
		Edges: []*dot.Edge{
			{From: "start", To: "stage1", Attrs: map[string]string{}},
			{From: "stage1", To: "stage2", Attrs: map[string]string{}},
			{From: "stage2", To: "exit", Attrs: map[string]string{}},
		},
	}

	result, err := Run(context.Background(), RunConfig{
		Graph:    g,
		LogsRoot: t.TempDir(),
		Registry: DefaultHandlerRegistry(CodergenHandler{Backend: usageBackend{}}),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if diff := cmp.Diff(StatusSuccess, result.Status); diff != "" {
		t.Errorf("status mismatch (-want +got):\n%s", diff)
	}

	// Two codergen stages, each with 5000 input + 1200 output.
	if diff := cmp.Diff(10000, result.TotalUsage.InputTokens); diff != "" {
		t.Errorf("total input tokens mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(2400, result.TotalUsage.OutputTokens); diff != "" {
		t.Errorf("total output tokens mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(14, result.TotalUsage.Rounds); diff != "" {
		t.Errorf("total rounds mismatch (-want +got):\n%s", diff)
	}

	// Per-stage breakdown should have entries for both codergen nodes.
	if _, ok := result.StageUsages["stage1"]; !ok {
		t.Error("expected StageUsages to contain stage1")
	}
	if _, ok := result.StageUsages["stage2"]; !ok {
		t.Error("expected StageUsages to contain stage2")
	}

	// Start and exit nodes should NOT have usage entries.
	if _, ok := result.StageUsages["start"]; ok {
		t.Error("start node should not have usage entry")
	}
}

func TestRun_BudgetCapExceeded(t *testing.T) {
	g := &dot.Graph{
		Name:  "BudgetTest",
		Attrs: map[string]string{},
		Nodes: []*dot.Node{
			{ID: "start", Attrs: map[string]string{"shape": "Mdiamond"}},
			{ID: "stage1", Attrs: map[string]string{"shape": "box", "prompt": "first"}},
			{ID: "stage2", Attrs: map[string]string{"shape": "box", "prompt": "second"}},
			{ID: "exit", Attrs: map[string]string{"shape": "Msquare"}},
		},
		Edges: []*dot.Edge{
			{From: "start", To: "stage1", Attrs: map[string]string{}},
			{From: "stage1", To: "stage2", Attrs: map[string]string{}},
			{From: "stage2", To: "exit", Attrs: map[string]string{}},
		},
	}

	// usageBackend returns 6200 total tokens per stage.
	// Cap at 7000: first stage (6200) passes, second stage (12400 cumulative) exceeds.
	result, err := Run(context.Background(), RunConfig{
		Graph:           g,
		LogsRoot:        t.TempDir(),
		Registry:        DefaultHandlerRegistry(CodergenHandler{Backend: usageBackend{}}),
		MaxBudgetTokens: 7000,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if diff := cmp.Diff(StatusFail, result.Status); diff != "" {
		t.Errorf("status mismatch (-want +got):\n%s", diff)
	}
	if !strings.Contains(result.FailureReason, "budget cap") {
		t.Errorf("expected failure reason to mention budget cap, got %q", result.FailureReason)
	}
	if diff := cmp.Diff(12400, result.TotalUsage.TotalTokens); diff != "" {
		t.Errorf("total tokens mismatch (-want +got):\n%s", diff)
	}
	if _, ok := result.StageUsages["stage1"]; !ok {
		t.Error("expected StageUsages to contain stage1")
	}
	if _, ok := result.StageUsages["stage2"]; !ok {
		t.Error("expected StageUsages to contain stage2")
	}
}

func TestRun_BudgetCapNotExceeded(t *testing.T) {
	g := &dot.Graph{
		Name:  "BudgetOK",
		Attrs: map[string]string{},
		Nodes: []*dot.Node{
			{ID: "start", Attrs: map[string]string{"shape": "Mdiamond"}},
			{ID: "stage1", Attrs: map[string]string{"shape": "box", "prompt": "first"}},
			{ID: "stage2", Attrs: map[string]string{"shape": "box", "prompt": "second"}},
			{ID: "exit", Attrs: map[string]string{"shape": "Msquare"}},
		},
		Edges: []*dot.Edge{
			{From: "start", To: "stage1", Attrs: map[string]string{}},
			{From: "stage1", To: "stage2", Attrs: map[string]string{}},
			{From: "stage2", To: "exit", Attrs: map[string]string{}},
		},
	}

	result, err := Run(context.Background(), RunConfig{
		Graph:           g,
		LogsRoot:        t.TempDir(),
		Registry:        DefaultHandlerRegistry(CodergenHandler{Backend: usageBackend{}}),
		MaxBudgetTokens: 50000,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if diff := cmp.Diff(StatusSuccess, result.Status); diff != "" {
		t.Errorf("status mismatch (-want +got):\n%s", diff)
	}
}

func TestRun_FailHaltsOnUnconditionalEdge(t *testing.T) {
	g := &dot.Graph{
		Name:  "FailHalt",
		Attrs: map[string]string{},
		Nodes: []*dot.Node{
			{ID: "start", Attrs: map[string]string{"shape": "Mdiamond"}},
			{ID: "broken", Attrs: map[string]string{"shape": "box", "prompt": "fail here"}},
			{ID: "downstream", Attrs: map[string]string{"shape": "box", "prompt": "should not run"}},
			{ID: "exit", Attrs: map[string]string{"shape": "Msquare"}},
		},
		Edges: []*dot.Edge{
			{From: "start", To: "broken", Attrs: map[string]string{}},
			{From: "broken", To: "downstream", Attrs: map[string]string{}},
			{From: "downstream", To: "exit", Attrs: map[string]string{}},
		},
	}

	registry := NewHandlerRegistry(CodergenHandler{Backend: nil})
	registry.Register("start", StartHandler{})
	registry.Register("exit", ExitHandler{})
	registry.Register("codergen", CodergenHandler{Backend: failingBackend{msg: "stage crashed"}})

	result, err := Run(context.Background(), RunConfig{
		Graph:    g,
		LogsRoot: t.TempDir(),
		Registry: registry,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if diff := cmp.Diff(StatusFail, result.Status); diff != "" {
		t.Errorf("status mismatch (-want +got):\n%s", diff)
	}
	if !strings.Contains(result.FailureReason, "unconditional edge") {
		t.Errorf("expected failure reason to mention unconditional edge, got %q", result.FailureReason)
	}
	if !strings.Contains(result.FailureReason, "broken") {
		t.Errorf("expected failure reason to mention the failed node, got %q", result.FailureReason)
	}
	// "downstream" should NOT appear in completed nodes.
	for _, n := range result.CompletedNodes {
		if n == "downstream" {
			t.Error("downstream node should not have been reached after unconditional fail")
		}
	}
}

func TestRun_FailContinuesOnConditionalEdge(t *testing.T) {
	g := &dot.Graph{
		Name:  "FailRecover",
		Attrs: map[string]string{},
		Nodes: []*dot.Node{
			{ID: "start", Attrs: map[string]string{"shape": "Mdiamond"}},
			{ID: "risky", Attrs: map[string]string{"shape": "box", "prompt": "might fail"}},
			{ID: "recover", Attrs: map[string]string{"shape": "box", "prompt": "handle failure"}},
			{ID: "exit", Attrs: map[string]string{"shape": "Msquare"}},
		},
		Edges: []*dot.Edge{
			{From: "start", To: "risky", Attrs: map[string]string{}},
			{From: "risky", To: "recover", Attrs: map[string]string{"condition": "outcome=fail"}},
			{From: "risky", To: "exit", Attrs: map[string]string{"condition": "outcome=success"}},
			{From: "recover", To: "exit", Attrs: map[string]string{}},
		},
	}

	// Register a backend that fails for "risky" but succeeds for "recover".
	riskyFail := &nodeSelectiveBackend{
		failNodes: map[string]bool{"risky": true},
	}

	registry := NewHandlerRegistry(CodergenHandler{Backend: nil})
	registry.Register("start", StartHandler{})
	registry.Register("exit", ExitHandler{})
	registry.Register("codergen", CodergenHandler{Backend: riskyFail})

	result, err := Run(context.Background(), RunConfig{
		Graph:    g,
		LogsRoot: t.TempDir(),
		Registry: registry,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if diff := cmp.Diff(StatusSuccess, result.Status); diff != "" {
		t.Errorf("pipeline should succeed via recovery path (-want +got):\n%s", diff)
	}
	// Both risky and recover should appear in completed nodes.
	foundRisky, foundRecover := false, false
	for _, n := range result.CompletedNodes {
		if n == "risky" {
			foundRisky = true
		}
		if n == "recover" {
			foundRecover = true
		}
	}
	if !foundRisky {
		t.Error("expected 'risky' in completed nodes")
	}
	if !foundRecover {
		t.Error("expected 'recover' in completed nodes")
	}
}

// nodeSelectiveBackend fails for nodes in failNodes, succeeds for all others.
type nodeSelectiveBackend struct {
	failNodes map[string]bool
}

func (b *nodeSelectiveBackend) Run(ctx context.Context, node *dot.Node, _ string, _ *Context) (BackendResult, error) {
	if b.failNodes[node.ID] {
		return BackendResult{}, fmt.Errorf("simulated failure for %s", node.ID)
	}
	return BackendResult{
		Response: "completed " + node.ID,
	}, nil
}

func TestRun_MaxIterationsExceeded(t *testing.T) {
	g := &dot.Graph{
		Name:  "Cycle",
		Attrs: map[string]string{},
		Nodes: []*dot.Node{
			{ID: "start", Attrs: map[string]string{"shape": "Mdiamond"}},
			{ID: "a", Attrs: map[string]string{"shape": "box", "prompt": "loop"}},
			{ID: "b", Attrs: map[string]string{"shape": "box", "prompt": "loop"}},
			{ID: "exit", Attrs: map[string]string{"shape": "Msquare"}},
		},
		Edges: []*dot.Edge{
			{From: "start", To: "a", Attrs: map[string]string{}},
			{From: "a", To: "b", Attrs: map[string]string{}},
			{From: "b", To: "a", Attrs: map[string]string{}},
		},
	}

	result, err := Run(context.Background(), RunConfig{
		Graph:         g,
		LogsRoot:      t.TempDir(),
		Registry:      DefaultHandlerRegistry(CodergenHandler{}),
		MaxIterations: 5,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if diff := cmp.Diff(StatusFail, result.Status); diff != "" {
		t.Errorf("status mismatch (-want +got):\n%s", diff)
	}
	if !strings.Contains(result.FailureReason, "max iterations") {
		t.Errorf("expected failure reason to mention max iterations, got %q", result.FailureReason)
	}
}

// --- Context cancellation tests ---

func TestRun_CancelBeforeFirstStage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := Run(ctx, RunConfig{
		Graph:    linearGraph(),
		LogsRoot: t.TempDir(),
		Registry: DefaultHandlerRegistry(CodergenHandler{}),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if diff := cmp.Diff(StatusFail, result.Status); diff != "" {
		t.Errorf("status mismatch (-want +got):\n%s", diff)
	}
	if !strings.Contains(result.FailureReason, "canceled") {
		t.Errorf("expected failure reason to mention cancellation, got %q", result.FailureReason)
	}
}

// blockingBackend blocks until its context is canceled, allowing tests to
// verify that handler execution respects cancellation.
type blockingBackend struct {
	started chan struct{}
}

func (b *blockingBackend) Run(ctx context.Context, _ *dot.Node, _ string, _ *Context) (BackendResult, error) {
	if b.started != nil {
		close(b.started)
	}
	<-ctx.Done()
	return BackendResult{}, ctx.Err()
}

func TestRun_CancelDuringHandlerExecution(t *testing.T) {
	backend := &blockingBackend{started: make(chan struct{})}
	registry := DefaultHandlerRegistry(CodergenHandler{Backend: backend})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	var result RunResult
	var runErr error
	go func() {
		defer close(done)
		result, runErr = Run(ctx, RunConfig{
			Graph:    linearGraph(),
			LogsRoot: t.TempDir(),
			Registry: registry,
		})
	}()

	<-backend.started
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return within 5s after cancellation")
	}

	if runErr != nil {
		t.Fatalf("unexpected error: %v", runErr)
	}
	if result.Status != StatusFail {
		t.Errorf("expected fail status, got %s", result.Status)
	}
}

func TestRun_CancelDuringRetryBackoff(t *testing.T) {
	g := &dot.Graph{
		Name:  "RetryCancel",
		Attrs: map[string]string{"goal": "test retry cancel"},
		Nodes: []*dot.Node{
			{ID: "start", Attrs: map[string]string{"shape": "Mdiamond"}},
			{ID: "work", Attrs: map[string]string{"shape": "box", "max_retries": "5"}},
			{ID: "done", Attrs: map[string]string{"shape": "Msquare"}},
		},
		Edges: []*dot.Edge{
			{From: "start", To: "work", Attrs: map[string]string{}},
			{From: "work", To: "done", Attrs: map[string]string{}},
		},
	}

	attemptCount := 0
	failBackend := &callbackBackend{fn: func(ctx context.Context, node *dot.Node) (BackendResult, error) {
		attemptCount++
		return BackendResult{}, fmt.Errorf("always fails")
	}}

	registry := DefaultHandlerRegistry(CodergenHandler{Backend: failBackend})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	result, err := Run(ctx, RunConfig{
		Graph:    g,
		LogsRoot: t.TempDir(),
		Registry: registry,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusFail {
		t.Errorf("expected fail status, got %s", result.Status)
	}
	workOutcome, ok := result.NodeOutcomes["work"]
	if !ok {
		t.Fatal("expected outcome for node 'work'")
	}
	if !strings.Contains(workOutcome.FailureReason, "canceled") {
		t.Errorf("expected cancellation in work node failure reason, got %q", workOutcome.FailureReason)
	}
	if attemptCount > 3 {
		t.Errorf("expected cancellation to limit retries, but got %d attempts", attemptCount)
	}
}

func TestRun_DeadlineExceeded(t *testing.T) {
	backend := &blockingBackend{}
	registry := DefaultHandlerRegistry(CodergenHandler{Backend: backend})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	result, err := Run(ctx, RunConfig{
		Graph:    linearGraph(),
		LogsRoot: t.TempDir(),
		Registry: registry,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusFail {
		t.Errorf("expected fail status, got %s", result.Status)
	}
	workOutcome, ok := result.NodeOutcomes["work"]
	if !ok {
		t.Fatal("expected outcome for node 'work'")
	}
	if !strings.Contains(workOutcome.FailureReason, "deadline") {
		t.Errorf("expected deadline in work node failure reason, got %q", workOutcome.FailureReason)
	}
}

func TestRun_SimulatedModeStillWorks(t *testing.T) {
	registry := DefaultHandlerRegistry(CodergenHandler{Backend: SimulatedBackend{}})

	result, err := Run(context.Background(), RunConfig{
		Graph:    linearGraph(),
		LogsRoot: t.TempDir(),
		Registry: registry,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if diff := cmp.Diff(StatusSuccess, result.Status); diff != "" {
		t.Errorf("status mismatch (-want +got):\n%s", diff)
	}
}

// callbackBackend delegates to a function, useful for tests that need custom
// per-call behavior.
type callbackBackend struct {
	fn func(ctx context.Context, node *dot.Node) (BackendResult, error)
}

func (b *callbackBackend) Run(ctx context.Context, node *dot.Node, _ string, _ *Context) (BackendResult, error) {
	return b.fn(ctx, node)
}

// --- Attempt-aware retry accounting tests ---

func TestRun_RetryAccumulatesUsage(t *testing.T) {
	g := &dot.Graph{
		Name:  "RetryUsage",
		Attrs: map[string]string{},
		Nodes: []*dot.Node{
			{ID: "start", Attrs: map[string]string{"shape": "Mdiamond"}},
			{ID: "work", Attrs: map[string]string{"shape": "box", "prompt": "do it", "max_retries": "2"}},
			{ID: "exit", Attrs: map[string]string{"shape": "Msquare"}},
		},
		Edges: []*dot.Edge{
			{From: "start", To: "work", Attrs: map[string]string{}},
			{From: "work", To: "exit", Attrs: map[string]string{}},
		},
	}

	callCount := 0
	backend := &callbackBackend{fn: func(_ context.Context, node *dot.Node) (BackendResult, error) {
		callCount++
		if callCount == 1 {
			return BackendResult{
				Response:         "partial work",
				Usage:            llm.Usage{InputTokens: 3000, OutputTokens: 800, TotalTokens: 3800},
				Model:            "test-model",
				Rounds:           5,
				Exhausted:        true,
				ExhaustionReason: ExhaustionRoundLimit,
			}, nil
		}
		return BackendResult{
			Response: "done",
			Usage:    llm.Usage{InputTokens: 2000, OutputTokens: 600, TotalTokens: 2600},
			Model:    "test-model",
			Rounds:   3,
		}, nil
	}}

	result, err := Run(context.Background(), RunConfig{
		Graph:    g,
		LogsRoot: t.TempDir(),
		Registry: DefaultHandlerRegistry(CodergenHandler{Backend: backend}),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if diff := cmp.Diff(StatusSuccess, result.Status); diff != "" {
		t.Errorf("status mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(5000, result.TotalUsage.InputTokens); diff != "" {
		t.Errorf("total input tokens should sum both attempts (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(1400, result.TotalUsage.OutputTokens); diff != "" {
		t.Errorf("total output tokens should sum both attempts (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(6400, result.TotalUsage.TotalTokens); diff != "" {
		t.Errorf("total tokens should sum both attempts (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(8, result.TotalUsage.Rounds); diff != "" {
		t.Errorf("total rounds should sum both attempts (-want +got):\n%s", diff)
	}

	workUsage, ok := result.StageUsages["work"]
	if !ok {
		t.Fatal("expected StageUsages to contain work")
	}
	if diff := cmp.Diff(6400, workUsage.TotalTokens); diff != "" {
		t.Errorf("stage usage should reflect cumulative tokens (-want +got):\n%s", diff)
	}

	workOutcome := result.NodeOutcomes["work"]
	if diff := cmp.Diff(2, workOutcome.Attempts); diff != "" {
		t.Errorf("Attempts should be 2 (-want +got):\n%s", diff)
	}
}

func TestRun_RetryExhaustedAccumulatesUsage(t *testing.T) {
	g := &dot.Graph{
		Name:  "RetryExhaust",
		Attrs: map[string]string{},
		Nodes: []*dot.Node{
			{ID: "start", Attrs: map[string]string{"shape": "Mdiamond"}},
			{ID: "work", Attrs: map[string]string{"shape": "box", "prompt": "do it", "max_retries": "1"}},
			{ID: "exit", Attrs: map[string]string{"shape": "Msquare"}},
		},
		Edges: []*dot.Edge{
			{From: "start", To: "work", Attrs: map[string]string{}},
			{From: "work", To: "exit", Attrs: map[string]string{}},
		},
	}

	backend := &callbackBackend{fn: func(_ context.Context, _ *dot.Node) (BackendResult, error) {
		return BackendResult{
			Response:         "partial",
			Usage:            llm.Usage{InputTokens: 4000, OutputTokens: 1000, TotalTokens: 5000},
			Model:            "test-model",
			Rounds:           6,
			Exhausted:        true,
			ExhaustionReason: ExhaustionRoundLimit,
		}, nil
	}}

	registry := NewHandlerRegistry(CodergenHandler{Backend: nil})
	registry.Register("start", StartHandler{})
	registry.Register("exit", ExitHandler{})
	registry.Register("codergen", CodergenHandler{Backend: backend})

	result, err := Run(context.Background(), RunConfig{
		Graph:    g,
		LogsRoot: t.TempDir(),
		Registry: registry,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Both attempts fail (exhausted), so the pipeline should fail via
	// the unconditional-edge halt logic.
	if diff := cmp.Diff(StatusFail, result.Status); diff != "" {
		t.Errorf("status mismatch (-want +got):\n%s", diff)
	}

	// Both attempts' usage should be accumulated.
	if diff := cmp.Diff(10000, result.TotalUsage.TotalTokens); diff != "" {
		t.Errorf("total tokens should include both exhausted attempts (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(12, result.TotalUsage.Rounds); diff != "" {
		t.Errorf("total rounds should include both exhausted attempts (-want +got):\n%s", diff)
	}

	workOutcome := result.NodeOutcomes["work"]
	if diff := cmp.Diff(2, workOutcome.Attempts); diff != "" {
		t.Errorf("Attempts should be 2 (1 retry) (-want +got):\n%s", diff)
	}
}

func TestRun_RetryBudgetEnforcementIncludesAllAttempts(t *testing.T) {
	g := &dot.Graph{
		Name:  "RetryBudget",
		Attrs: map[string]string{},
		Nodes: []*dot.Node{
			{ID: "start", Attrs: map[string]string{"shape": "Mdiamond"}},
			{ID: "work", Attrs: map[string]string{"shape": "box", "prompt": "do it", "max_retries": "2"}},
			{ID: "exit", Attrs: map[string]string{"shape": "Msquare"}},
		},
		Edges: []*dot.Edge{
			{From: "start", To: "work", Attrs: map[string]string{}},
			{From: "work", To: "exit", Attrs: map[string]string{}},
		},
	}

	callCount := 0
	backend := &callbackBackend{fn: func(_ context.Context, _ *dot.Node) (BackendResult, error) {
		callCount++
		if callCount == 1 {
			return BackendResult{
				Response:         "partial",
				Usage:            llm.Usage{InputTokens: 3000, OutputTokens: 1000, TotalTokens: 4000},
				Model:            "test-model",
				Rounds:           4,
				Exhausted:        true,
				ExhaustionReason: ExhaustionRoundLimit,
			}, nil
		}
		// Second attempt succeeds but pushes cumulative total over budget.
		return BackendResult{
			Response: "done",
			Usage:    llm.Usage{InputTokens: 3000, OutputTokens: 1000, TotalTokens: 4000},
			Model:    "test-model",
			Rounds:   4,
		}, nil
	}}

	// Budget of 5000: attempt 1 uses 4000, attempt 2 uses 4000 -> 8000 total exceeds budget.
	result, err := Run(context.Background(), RunConfig{
		Graph:           g,
		LogsRoot:        t.TempDir(),
		Registry:        DefaultHandlerRegistry(CodergenHandler{Backend: backend}),
		MaxBudgetTokens: 5000,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if diff := cmp.Diff(StatusFail, result.Status); diff != "" {
		t.Errorf("status mismatch (-want +got):\n%s", diff)
	}
	if !strings.Contains(result.FailureReason, "budget cap") {
		t.Errorf("expected failure reason to mention budget cap, got %q", result.FailureReason)
	}
	if diff := cmp.Diff(8000, result.TotalUsage.TotalTokens); diff != "" {
		t.Errorf("total tokens should reflect both attempts (-want +got):\n%s", diff)
	}
}
