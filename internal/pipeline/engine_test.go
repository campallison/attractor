package pipeline

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/campallison/attractor/internal/dot"
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
	result, err := Run(RunConfig{
		Graph:    linearGraph(),
		LogsRoot: logsRoot,
		Registry: DefaultHandlerRegistry(nil),
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
	result, err := Run(RunConfig{
		Graph:    g,
		LogsRoot: logsRoot,
		Registry: DefaultHandlerRegistry(nil),
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

	result, err := Run(RunConfig{
		Graph:    g,
		LogsRoot: logsRoot,
		Registry: DefaultHandlerRegistry(nil),
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
	result, err := Run(RunConfig{
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
	result, err := Run(RunConfig{
		Graph:    g,
		LogsRoot: logsRoot,
		Registry: DefaultHandlerRegistry(SimulatedBackend{}),
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
	_, err := Run(RunConfig{
		Graph:    linearGraph(),
		LogsRoot: logsRoot,
		Registry: DefaultHandlerRegistry(nil),
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
	_, err := Run(RunConfig{
		Graph:    g,
		LogsRoot: logsRoot,
		Registry: DefaultHandlerRegistry(nil),
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
	_, err := Run(RunConfig{
		Graph:    g,
		LogsRoot: t.TempDir(),
		Registry: DefaultHandlerRegistry(nil),
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

	result, err := Run(RunConfig{
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

	result, err := Run(RunConfig{
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
	result, err := Run(RunConfig{
		Graph:    g,
		LogsRoot: "/dev/null/impossible",
		Registry: DefaultHandlerRegistry(nil),
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

	result, err := Run(RunConfig{
		Graph:         g,
		LogsRoot:      t.TempDir(),
		Registry:      DefaultHandlerRegistry(nil),
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
