package pipeline

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

func TestValidate_MaxRetriesTooHigh(t *testing.T) {
	g := validGraph()
	g.Nodes[1].Attrs["max_retries"] = "500"
	diags := Validate(g)
	found := findDiag(diags, "max_retries_cap")
	if found == nil {
		t.Fatal("expected max_retries_cap warning for max_retries=500")
	}
	if diff := cmp.Diff(SeverityWarning, found.Severity); diff != "" {
		t.Errorf("severity mismatch (-want +got):\n%s", diff)
	}
}

func TestValidate_MaxRetriesOK(t *testing.T) {
	g := validGraph()
	g.Nodes[1].Attrs["max_retries"] = "5"
	diags := Validate(g)
	found := findDiag(diags, "max_retries_cap")
	if found != nil {
		t.Errorf("unexpected max_retries_cap diagnostic for max_retries=5: %s", found)
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

func findAllDiags(diags []Diagnostic, rule string) []Diagnostic {
	var found []Diagnostic
	for _, d := range diags {
		if d.Rule == rule {
			found = append(found, d)
		}
	}
	return found
}

// --- no_fail_recovery ---

func TestValidate_NoFailRecovery_Triggered(t *testing.T) {
	g := validGraph()
	diags := Validate(g)
	found := findDiag(diags, "no_fail_recovery")
	if found == nil {
		t.Fatal("expected no_fail_recovery warning for codergen node with only unconditional edges")
	}
	if diff := cmp.Diff(SeverityWarning, found.Severity); diff != "" {
		t.Errorf("severity mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("work", found.NodeID); diff != "" {
		t.Errorf("node ID mismatch (-want +got):\n%s", diff)
	}
}

func TestValidate_NoFailRecovery_NotTriggeredWithConditionalEdge(t *testing.T) {
	g := &dot.Graph{
		Name:  "WithRecovery",
		Attrs: map[string]string{},
		Nodes: []*dot.Node{
			{ID: "start", Attrs: map[string]string{"shape": "Mdiamond"}},
			{ID: "work", Attrs: map[string]string{"shape": "box", "prompt": "do work"}},
			{ID: "fix", Attrs: map[string]string{"shape": "box", "prompt": "fix it"}},
			{ID: "exit", Attrs: map[string]string{"shape": "Msquare"}},
		},
		Edges: []*dot.Edge{
			{From: "start", To: "work", Attrs: map[string]string{}},
			{From: "work", To: "exit", Attrs: map[string]string{"condition": "outcome=success"}},
			{From: "work", To: "fix", Attrs: map[string]string{"condition": "outcome=fail"}},
			{From: "fix", To: "exit", Attrs: map[string]string{}},
		},
	}
	diags := Validate(g)
	found := findDiag(diags, "no_fail_recovery")
	if found != nil && found.NodeID == "work" {
		t.Errorf("unexpected no_fail_recovery for node with conditional edges: %s", found)
	}
}

func TestValidate_NoFailRecovery_SkipsNonCodergen(t *testing.T) {
	g := &dot.Graph{
		Name:  "Diamond",
		Attrs: map[string]string{},
		Nodes: []*dot.Node{
			{ID: "start", Attrs: map[string]string{"shape": "Mdiamond"}},
			{ID: "gate", Attrs: map[string]string{"shape": "diamond"}},
			{ID: "exit", Attrs: map[string]string{"shape": "Msquare"}},
		},
		Edges: []*dot.Edge{
			{From: "start", To: "gate", Attrs: map[string]string{}},
			{From: "gate", To: "exit", Attrs: map[string]string{}},
		},
	}
	diags := Validate(g)
	found := findDiag(diags, "no_fail_recovery")
	if found != nil {
		t.Errorf("unexpected no_fail_recovery for non-codergen node: %s", found)
	}
}

// --- linear_no_conditions ---

func TestValidate_LinearNoConditions_Triggered(t *testing.T) {
	g := validGraph()
	diags := Validate(g)
	found := findDiag(diags, "linear_no_conditions")
	if found == nil {
		t.Fatal("expected linear_no_conditions info for graph with no conditional edges")
	}
	if diff := cmp.Diff(SeverityInfo, found.Severity); diff != "" {
		t.Errorf("severity mismatch (-want +got):\n%s", diff)
	}
}

func TestValidate_LinearNoConditions_NotTriggered(t *testing.T) {
	g := &dot.Graph{
		Name:  "WithConditions",
		Attrs: map[string]string{},
		Nodes: []*dot.Node{
			{ID: "start", Attrs: map[string]string{"shape": "Mdiamond"}},
			{ID: "work", Attrs: map[string]string{"shape": "box", "prompt": "do work"}},
			{ID: "exit", Attrs: map[string]string{"shape": "Msquare"}},
		},
		Edges: []*dot.Edge{
			{From: "start", To: "work", Attrs: map[string]string{}},
			{From: "work", To: "exit", Attrs: map[string]string{"condition": "outcome=success"}},
		},
	}
	diags := Validate(g)
	found := findDiag(diags, "linear_no_conditions")
	if found != nil {
		t.Errorf("unexpected linear_no_conditions for graph with conditional edges: %s", found)
	}
}

// --- exit_reachable ---

func TestValidate_ExitReachable_Triggered(t *testing.T) {
	g := &dot.Graph{
		Name:  "DeadEnd",
		Attrs: map[string]string{},
		Nodes: []*dot.Node{
			{ID: "start", Attrs: map[string]string{"shape": "Mdiamond"}},
			{ID: "work", Attrs: map[string]string{"shape": "box", "prompt": "do work"}},
			{ID: "dead", Attrs: map[string]string{"shape": "box", "prompt": "dead end"}},
			{ID: "exit", Attrs: map[string]string{"shape": "Msquare"}},
		},
		Edges: []*dot.Edge{
			{From: "start", To: "work", Attrs: map[string]string{}},
			{From: "start", To: "dead", Attrs: map[string]string{}},
			{From: "work", To: "exit", Attrs: map[string]string{}},
		},
	}
	diags := Validate(g)
	found := findDiag(diags, "exit_reachable")
	if found == nil {
		t.Fatal("expected exit_reachable warning for dead-end node")
	}
	if diff := cmp.Diff("dead", found.NodeID); diff != "" {
		t.Errorf("node ID mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(SeverityWarning, found.Severity); diff != "" {
		t.Errorf("severity mismatch (-want +got):\n%s", diff)
	}
}

func TestValidate_ExitReachable_AllReachable(t *testing.T) {
	g := validGraph()
	diags := Validate(g)
	found := findDiag(diags, "exit_reachable")
	if found != nil {
		t.Errorf("unexpected exit_reachable for graph where all nodes reach exit: %s", found)
	}
}

// --- retry_path_to_gate ---

func TestValidate_RetryPathToGate_Triggered(t *testing.T) {
	g := &dot.Graph{
		Name:  "BrokenRetry",
		Attrs: map[string]string{},
		Nodes: []*dot.Node{
			{ID: "start", Attrs: map[string]string{"shape": "Mdiamond"}},
			{ID: "work", Attrs: map[string]string{"shape": "box", "prompt": "do work"}},
			{ID: "orphan_fix", Attrs: map[string]string{"shape": "box", "prompt": "fix it"}},
			{ID: "gate", Attrs: map[string]string{"shape": "box", "prompt": "check", "goal_gate": "true", "retry_target": "orphan_fix"}},
			{ID: "exit", Attrs: map[string]string{"shape": "Msquare"}},
		},
		Edges: []*dot.Edge{
			{From: "start", To: "work", Attrs: map[string]string{}},
			{From: "work", To: "gate", Attrs: map[string]string{}},
			{From: "gate", To: "exit", Attrs: map[string]string{}},
			{From: "orphan_fix", To: "exit", Attrs: map[string]string{}},
		},
	}
	diags := Validate(g)
	found := findDiag(diags, "retry_path_to_gate")
	if found == nil {
		t.Fatal("expected retry_path_to_gate warning when retry target cannot reach goal gate")
	}
	if diff := cmp.Diff(SeverityWarning, found.Severity); diff != "" {
		t.Errorf("severity mismatch (-want +got):\n%s", diff)
	}
	if !strings.Contains(found.Message, "orphan_fix") {
		t.Errorf("expected message to mention retry target, got %q", found.Message)
	}
}

func TestValidate_RetryPathToGate_ValidPath(t *testing.T) {
	g := &dot.Graph{
		Name:  "GoodRetry",
		Attrs: map[string]string{},
		Nodes: []*dot.Node{
			{ID: "start", Attrs: map[string]string{"shape": "Mdiamond"}},
			{ID: "work", Attrs: map[string]string{"shape": "box", "prompt": "do work"}},
			{ID: "review", Attrs: map[string]string{"shape": "box", "prompt": "review"}},
			{ID: "gate", Attrs: map[string]string{"shape": "box", "prompt": "check", "goal_gate": "true", "retry_target": "review"}},
			{ID: "exit", Attrs: map[string]string{"shape": "Msquare"}},
		},
		Edges: []*dot.Edge{
			{From: "start", To: "work", Attrs: map[string]string{}},
			{From: "work", To: "review", Attrs: map[string]string{}},
			{From: "review", To: "gate", Attrs: map[string]string{}},
			{From: "gate", To: "exit", Attrs: map[string]string{}},
		},
	}
	diags := Validate(g)
	found := findDiag(diags, "retry_path_to_gate")
	if found != nil {
		t.Errorf("unexpected retry_path_to_gate for valid retry path: %s", found)
	}
}

func TestValidate_RetryPathToGate_NoRetryTarget(t *testing.T) {
	g := validGraph()
	g.Nodes[1].Attrs["goal_gate"] = "true"
	diags := Validate(g)
	found := findDiag(diags, "retry_path_to_gate")
	if found != nil {
		t.Errorf("unexpected retry_path_to_gate when no retry_target is set: %s", found)
	}
}

// --- Regression tests for actual DOT files ---

func projectRoot() string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "..", "..")
}

func TestValidate_ExamplePipelineV1_NoErrors(t *testing.T) {
	dotPath := filepath.Join(projectRoot(), "pipelines", "retroquest-returns.dot")
	src, err := os.ReadFile(dotPath)
	if err != nil {
		t.Skipf("skipping: pipeline file not found: %v", err)
	}
	g, err := dot.Parse(string(src))
	if err != nil {
		t.Fatalf("failed to parse %s: %v", dotPath, err)
	}
	diags := Validate(g)
	for _, d := range diags {
		t.Logf("[%s] %s", d.Severity, d)
	}
	if HasErrors(diags) {
		t.Fatal("pipeline has ERROR-severity diagnostics")
	}
}

func TestValidate_ExamplePipelineV2_NoErrors(t *testing.T) {
	dotPath := filepath.Join(projectRoot(), "pipelines", "retroquest-returns-v2.dot")
	src, err := os.ReadFile(dotPath)
	if err != nil {
		t.Skipf("skipping: pipeline file not found: %v", err)
	}
	g, err := dot.Parse(string(src))
	if err != nil {
		t.Fatalf("failed to parse %s: %v", dotPath, err)
	}
	diags := Validate(g)
	for _, d := range diags {
		t.Logf("[%s] %s", d.Severity, d)
	}
	if HasErrors(diags) {
		t.Fatal("pipeline has ERROR-severity diagnostics")
	}
}

func TestValidate_ExamplePipelineV2_ExpectedWarnings(t *testing.T) {
	dotPath := filepath.Join(projectRoot(), "pipelines", "retroquest-returns-v2.dot")
	src, err := os.ReadFile(dotPath)
	if err != nil {
		t.Skipf("skipping: pipeline file not found: %v", err)
	}
	g, err := dot.Parse(string(src))
	if err != nil {
		t.Fatalf("failed to parse %s: %v", dotPath, err)
	}
	diags := Validate(g)

	noRecovery := findAllDiags(diags, "no_fail_recovery")
	if len(noRecovery) == 0 {
		t.Error("expected at least one no_fail_recovery warning (linear pipeline)")
	}

	linearInfo := findDiag(diags, "linear_no_conditions")
	if linearInfo == nil {
		t.Error("expected linear_no_conditions info (pipeline has no conditional edges)")
	}
}

func TestValidate_ExamplePipelineV3_NoErrors(t *testing.T) {
	dotPath := filepath.Join(projectRoot(), "pipelines", "retroquest-returns-v3.dot")
	src, err := os.ReadFile(dotPath)
	if err != nil {
		t.Skipf("skipping: pipeline file not found: %v", err)
	}
	g, err := dot.Parse(string(src))
	if err != nil {
		t.Fatalf("failed to parse %s: %v", dotPath, err)
	}
	diags := Validate(g)
	for _, d := range diags {
		t.Logf("[%s] %s", d.Severity, d)
	}
	if HasErrors(diags) {
		t.Fatal("pipeline has ERROR-severity diagnostics")
	}
}

func TestValidate_ExamplePipelineV3_ExpectedWarnings(t *testing.T) {
	dotPath := filepath.Join(projectRoot(), "pipelines", "retroquest-returns-v3.dot")
	src, err := os.ReadFile(dotPath)
	if err != nil {
		t.Skipf("skipping: pipeline file not found: %v", err)
	}
	g, err := dot.Parse(string(src))
	if err != nil {
		t.Fatalf("failed to parse %s: %v", dotPath, err)
	}
	diags := Validate(g)

	noRecovery := findAllDiags(diags, "no_fail_recovery")
	if len(noRecovery) == 0 {
		t.Error("expected at least one no_fail_recovery warning (linear pipeline)")
	}

	linearInfo := findDiag(diags, "linear_no_conditions")
	if linearInfo == nil {
		t.Error("expected linear_no_conditions info (pipeline has no conditional edges)")
	}
}
