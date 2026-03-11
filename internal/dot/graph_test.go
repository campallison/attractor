package dot

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func TestGraph_FindStartNode(t *testing.T) {
	tests := []struct {
		name  string
		nodes []*Node
		want  string // expected node ID, or "" for nil
	}{
		{
			name: "by shape Mdiamond",
			nodes: []*Node{
				{ID: "s", Attrs: map[string]string{"shape": "Mdiamond"}},
				{ID: "e", Attrs: map[string]string{"shape": "Msquare"}},
			},
			want: "s",
		},
		{
			name: "fallback to ID start",
			nodes: []*Node{
				{ID: "start", Attrs: map[string]string{"shape": "box"}},
				{ID: "e", Attrs: map[string]string{"shape": "Msquare"}},
			},
			want: "start",
		},
		{
			name: "fallback to ID Start",
			nodes: []*Node{
				{ID: "Start", Attrs: map[string]string{"shape": "box"}},
			},
			want: "Start",
		},
		{
			name:  "no start node",
			nodes: []*Node{{ID: "work", Attrs: map[string]string{"shape": "box"}}},
			want:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := &Graph{Nodes: tt.nodes}
			got := g.FindStartNode()
			if tt.want == "" {
				if got != nil {
					t.Errorf("expected nil, got %q", got.ID)
				}
			} else {
				if got == nil {
					t.Fatalf("expected %q, got nil", tt.want)
				}
				if diff := cmp.Diff(tt.want, got.ID); diff != "" {
					t.Errorf("start node ID mismatch (-want +got):\n%s", diff)
				}
			}
		})
	}
}

func TestGraph_FindExitNode(t *testing.T) {
	tests := []struct {
		name  string
		nodes []*Node
		want  string
	}{
		{
			name:  "by shape Msquare",
			nodes: []*Node{{ID: "e", Attrs: map[string]string{"shape": "Msquare"}}},
			want:  "e",
		},
		{
			name:  "fallback to exit",
			nodes: []*Node{{ID: "exit", Attrs: map[string]string{}}},
			want:  "exit",
		},
		{
			name:  "fallback to End",
			nodes: []*Node{{ID: "End", Attrs: map[string]string{}}},
			want:  "End",
		},
		{
			name:  "no exit node",
			nodes: []*Node{{ID: "work", Attrs: map[string]string{}}},
			want:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := &Graph{Nodes: tt.nodes}
			got := g.FindExitNode()
			if tt.want == "" {
				if got != nil {
					t.Errorf("expected nil, got %q", got.ID)
				}
			} else {
				if got == nil {
					t.Fatalf("expected %q, got nil", tt.want)
				}
				if diff := cmp.Diff(tt.want, got.ID); diff != "" {
					t.Errorf("exit node ID mismatch (-want +got):\n%s", diff)
				}
			}
		})
	}
}

func TestGraph_OutgoingEdges(t *testing.T) {
	g := &Graph{
		Edges: []*Edge{
			{From: "A", To: "B", Attrs: map[string]string{}},
			{From: "A", To: "C", Attrs: map[string]string{}},
			{From: "B", To: "C", Attrs: map[string]string{}},
		},
	}
	out := g.OutgoingEdges("A")
	if diff := cmp.Diff(2, len(out)); diff != "" {
		t.Errorf("outgoing edge count mismatch (-want +got):\n%s", diff)
	}
}

func TestGraph_IncomingEdges(t *testing.T) {
	g := &Graph{
		Edges: []*Edge{
			{From: "A", To: "C", Attrs: map[string]string{}},
			{From: "B", To: "C", Attrs: map[string]string{}},
		},
	}
	in := g.IncomingEdges("C")
	if diff := cmp.Diff(2, len(in)); diff != "" {
		t.Errorf("incoming edge count mismatch (-want +got):\n%s", diff)
	}
}

func TestNode_Accessors(t *testing.T) {
	n := &Node{ID: "test", Attrs: map[string]string{
		"shape":       "hexagon",
		"label":       "Test Node",
		"goal_gate":   "true",
		"max_retries": "5",
		"max_rounds":  "20",
		"timeout":     "900s",
		"model":       "anthropic/claude-sonnet-4",
	}}

	if diff := cmp.Diff("hexagon", n.Shape()); diff != "" {
		t.Errorf("shape (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("Test Node", n.NodeLabel()); diff != "" {
		t.Errorf("label (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(true, n.GoalGate()); diff != "" {
		t.Errorf("goal_gate (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(5, n.MaxRetries()); diff != "" {
		t.Errorf("max_retries (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(20, n.MaxRounds()); diff != "" {
		t.Errorf("max_rounds (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(900*time.Second, n.GetDuration("timeout", 0)); diff != "" {
		t.Errorf("timeout (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("anthropic/claude-sonnet-4", n.Model()); diff != "" {
		t.Errorf("model (-want +got):\n%s", diff)
	}
}

func TestNode_DefaultValues(t *testing.T) {
	n := &Node{ID: "bare", Attrs: map[string]string{}}

	if diff := cmp.Diff("box", n.Shape()); diff != "" {
		t.Errorf("default shape (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("bare", n.NodeLabel()); diff != "" {
		t.Errorf("default label should be ID (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(false, n.GoalGate()); diff != "" {
		t.Errorf("default goal_gate (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(0, n.MaxRetries()); diff != "" {
		t.Errorf("default max_retries (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(0, n.MaxRounds()); diff != "" {
		t.Errorf("default max_rounds (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("", n.Model()); diff != "" {
		t.Errorf("default model (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(false, n.AllowEmptyOutput()); diff != "" {
		t.Errorf("default allow_empty_output (-want +got):\n%s", diff)
	}
}

func TestNode_AllowEmptyOutput(t *testing.T) {
	tests := []struct {
		name  string
		attrs map[string]string
		want  bool
	}{
		{"unset defaults to false", map[string]string{}, false},
		{"true", map[string]string{"allow_empty_output": "true"}, true},
		{"false", map[string]string{"allow_empty_output": "false"}, false},
		{"invalid defaults to false", map[string]string{"allow_empty_output": "maybe"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := &Node{ID: "test", Attrs: tt.attrs}
			if diff := cmp.Diff(tt.want, n.AllowEmptyOutput()); diff != "" {
				t.Errorf("AllowEmptyOutput mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		input    string
		fallback time.Duration
		want     time.Duration
	}{
		{input: "250ms", want: 250 * time.Millisecond},
		{input: "30s", want: 30 * time.Second},
		{input: "15m", want: 15 * time.Minute},
		{input: "2h", want: 2 * time.Hour},
		{input: "1d", want: 24 * time.Hour},
		{input: "900s", want: 900 * time.Second},
		{input: "invalid", fallback: 5 * time.Second, want: 5 * time.Second},
		{input: "", fallback: 10 * time.Second, want: 10 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ParseDuration(tt.input, tt.fallback)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("ParseDuration mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestEdge_Accessors(t *testing.T) {
	e := &Edge{
		From: "A", To: "B",
		Attrs: map[string]string{
			"label":        "retry",
			"condition":    "outcome=fail",
			"weight":       "5",
			"loop_restart": "true",
		},
	}
	if diff := cmp.Diff("retry", e.EdgeLabel()); diff != "" {
		t.Errorf("label (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("outcome=fail", e.Condition()); diff != "" {
		t.Errorf("condition (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(5, e.Weight()); diff != "" {
		t.Errorf("weight (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(true, e.LoopRestart()); diff != "" {
		t.Errorf("loop_restart (-want +got):\n%s", diff)
	}
}
