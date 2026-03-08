package pipeline

import (
	"fmt"
	"strings"

	"github.com/campallison/attractor/internal/dot"
)

// Severity classifies the importance of a validation diagnostic.
type Severity int

const (
	SeverityError   Severity = iota // pipeline will not execute
	SeverityWarning                 // pipeline will execute but behavior may be unexpected
	SeverityInfo                    // informational note
)

func (s Severity) String() string {
	switch s {
	case SeverityError:
		return "ERROR"
	case SeverityWarning:
		return "WARNING"
	case SeverityInfo:
		return "INFO"
	default:
		return "UNKNOWN"
	}
}

// Diagnostic is a single validation finding.
type Diagnostic struct {
	Rule     string
	Severity Severity
	Message  string
	NodeID   string // related node (optional)
	EdgeFrom string // related edge source (optional)
	EdgeTo   string // related edge target (optional)
	Fix      string // suggested fix (optional)
}

func (d Diagnostic) String() string {
	loc := ""
	if d.NodeID != "" {
		loc = fmt.Sprintf(" [node %s]", d.NodeID)
	} else if d.EdgeFrom != "" {
		loc = fmt.Sprintf(" [edge %s->%s]", d.EdgeFrom, d.EdgeTo)
	}
	return fmt.Sprintf("%s: %s%s", d.Severity, d.Message, loc)
}

// Validate runs all built-in lint rules against the graph and returns
// diagnostics. Use ValidateOrError to reject graphs with error-severity issues.
func Validate(g *dot.Graph) []Diagnostic {
	var diags []Diagnostic
	diags = append(diags, checkStartNode(g)...)
	diags = append(diags, checkTerminalNode(g)...)
	diags = append(diags, checkStartNoIncoming(g)...)
	diags = append(diags, checkExitNoOutgoing(g)...)
	diags = append(diags, checkEdgeTargets(g)...)
	diags = append(diags, checkReachability(g)...)
	diags = append(diags, checkConditionSyntax(g)...)
	diags = append(diags, checkTypeKnown(g)...)
	diags = append(diags, checkRetryTargetExists(g)...)
	diags = append(diags, checkGoalGateHasRetry(g)...)
	diags = append(diags, checkPromptOnLLMNodes(g)...)
	diags = append(diags, checkMaxRetries(g)...)
	return diags
}

// ValidateOrError runs validation and returns an error if any ERROR-severity
// diagnostics are found. WARNING and INFO diagnostics are returned alongside.
func ValidateOrError(g *dot.Graph) ([]Diagnostic, error) {
	diags := Validate(g)
	var errs []string
	for _, d := range diags {
		if d.Severity == SeverityError {
			errs = append(errs, d.String())
		}
	}
	if len(errs) > 0 {
		return diags, fmt.Errorf("validation failed:\n  %s", strings.Join(errs, "\n  "))
	}
	return diags, nil
}

// HasErrors returns true if any diagnostic has ERROR severity.
func HasErrors(diags []Diagnostic) bool {
	for _, d := range diags {
		if d.Severity == SeverityError {
			return true
		}
	}
	return false
}

func checkStartNode(g *dot.Graph) []Diagnostic {
	start := g.FindStartNode()
	if start == nil {
		return []Diagnostic{{
			Rule:     "start_node",
			Severity: SeverityError,
			Message:  "pipeline must have exactly one start node (shape=Mdiamond)",
			Fix:      `add a node with shape=Mdiamond, e.g.: start [shape=Mdiamond]`,
		}}
	}
	return nil
}

func checkTerminalNode(g *dot.Graph) []Diagnostic {
	exit := g.FindExitNode()
	if exit == nil {
		return []Diagnostic{{
			Rule:     "terminal_node",
			Severity: SeverityError,
			Message:  "pipeline must have at least one exit node (shape=Msquare)",
			Fix:      `add a node with shape=Msquare, e.g.: exit [shape=Msquare]`,
		}}
	}
	return nil
}

func checkStartNoIncoming(g *dot.Graph) []Diagnostic {
	start := g.FindStartNode()
	if start == nil {
		return nil
	}
	if incoming := g.IncomingEdges(start.ID); len(incoming) > 0 {
		return []Diagnostic{{
			Rule:     "start_no_incoming",
			Severity: SeverityError,
			Message:  "start node must have no incoming edges",
			NodeID:   start.ID,
			Fix:      "remove edges pointing to the start node",
		}}
	}
	return nil
}

