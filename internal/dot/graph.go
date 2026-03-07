// Package dot provides a parser and in-memory model for a strict subset of
// the Graphviz DOT language, as defined by the Attractor pipeline spec.
package dot

import (
	"strconv"
	"strings"
	"time"
)

// Graph is the top-level container produced by parsing a digraph DOT file.
type Graph struct {
	Name  string
	Attrs map[string]string // graph-level key=value attributes
	Nodes []*Node
	Edges []*Edge
}

// Node is a single node declaration with its parsed attributes.
type Node struct {
	ID    string
	Attrs map[string]string
}

// Edge is a directed edge between two nodes with its parsed attributes.
type Edge struct {
	From  string
	To    string
	Attrs map[string]string
}

// Goal returns the graph-level "goal" attribute.
func (g *Graph) Goal() string {
	return g.Attrs["goal"]
}

// Label returns the graph-level "label" attribute.
func (g *Graph) Label() string {
	return g.Attrs["label"]
}

// NodeByID returns the node with the given ID, or nil if not found.
func (g *Graph) NodeByID(id string) *Node {
	for _, n := range g.Nodes {
		if n.ID == id {
			return n
		}
	}
	return nil
}

// OutgoingEdges returns all edges whose From field matches the given node ID.
func (g *Graph) OutgoingEdges(nodeID string) []*Edge {
	var out []*Edge
	for _, e := range g.Edges {
		if e.From == nodeID {
			out = append(out, e)
		}
	}
	return out
}

// IncomingEdges returns all edges whose To field matches the given node ID.
func (g *Graph) IncomingEdges(nodeID string) []*Edge {
	var in []*Edge
	for _, e := range g.Edges {
		if e.To == nodeID {
			in = append(in, e)
		}
	}
	return in
}

// FindStartNode returns the node with shape=Mdiamond, falling back to ID
// "start" or "Start". Returns nil if no start node is found.
func (g *Graph) FindStartNode() *Node {
	for _, n := range g.Nodes {
		if n.Shape() == "Mdiamond" {
			return n
		}
	}
	for _, n := range g.Nodes {
		if n.ID == "start" || n.ID == "Start" {
			return n
		}
	}
	return nil
}

// FindExitNode returns the node with shape=Msquare, falling back to ID
// "exit", "Exit", "end", or "End". Returns nil if no exit node is found.
func (g *Graph) FindExitNode() *Node {
	for _, n := range g.Nodes {
		if n.Shape() == "Msquare" {
			return n
		}
	}
	for _, n := range g.Nodes {
		switch n.ID {
		case "exit", "Exit", "end", "End":
			return n
		}
	}
	return nil
}

// --- Node attribute accessors ---

// GetAttr returns the value of a node attribute, or the fallback if unset.
func (n *Node) GetAttr(key, fallback string) string {
	if v, ok := n.Attrs[key]; ok {
		return v
	}
	return fallback
}

// Shape returns the node's "shape" attribute, defaulting to "box".
func (n *Node) Shape() string { return n.GetAttr("shape", "box") }

// NodeLabel returns the node's "label" attribute, defaulting to the node ID.
func (n *Node) NodeLabel() string { return n.GetAttr("label", n.ID) }

// Type returns the explicit "type" attribute, or empty string.
func (n *Node) Type() string { return n.GetAttr("type", "") }

// Prompt returns the node's "prompt" attribute.
func (n *Node) Prompt() string { return n.GetAttr("prompt", "") }

// GoalGate returns true if the node has goal_gate=true.
func (n *Node) GoalGate() bool { return n.GetBool("goal_gate", false) }

// AutoStatus returns true if the node has auto_status=true.
func (n *Node) AutoStatus() bool { return n.GetBool("auto_status", false) }

// AllowPartial returns true if the node has allow_partial=true.
func (n *Node) AllowPartial() bool { return n.GetBool("allow_partial", false) }

// RetryTarget returns the node's retry_target attribute.
func (n *Node) RetryTarget() string { return n.GetAttr("retry_target", "") }

// FallbackRetryTarget returns the node's fallback_retry_target attribute.
func (n *Node) FallbackRetryTarget() string { return n.GetAttr("fallback_retry_target", "") }

// MaxRetries returns the node's max_retries attribute, defaulting to 0.
func (n *Node) MaxRetries() int { return n.GetInt("max_retries", 0) }

// GetInt returns a node attribute parsed as an integer, or the fallback.
func (n *Node) GetInt(key string, fallback int) int {
	v, ok := n.Attrs[key]
	if !ok {
		return fallback
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return i
}

// GetBool returns a node attribute parsed as a boolean, or the fallback.
func (n *Node) GetBool(key string, fallback bool) bool {
	v, ok := n.Attrs[key]
	if !ok {
		return fallback
	}
	switch strings.ToLower(v) {
	case "true":
		return true
	case "false":
		return false
	default:
		return fallback
	}
}

// GetDuration parses a duration attribute in the spec's format (e.g. "900s",
// "15m", "2h", "250ms", "1d"). Returns the fallback on missing or invalid values.
func (n *Node) GetDuration(key string, fallback time.Duration) time.Duration {
	v, ok := n.Attrs[key]
	if !ok {
		return fallback
	}
	return ParseDuration(v, fallback)
}

// --- Edge attribute accessors ---

// GetAttr returns the value of an edge attribute, or the fallback if unset.
func (e *Edge) GetAttr(key, fallback string) string {
	if v, ok := e.Attrs[key]; ok {
		return v
	}
	return fallback
}

// EdgeLabel returns the edge's "label" attribute.
func (e *Edge) EdgeLabel() string { return e.GetAttr("label", "") }

// Condition returns the edge's "condition" attribute.
func (e *Edge) Condition() string { return e.GetAttr("condition", "") }

// Weight returns the edge's "weight" attribute, defaulting to 0.
func (e *Edge) Weight() int {
	v, ok := e.Attrs["weight"]
	if !ok {
		return 0
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return 0
	}
	return i
}

// LoopRestart returns true if the edge has loop_restart=true.
func (e *Edge) LoopRestart() bool {
	switch strings.ToLower(e.GetAttr("loop_restart", "false")) {
	case "true":
		return true
	default:
		return false
	}
}

// --- Duration parsing ---

// ParseDuration parses a spec-format duration string (e.g. "900s", "15m",
// "250ms", "1d"). Returns the fallback on parse failure.
func ParseDuration(s string, fallback time.Duration) time.Duration {
	if strings.HasSuffix(s, "ms") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "ms"))
		if err != nil {
			return fallback
		}
		return time.Duration(n) * time.Millisecond
	}
	suffixes := []struct {
		suffix string
		unit   time.Duration
	}{
		{"d", 24 * time.Hour},
		{"h", time.Hour},
		{"m", time.Minute},
		{"s", time.Second},
	}
	for _, sf := range suffixes {
		if strings.HasSuffix(s, sf.suffix) {
			n, err := strconv.Atoi(strings.TrimSuffix(s, sf.suffix))
			if err != nil {
				return fallback
			}
			return time.Duration(n) * sf.unit
		}
	}
	return fallback
}
