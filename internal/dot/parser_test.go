package dot

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestParse_MinimalDigraph(t *testing.T) {
	src := `digraph Simple { }`
	g, err := Parse(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if diff := cmp.Diff("Simple", g.Name); diff != "" {
		t.Errorf("name mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(0, len(g.Nodes)); diff != "" {
		t.Errorf("expected no nodes (-want +got):\n%s", diff)
	}
}

func TestParse_GraphAttributes(t *testing.T) {
	src := `digraph G {
		graph [goal="Run tests", label="Test Pipeline"]
		rankdir=LR
	}`
	g, err := Parse(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if diff := cmp.Diff("Run tests", g.Goal()); diff != "" {
		t.Errorf("goal mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("Test Pipeline", g.Label()); diff != "" {
		t.Errorf("label mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("LR", g.Attrs["rankdir"]); diff != "" {
		t.Errorf("rankdir mismatch (-want +got):\n%s", diff)
	}
}

func TestParse_NodesWithAttributes(t *testing.T) {
	src := `digraph G {
		start [shape=Mdiamond, label="Start"]
		plan  [shape=box, prompt="Plan the work"]
		exit  [shape=Msquare]
	}`
	g, err := Parse(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if diff := cmp.Diff(3, len(g.Nodes)); diff != "" {
		t.Fatalf("node count mismatch (-want +got):\n%s", diff)
	}

	start := g.NodeByID("start")
	if start == nil {
		t.Fatal("start node not found")
	}
	if diff := cmp.Diff("Mdiamond", start.Shape()); diff != "" {
		t.Errorf("start shape mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("Start", start.NodeLabel()); diff != "" {
		t.Errorf("start label mismatch (-want +got):\n%s", diff)
	}

	plan := g.NodeByID("plan")
	if plan == nil {
		t.Fatal("plan node not found")
	}
	if diff := cmp.Diff("Plan the work", plan.Prompt()); diff != "" {
		t.Errorf("plan prompt mismatch (-want +got):\n%s", diff)
	}
}

func TestParse_SimpleEdges(t *testing.T) {
	src := `digraph G {
		A -> B
		B -> C [label="next", condition="outcome=success"]
	}`
	g, err := Parse(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if diff := cmp.Diff(2, len(g.Edges)); diff != "" {
		t.Fatalf("edge count mismatch (-want +got):\n%s", diff)
	}
	e1 := g.Edges[0]
	if diff := cmp.Diff("A", e1.From); diff != "" {
		t.Errorf("edge 0 from mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("B", e1.To); diff != "" {
		t.Errorf("edge 0 to mismatch (-want +got):\n%s", diff)
	}

	e2 := g.Edges[1]
	if diff := cmp.Diff("next", e2.EdgeLabel()); diff != "" {
		t.Errorf("edge 1 label mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("outcome=success", e2.Condition()); diff != "" {
		t.Errorf("edge 1 condition mismatch (-want +got):\n%s", diff)
	}
}

func TestParse_ChainedEdges(t *testing.T) {
	src := `digraph G {
		start -> plan -> implement -> exit [label="next"]
	}`
	g, err := Parse(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if diff := cmp.Diff(3, len(g.Edges)); diff != "" {
		t.Fatalf("chained edge should produce 3 edges (-want +got):\n%s", diff)
	}

	wantEdges := []struct{ from, to, label string }{
		{"start", "plan", "next"},
		{"plan", "implement", "next"},
		{"implement", "exit", "next"},
	}
	for i, w := range wantEdges {
		e := g.Edges[i]
		if diff := cmp.Diff(w.from, e.From); diff != "" {
			t.Errorf("edge %d from mismatch (-want +got):\n%s", i, diff)
		}
		if diff := cmp.Diff(w.to, e.To); diff != "" {
			t.Errorf("edge %d to mismatch (-want +got):\n%s", i, diff)
		}
		if diff := cmp.Diff(w.label, e.EdgeLabel()); diff != "" {
			t.Errorf("edge %d label mismatch (-want +got):\n%s", i, diff)
		}
	}
}

func TestParse_NodeDefaults(t *testing.T) {
	src := `digraph G {
		node [shape=box, timeout="900s"]
		plan [label="Plan"]
		implement [label="Implement", timeout="1800s"]
	}`
	g, err := Parse(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	plan := g.NodeByID("plan")
	if plan == nil {
		t.Fatal("plan node not found")
	}
	if diff := cmp.Diff("box", plan.Shape()); diff != "" {
		t.Errorf("plan shape (from defaults) mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("900s", plan.GetAttr("timeout", "")); diff != "" {
		t.Errorf("plan timeout (from defaults) mismatch (-want +got):\n%s", diff)
	}

	implement := g.NodeByID("implement")
	if implement == nil {
		t.Fatal("implement node not found")
	}
	if diff := cmp.Diff("1800s", implement.GetAttr("timeout", "")); diff != "" {
		t.Errorf("implement timeout (override) mismatch (-want +got):\n%s", diff)
	}
}

func TestParse_EdgeDefaults(t *testing.T) {
	src := `digraph G {
		edge [weight=5]
		A -> B
		C -> D [weight=10]
	}`
	g, err := Parse(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	e1 := g.Edges[0]
	if diff := cmp.Diff(5, e1.Weight()); diff != "" {
		t.Errorf("edge AB weight (from default) mismatch (-want +got):\n%s", diff)
	}
	e2 := g.Edges[1]
	if diff := cmp.Diff(10, e2.Weight()); diff != "" {
		t.Errorf("edge CD weight (override) mismatch (-want +got):\n%s", diff)
	}
}

func TestParse_Subgraph(t *testing.T) {
	src := `digraph G {
		subgraph cluster_loop {
			node [timeout="900s"]
			Plan [label="Plan next step"]
			Implement [label="Implement", timeout="1800s"]
		}
		Other [label="Other"]
	}`
	g, err := Parse(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Nodes from subgraph are flattened into the parent graph.
	plan := g.NodeByID("Plan")
	if plan == nil {
		t.Fatal("Plan node not found")
	}
	if diff := cmp.Diff("900s", plan.GetAttr("timeout", "")); diff != "" {
		t.Errorf("Plan timeout (subgraph default) mismatch (-want +got):\n%s", diff)
	}

	impl := g.NodeByID("Implement")
	if impl == nil {
		t.Fatal("Implement node not found")
	}
	if diff := cmp.Diff("1800s", impl.GetAttr("timeout", "")); diff != "" {
		t.Errorf("Implement timeout (override) mismatch (-want +got):\n%s", diff)
	}

	// Subgraph defaults don't leak to outer scope.
	other := g.NodeByID("Other")
	if other == nil {
		t.Fatal("Other node not found")
	}
	if other.GetAttr("timeout", "none") != "none" {
		t.Errorf("subgraph defaults leaked to outer scope: timeout=%q", other.GetAttr("timeout", ""))
	}
}

func TestParse_Comments(t *testing.T) {
	src := `digraph G {
		// This is a line comment
		A [label="Node A"]
		/* Block comment
		   spanning lines */
		B [label="Node B"]
		A -> B // trailing comment
	}`
	g, err := Parse(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if diff := cmp.Diff(2, len(g.Nodes)); diff != "" {
		t.Errorf("node count mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(1, len(g.Edges)); diff != "" {
		t.Errorf("edge count mismatch (-want +got):\n%s", diff)
	}
}

func TestParse_BooleanAttributes(t *testing.T) {
	src := `digraph G {
		A [goal_gate=true, auto_status=false]
	}`
	g, err := Parse(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	a := g.NodeByID("A")
	if a == nil {
		t.Fatal("node A not found")
	}
	if diff := cmp.Diff(true, a.GoalGate()); diff != "" {
		t.Errorf("goal_gate mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(false, a.AutoStatus()); diff != "" {
		t.Errorf("auto_status mismatch (-want +got):\n%s", diff)
	}
}

func TestParse_IntegerAttributes(t *testing.T) {
	src := `digraph G {
		A [max_retries=3]
	}`
	g, err := Parse(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	a := g.NodeByID("A")
	if a == nil {
		t.Fatal("node A not found")
	}
	if diff := cmp.Diff(3, a.MaxRetries()); diff != "" {
		t.Errorf("max_retries mismatch (-want +got):\n%s", diff)
	}
}

func TestParse_EdgeLoopRestart(t *testing.T) {
	src := `digraph G {
		A -> B [loop_restart=true]
	}`
	g, err := Parse(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if diff := cmp.Diff(true, g.Edges[0].LoopRestart()); diff != "" {
		t.Errorf("loop_restart mismatch (-want +got):\n%s", diff)
	}
}

func TestParse_FullPipeline(t *testing.T) {
	src := `digraph Pipeline {
		graph [goal="Implement and validate a feature"]
		rankdir=LR
		node [shape=box, timeout="900s"]

		start     [shape=Mdiamond, label="Start"]
		exit      [shape=Msquare, label="Exit"]
		plan      [label="Plan", prompt="Plan the implementation"]
		implement [label="Implement", prompt="Implement the plan"]
		validate  [label="Validate", prompt="Run tests"]
		gate      [shape=diamond, label="Tests passing?"]

		start -> plan -> implement -> validate -> gate
		gate -> exit      [label="Yes", condition="outcome=success"]
		gate -> implement [label="No", condition="outcome!=success"]
	}`
	g, err := Parse(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if diff := cmp.Diff("Implement and validate a feature", g.Goal()); diff != "" {
		t.Errorf("goal mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(6, len(g.Nodes)); diff != "" {
		t.Errorf("node count mismatch (-want +got):\n%s", diff)
	}
	// 4 from the chain + 2 from gate
	if diff := cmp.Diff(6, len(g.Edges)); diff != "" {
		t.Errorf("edge count mismatch (-want +got):\n%s", diff)
	}

	startNode := g.FindStartNode()
	if startNode == nil {
		t.Fatal("start node not found")
	}
	if diff := cmp.Diff("start", startNode.ID); diff != "" {
		t.Errorf("start node ID mismatch (-want +got):\n%s", diff)
	}

	exitNode := g.FindExitNode()
	if exitNode == nil {
		t.Fatal("exit node not found")
	}
	if diff := cmp.Diff("exit", exitNode.ID); diff != "" {
		t.Errorf("exit node ID mismatch (-want +got):\n%s", diff)
	}

	// gate should have 2 outgoing edges.
	gateEdges := g.OutgoingEdges("gate")
	if diff := cmp.Diff(2, len(gateEdges)); diff != "" {
		t.Errorf("gate outgoing edge count mismatch (-want +got):\n%s", diff)
	}
}

func TestParse_SemicolonTerminated(t *testing.T) {
	src := `digraph G {
		A [label="Node A"];
		B [label="Node B"];
		A -> B;
	}`
	g, err := Parse(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if diff := cmp.Diff(2, len(g.Nodes)); diff != "" {
		t.Errorf("node count mismatch (-want +got):\n%s", diff)
	}
}

func TestParse_ImplicitNodeCreation(t *testing.T) {
	src := `digraph G {
		A -> B -> C
	}`
	g, err := Parse(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if diff := cmp.Diff(3, len(g.Nodes)); diff != "" {
		t.Errorf("edges should implicitly create nodes (-want +got):\n%s", diff)
	}
}

func TestParse_Errors(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{name: "not a digraph", src: `graph G { }`},
		{name: "missing graph name", src: `digraph { }`},
		{name: "missing brace", src: `digraph G A`},
		{name: "bad edge target", src: `digraph G { A -> }`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.src)
			if err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}
