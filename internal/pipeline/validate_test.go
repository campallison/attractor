package pipeline

import (
	"testing"

	"github.com/campallison/attractor/internal/dot"
	"github.com/google/go-cmp/cmp"
)

func validGraph() *dot.Graph {
	return &dot.Graph{
		Name:  "Test",
		Attrs: map[string]string{},
		Nodes: []*dot.Node{
			{ID: "start", Attrs: map[string]string{"shape": "Mdiamond"}},
			{ID: "work", Attrs: map[string]string{"shape": "box", "prompt": "do work"}},
			{ID: "exit", Attrs: map[string]string{"shape": "Msquare"}},
		},
		Edges: []*dot.Edge{
			{From: "start", To: "work", Attrs: map[string]string{}},
			{From: "work", To: "exit", Attrs: map[string]string{}},
		},
	}
}

func TestValidate_ValidGraph(t *testing.T) {
	diags := Validate(validGraph())
	if HasErrors(diags) {
		for _, d := range diags {
			t.Errorf("unexpected diagnostic: %s", d)
		}
	}
}

func TestValidate_MissingStartNode(t *testing.T) {
	g := &dot.Graph{
		Name:  "NoStart",
		Attrs: map[string]string{},
		Nodes: []*dot.Node{
			{ID: "work", Attrs: map[string]string{"shape": "box"}},
			{ID: "exit", Attrs: map[string]string{"shape": "Msquare"}},
		},
		Edges: []*dot.Edge{
			{From: "work", To: "exit", Attrs: map[string]string{}},
		},
	}
	diags := Validate(g)
	found := findDiag(diags, "start_node")
	if found == nil {
		t.Fatal("expected start_node diagnostic")
	}
	if diff := cmp.Diff(SeverityError, found.Severity); diff != "" {
		t.Errorf("severity mismatch (-want +got):\n%s", diff)
	}
}

func TestValidate_MissingExitNode(t *testing.T) {
	g := &dot.Graph{
		Name:  "NoExit",
		Attrs: map[string]string{},
		Nodes: []*dot.Node{
			{ID: "start", Attrs: map[string]string{"shape": "Mdiamond"}},
			{ID: "work", Attrs: map[string]string{"shape": "box"}},
		},
		Edges: []*dot.Edge{
			{From: "start", To: "work", Attrs: map[string]string{}},
		},
	}
	diags := Validate(g)
	found := findDiag(diags, "terminal_node")
	if found == nil {
		t.Fatal("expected terminal_node diagnostic")
	}
	if diff := cmp.Diff(SeverityError, found.Severity); diff != "" {
		t.Errorf("severity mismatch (-want +got):\n%s", diff)
	}
}

func TestValidate_StartHasIncoming(t *testing.T) {
	g := validGraph()
	g.Edges = append(g.Edges, &dot.Edge{From: "work", To: "start", Attrs: map[string]string{}})
	diags := Validate(g)
	found := findDiag(diags, "start_no_incoming")
	if found == nil {
		t.Fatal("expected start_no_incoming diagnostic")
	}
}

func TestValidate_ExitHasOutgoing(t *testing.T) {
	g := validGraph()
	g.Edges = append(g.Edges, &dot.Edge{From: "exit", To: "work", Attrs: map[string]string{}})
	diags := Validate(g)
	found := findDiag(diags, "exit_no_outgoing")
	if found == nil {
		t.Fatal("expected exit_no_outgoing diagnostic")
	}
}

func TestValidate_UnreachableNode(t *testing.T) {
	g := validGraph()
	g.Nodes = append(g.Nodes, &dot.Node{ID: "orphan", Attrs: map[string]string{"shape": "box"}})
	diags := Validate(g)
	found := findDiag(diags, "reachability")
	if found == nil {
		t.Fatal("expected reachability diagnostic")
	}
	if diff := cmp.Diff("orphan", found.NodeID); diff != "" {
		t.Errorf("node ID mismatch (-want +got):\n%s", diff)
	}
}

func TestValidate_BadEdgeTarget(t *testing.T) {
	g := validGraph()
	g.Edges = append(g.Edges, &dot.Edge{From: "work", To: "nonexistent", Attrs: map[string]string{}})
	diags := Validate(g)
	found := findDiag(diags, "edge_target_exists")
	if found == nil {
		t.Fatal("expected edge_target_exists diagnostic")
	}
}

func TestValidate_BadConditionSyntax(t *testing.T) {
	g := validGraph()
	g.Edges[1].Attrs["condition"] = "=badkey"
	diags := Validate(g)
	found := findDiag(diags, "condition_syntax")
	if found == nil {
		t.Fatal("expected condition_syntax diagnostic")
	}
}

func TestValidate_UnknownType(t *testing.T) {
	g := validGraph()
	g.Nodes[1].Attrs["type"] = "super_custom"
	diags := Validate(g)
	found := findDiag(diags, "type_known")
	if found == nil {
		t.Fatal("expected type_known warning")
	}
	if diff := cmp.Diff(SeverityWarning, found.Severity); diff != "" {
		t.Errorf("severity mismatch (-want +got):\n%s", diff)
	}
}

func TestValidate_RetryTargetMissing(t *testing.T) {
	g := validGraph()
	g.Nodes[1].Attrs["retry_target"] = "nonexistent"
	diags := Validate(g)
	found := findDiag(diags, "retry_target_exists")
	if found == nil {
		t.Fatal("expected retry_target_exists warning")
	}
}

func TestValidate_GoalGateNoRetry(t *testing.T) {
	g := validGraph()
	g.Nodes[1].Attrs["goal_gate"] = "true"
	diags := Validate(g)
	found := findDiag(diags, "goal_gate_has_retry")
	if found == nil {
		t.Fatal("expected goal_gate_has_retry warning")
	}
}

func TestValidate_PromptOnLLMNodes(t *testing.T) {
	g := validGraph()
	// Remove prompt from codergen node; label defaults to ID.
	delete(g.Nodes[1].Attrs, "prompt")
	delete(g.Nodes[1].Attrs, "label")
	diags := Validate(g)
	found := findDiag(diags, "prompt_on_llm_nodes")
	if found == nil {
		t.Fatal("expected prompt_on_llm_nodes warning")
	}
}

func TestValidateOrError_RejectsErrors(t *testing.T) {
	g := &dot.Graph{
		Name:  "Bad",
		Attrs: map[string]string{},
		Nodes: []*dot.Node{},
		Edges: []*dot.Edge{},
	}
	_, err := ValidateOrError(g)
	if err == nil {
		t.Fatal("expected error from ValidateOrError")
	}
}

func TestValidateOrError_AcceptsValid(t *testing.T) {
	_, err := ValidateOrError(validGraph())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func findDiag(diags []Diagnostic, rule string) *Diagnostic {
	for i := range diags {
		if diags[i].Rule == rule {
			return &diags[i]
		}
	}
	return nil
}
