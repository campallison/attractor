package pipeline

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/campallison/attractor/internal/dot"
)

// RunConfig holds the configuration for a pipeline run.
type RunConfig struct {
	Graph         *dot.Graph
	LogsRoot      string
	Registry      *HandlerRegistry
	MaxIterations int // 0 means use default (1000)
}

// RunResult is the final outcome of a pipeline execution.
type RunResult struct {
	Status         StageStatus
	CompletedNodes []string
	NodeOutcomes   map[string]Outcome
	FailureReason  string
	Warnings       []string
}

// Run executes a parsed and validated pipeline graph from start to exit. It
// implements the core traversal loop from spec Section 3.2.
func Run(cfg RunConfig) (RunResult, error) {
	g := cfg.Graph
	logsRoot := cfg.LogsRoot
	registry := cfg.Registry

	_ = os.MkdirAll(logsRoot, 0o755)

	ctx := NewContext()
	mirrorGraphAttributes(g, ctx)

	var completedNodes []string
	var warnings []string
	nodeOutcomes := make(map[string]Outcome)
	nodeRetries := make(map[string]int)

	startNode := g.FindStartNode()
	if startNode == nil {
		return RunResult{}, fmt.Errorf("no start node found")
	}

	current := startNode

	maxIter := cfg.MaxIterations
	if maxIter <= 0 {
		maxIter = 1000
	}
	iterations := 0

	for {
		iterations++
		if iterations > maxIter {
			return RunResult{
				Status:         StatusFail,
				CompletedNodes: completedNodes,
				NodeOutcomes:   nodeOutcomes,
				FailureReason:  fmt.Sprintf("max iterations (%d) exceeded -- possible cycle", maxIter),
				Warnings:       warnings,
			}, nil
		}

		// Step 1: Check for terminal node.
		if isTerminal(current) {
			gateOK, failedGate := checkGoalGates(g, nodeOutcomes)
			if !gateOK && failedGate != nil {
				retryTarget := getRetryTarget(failedGate, g)
				if retryTarget != "" {
					current = g.NodeByID(retryTarget)
					if current == nil {
						return RunResult{}, fmt.Errorf("retry target %q not found", retryTarget)
					}
					continue
				}
				return RunResult{
					Status:         StatusFail,
					CompletedNodes: completedNodes,
					NodeOutcomes:   nodeOutcomes,
					FailureReason:  fmt.Sprintf("goal gate %q unsatisfied and no retry target", failedGate.ID),
					Warnings:       warnings,
				}, nil
			}
			return RunResult{
				Status:         StatusSuccess,
				CompletedNodes: completedNodes,
				NodeOutcomes:   nodeOutcomes,
				Warnings:       warnings,
			}, nil
		}

		// Step 2: Execute node handler with retry policy.
		ctx.Set("current_node", current.ID)
		handler := registry.Resolve(current)
		outcome := executeWithRetry(handler, current, ctx, g, logsRoot, nodeRetries)

		// Step 3: Record completion.
		completedNodes = append(completedNodes, current.ID)
		nodeOutcomes[current.ID] = outcome

		// Step 4: Apply context updates.
		ctx.ApplyUpdates(outcome.ContextUpdates)
		ctx.Set("outcome", string(outcome.Status))
		if outcome.PreferredLabel != "" {
			ctx.Set("preferred_label", outcome.PreferredLabel)
		}
		ctx.AppendLog(fmt.Sprintf("node %s: %s", current.ID, outcome.Status))

		// Step 5: Save checkpoint.
		cp := NewCheckpoint(current.ID, completedNodes, nodeRetries, ctx)
		cpPath := filepath.Join(logsRoot, "checkpoint.json")
		if err := cp.Save(cpPath); err != nil {
			warnings = append(warnings, fmt.Sprintf("checkpoint save failed: %v", err))
		}

		// Step 6: Select next edge.
		nextEdge := SelectEdge(current.ID, outcome, ctx, g)
		if nextEdge == nil {
			if outcome.Status == StatusFail {
				return RunResult{
					Status:         StatusFail,
					CompletedNodes: completedNodes,
					NodeOutcomes:   nodeOutcomes,
					FailureReason:  fmt.Sprintf("node %q failed with no outgoing fail edge", current.ID),
					Warnings:       warnings,
				}, nil
			}
			// No more edges -- pipeline complete.
			return RunResult{
				Status:         StatusSuccess,
				CompletedNodes: completedNodes,
				NodeOutcomes:   nodeOutcomes,
				Warnings:       warnings,
			}, nil
		}

		// Step 7: Handle loop_restart (Phase 1: not implemented, treat as normal edge).

		// Step 8: Advance to next node.
		next := g.NodeByID(nextEdge.To)
		if next == nil {
			return RunResult{}, fmt.Errorf("edge target %q not found", nextEdge.To)
		}
		current = next
	}
}