func checkExitNoOutgoing(g *dot.Graph) []Diagnostic {
	exit := g.FindExitNode()
	if exit == nil {
		return nil
	}
	if outgoing := g.OutgoingEdges(exit.ID); len(outgoing) > 0 {
		return []Diagnostic{{
			Rule:     "exit_no_outgoing",
			Severity: SeverityError,
			Message:  "exit node must have no outgoing edges",
			NodeID:   exit.ID,
			Fix:      "remove edges from the exit node",
		}}
	}
	return nil
}

func checkEdgeTargets(g *dot.Graph) []Diagnostic {
	nodeIDs := make(map[string]bool, len(g.Nodes))
	for _, n := range g.Nodes {
		nodeIDs[n.ID] = true
	}
	var diags []Diagnostic
	for _, e := range g.Edges {
		if !nodeIDs[e.From] {
			diags = append(diags, Diagnostic{
				Rule:     "edge_target_exists",
				Severity: SeverityError,
				Message:  fmt.Sprintf("edge source %q does not reference an existing node", e.From),
				EdgeFrom: e.From,
				EdgeTo:   e.To,
			})
		}
		if !nodeIDs[e.To] {
			diags = append(diags, Diagnostic{
				Rule:     "edge_target_exists",
				Severity: SeverityError,
				Message:  fmt.Sprintf("edge target %q does not reference an existing node", e.To),
				EdgeFrom: e.From,
				EdgeTo:   e.To,
			})
		}
	}
	return diags
}

func checkReachability(g *dot.Graph) []Diagnostic {
	start := g.FindStartNode()
	if start == nil {
		return nil
	}
	reachable := make(map[string]bool)
	var bfs func(id string)
	bfs = func(id string) {
		if reachable[id] {
			return
		}
		reachable[id] = true
		for _, e := range g.OutgoingEdges(id) {
			bfs(e.To)
		}
	}
	bfs(start.ID)

	var diags []Diagnostic
	for _, n := range g.Nodes {
		if !reachable[n.ID] {
			diags = append(diags, Diagnostic{
				Rule:     "reachability",
				Severity: SeverityError,
				Message:  fmt.Sprintf("node %q is not reachable from the start node", n.ID),
				NodeID:   n.ID,
				Fix:      "add an edge path from the start node to this node, or remove it",
			})
		}
	}
	return diags
}

func checkConditionSyntax(g *dot.Graph) []Diagnostic {
	var diags []Diagnostic
	for _, e := range g.Edges {
		cond := e.Condition()
		if cond == "" {
			continue
		}
		clauses := strings.Split(cond, "&&")
		for _, clause := range clauses {
			clause = strings.TrimSpace(clause)
			if clause == "" {
				continue
			}
			if !strings.Contains(clause, "=") {
				// Bare key is valid (truthy check)
				continue
			}
			// Must have a key and value around = or !=
			if idx := strings.Index(clause, "!="); idx >= 0 {
				key := strings.TrimSpace(clause[:idx])
				if key == "" {
					diags = append(diags, Diagnostic{
						Rule:     "condition_syntax",
						Severity: SeverityError,
						Message:  fmt.Sprintf("condition clause %q has empty key", clause),
						EdgeFrom: e.From,
						EdgeTo:   e.To,
					})
				}
				continue
			}
			if idx := strings.Index(clause, "="); idx >= 0 {
				key := strings.TrimSpace(clause[:idx])
				if key == "" {
					diags = append(diags, Diagnostic{
						Rule:     "condition_syntax",
						Severity: SeverityError,
						Message:  fmt.Sprintf("condition clause %q has empty key", clause),
						EdgeFrom: e.From,
						EdgeTo:   e.To,
					})
				}
			}
		}
	}
	return diags
}

// knownHandlerTypes are the handler types recognized in Phase 1.
var knownHandlerTypes = map[string]bool{
	"start":       true,
	"exit":        true,
	"codergen":    true,
	"conditional": true,
	"wait.human":  true,
	"parallel":    true,
	"tool":        true,
}