// --- Edge selection (spec Section 3.3) ---

// SelectEdge implements the 5-step edge selection algorithm from the spec.
func SelectEdge(nodeID string, outcome Outcome, ctx *Context, g *dot.Graph) *dot.Edge {
	edges := g.OutgoingEdges(nodeID)
	if len(edges) == 0 {
		return nil
	}

	// Step 1: Condition-matching edges.
	var condMatched []*dot.Edge
	for _, e := range edges {
		cond := e.Condition()
		if cond != "" && EvaluateCondition(cond, outcome, ctx) {
			condMatched = append(condMatched, e)
		}
	}
	if len(condMatched) > 0 {
		return bestByWeightThenLexical(condMatched)
	}

	// Step 2: Preferred label match.
	if outcome.PreferredLabel != "" {
		normPref := normalizeLabel(outcome.PreferredLabel)
		for _, e := range edges {
			if normalizeLabel(e.EdgeLabel()) == normPref {
				return e
			}
		}
	}

	// Step 3: Suggested next IDs.
	if len(outcome.SuggestedNextIDs) > 0 {
		for _, suggestedID := range outcome.SuggestedNextIDs {
			for _, e := range edges {
				if e.To == suggestedID {
					return e
				}
			}
		}
	}

	// Step 4 & 5: Weight with lexical tiebreak (unconditional edges only).
	var unconditional []*dot.Edge
	for _, e := range edges {
		if e.Condition() == "" {
			unconditional = append(unconditional, e)
		}
	}
	if len(unconditional) > 0 {
		return bestByWeightThenLexical(unconditional)
	}

	// Fallback: any edge.
	return bestByWeightThenLexical(edges)
}

func bestByWeightThenLexical(edges []*dot.Edge) *dot.Edge {
	if len(edges) == 0 {
		return nil
	}
	sorted := make([]*dot.Edge, len(edges))
	copy(sorted, edges)
	sort.Slice(sorted, func(i, j int) bool {
		wi, wj := sorted[i].Weight(), sorted[j].Weight()
		if wi != wj {
			return wi > wj // higher weight first
		}
		return sorted[i].To < sorted[j].To // lexical tiebreak
	})
	return sorted[0]
}

// normalizeLabel lowercases, trims whitespace, and strips accelerator prefixes
// like "[Y] ", "Y) ", "Y - ".
func normalizeLabel(label string) string {
	label = strings.TrimSpace(strings.ToLower(label))
	// Strip [K] prefix
	if len(label) >= 4 && label[0] == '[' && label[2] == ']' && label[3] == ' ' {
		label = strings.TrimSpace(label[4:])
	}
	// Strip K) prefix
	if len(label) >= 3 && label[1] == ')' && label[2] == ' ' {
		label = strings.TrimSpace(label[3:])
	}
	// Strip K - prefix
	if len(label) >= 4 && label[1] == ' ' && label[2] == '-' && label[3] == ' ' {
		label = strings.TrimSpace(label[4:])
	}
	return label
}

// --- Goal gate enforcement (spec Section 3.4) ---