func checkTypeKnown(g *dot.Graph) []Diagnostic {
	var diags []Diagnostic
	for _, n := range g.Nodes {
		typ := n.Type()
		if typ == "" {
			continue
		}
		if !knownHandlerTypes[typ] {
			diags = append(diags, Diagnostic{
				Rule:     "type_known",
				Severity: SeverityWarning,
				Message:  fmt.Sprintf("node type %q is not a recognized handler type", typ),
				NodeID:   n.ID,
			})
		}
	}
	return diags
}

func checkRetryTargetExists(g *dot.Graph) []Diagnostic {
	nodeIDs := make(map[string]bool, len(g.Nodes))
	for _, n := range g.Nodes {
		nodeIDs[n.ID] = true
	}
	var diags []Diagnostic
	for _, n := range g.Nodes {
		if rt := n.RetryTarget(); rt != "" && !nodeIDs[rt] {
			diags = append(diags, Diagnostic{
				Rule:     "retry_target_exists",
				Severity: SeverityWarning,
				Message:  fmt.Sprintf("retry_target %q does not reference an existing node", rt),
				NodeID:   n.ID,
			})
		}
		if frt := n.FallbackRetryTarget(); frt != "" && !nodeIDs[frt] {
			diags = append(diags, Diagnostic{
				Rule:     "retry_target_exists",
				Severity: SeverityWarning,
				Message:  fmt.Sprintf("fallback_retry_target %q does not reference an existing node", frt),
				NodeID:   n.ID,
			})
		}
	}
	return diags
}

func checkGoalGateHasRetry(g *dot.Graph) []Diagnostic {
	var diags []Diagnostic
	for _, n := range g.Nodes {
		if n.GoalGate() {
			if n.RetryTarget() == "" && n.FallbackRetryTarget() == "" {
				graphRT := g.Attrs["retry_target"]
				graphFRT := g.Attrs["fallback_retry_target"]
				if graphRT == "" && graphFRT == "" {
					diags = append(diags, Diagnostic{
						Rule:     "goal_gate_has_retry",
						Severity: SeverityWarning,
						Message:  "goal_gate node has no retry_target; if it fails, the pipeline cannot recover",
						NodeID:   n.ID,
						Fix:      "add retry_target attribute to this node or to the graph",
					})
				}
			}
		}
	}
	return diags
}

// shapeToHandlerType maps shapes to their default handler types.
var shapeToHandlerType = map[string]string{
	"Mdiamond":      "start",
	"Msquare":       "exit",
	"box":           "codergen",
	"hexagon":       "wait.human",
	"diamond":       "conditional",
	"component":     "parallel",
	"tripleoctagon": "parallel.fan_in",
	"parallelogram": "tool",
	"house":         "stack.manager_loop",
}

func checkPromptOnLLMNodes(g *dot.Graph) []Diagnostic {
	var diags []Diagnostic
	for _, n := range g.Nodes {
		handlerType := n.Type()
		if handlerType == "" {
			handlerType = shapeToHandlerType[n.Shape()]
		}
		if handlerType == "codergen" {
			if n.Prompt() == "" && n.NodeLabel() == n.ID {
				diags = append(diags, Diagnostic{
					Rule:     "prompt_on_llm_nodes",
					Severity: SeverityWarning,
					Message:  "codergen node has no prompt or label; the LLM will have no instructions",
					NodeID:   n.ID,
					Fix:      "add a prompt or label attribute",
				})
			}
		}
	}
	return diags
}

const maxRetriesTolerance = 100

func checkMaxRetries(g *dot.Graph) []Diagnostic {
	var diags []Diagnostic
	for _, n := range g.Nodes {
		mr := n.MaxRetries()
		if mr > maxRetriesTolerance {
			diags = append(diags, Diagnostic{
				Rule:     "max_retries_cap",
				Severity: SeverityWarning,
				Message:  fmt.Sprintf("max_retries=%d exceeds cap (%d); will be clamped at runtime", mr, maxRetriesTolerance),
				NodeID:   n.ID,
				Fix:      fmt.Sprintf("set max_retries to %d or lower", maxRetriesTolerance),
			})
		}
	}
	return diags
}