func checkGoalGates(g *dot.Graph, nodeOutcomes map[string]Outcome) (bool, *dot.Node) {
	for nodeID, outcome := range nodeOutcomes {
		node := g.NodeByID(nodeID)
		if node == nil {
			continue
		}
		if node.GoalGate() && !outcome.Status.IsSuccess() {
			return false, node
		}
	}
	return true, nil
}

func getRetryTarget(node *dot.Node, g *dot.Graph) string {
	if rt := node.RetryTarget(); rt != "" {
		return rt
	}
	if frt := node.FallbackRetryTarget(); frt != "" {
		return frt
	}
	if rt := g.Attrs["retry_target"]; rt != "" {
		return rt
	}
	if frt := g.Attrs["fallback_retry_target"]; frt != "" {
		return frt
	}
	return ""
}

func isTerminal(node *dot.Node) bool {
	return node.Shape() == "Msquare"
}

// --- Retry logic (spec Section 3.5) ---

const maxRetriesCap = 100

func executeWithRetry(h Handler, node *dot.Node, ctx *Context, g *dot.Graph, logsRoot string, nodeRetries map[string]int) Outcome {
	maxRetries := node.MaxRetries()
	graphDefault := 0
	if v, ok := g.Attrs["default_max_retry"]; ok {
		if parsed, err := fmt.Sscanf(v, "%d", &graphDefault); parsed == 1 && err == nil && maxRetries == 0 {
			maxRetries = graphDefault
		}
	}
	if maxRetries > maxRetriesCap {
		maxRetries = maxRetriesCap
	}
	maxAttempts := maxRetries + 1

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		outcome := h.Execute(node, ctx, g, logsRoot)

		if outcome.Status.IsSuccess() {
			nodeRetries[node.ID] = 0
			return outcome
		}

		if outcome.Status == StatusRetry && attempt < maxAttempts {
			nodeRetries[node.ID] = attempt
			delay := backoffDelay(attempt)
			time.Sleep(delay)
			continue
		}

		if outcome.Status == StatusFail {
			if attempt < maxAttempts {
				nodeRetries[node.ID] = attempt
				delay := backoffDelay(attempt)
				time.Sleep(delay)
				continue
			}
			if node.AllowPartial() {
				return Outcome{
					Status: StatusPartialSuccess,
					Notes:  "retries exhausted, partial accepted",
				}
			}
			return outcome
		}

		// For RETRY on last attempt:
		if outcome.Status == StatusRetry && attempt >= maxAttempts {
			if node.AllowPartial() {
				return Outcome{
					Status: StatusPartialSuccess,
					Notes:  "retries exhausted, partial accepted",
				}
			}
			return Outcome{
				Status:        StatusFail,
				FailureReason: "max retries exceeded",
			}
		}

		return outcome
	}

	return Outcome{Status: StatusFail, FailureReason: "max retries exceeded"}
}

// backoffDelay calculates exponential backoff with jitter for the given attempt
// number (1-indexed). Uses the "standard" preset: initial=200ms, factor=2.0,
// max=60s, jitter=true.
func backoffDelay(attempt int) time.Duration {
	const (
		initialDelayMs = 200
		backoffFactor  = 2.0
		maxDelayMs     = 60000
	)
	delay := float64(initialDelayMs) * math.Pow(backoffFactor, float64(attempt-1))
	if delay > maxDelayMs {
		delay = maxDelayMs
	}
	// Jitter: multiply by random factor between 0.5 and 1.5
	jitter := 0.5 + rand.Float64() //nolint:gosec
	delay *= jitter
	return time.Duration(delay) * time.Millisecond
}

// mirrorGraphAttributes copies graph-level attributes into the context under
// the "graph." namespace, as specified by the engine initialization step.
func mirrorGraphAttributes(g *dot.Graph, ctx *Context) {
	for k, v := range g.Attrs {
		ctx.Set("graph."+k, v)
	}
}
