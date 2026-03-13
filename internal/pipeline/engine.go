package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/campallison/attractor/internal/dot"
	"github.com/campallison/attractor/internal/store"
	"github.com/google/uuid"
)

// RunConfig holds the configuration for a pipeline run.
type RunConfig struct {
	Graph           *dot.Graph
	LogsRoot        string
	Registry        *HandlerRegistry
	MaxIterations   int // 0 means use default (1000)
	MaxBudgetTokens int // 0 means no limit

	Recorder store.RunRecorder // nil-safe: uses NopRecorder when nil
	RunID    uuid.UUID         // set by caller after StartRun; zero means no recording
}

// RunResult is the final outcome of a pipeline execution.
type RunResult struct {
	Status         StageStatus
	CompletedNodes []string
	NodeOutcomes   map[string]Outcome
	FailureReason  string
	Warnings       []string
	TotalUsage     StageUsage
	StageUsages    map[string]*StageUsage
}

// Run executes a parsed and validated pipeline graph from start to exit. It
// implements the core traversal loop from spec Section 3.2. The provided
// context controls the run's lifecycle: cancellation stops the pipeline in
// bounded time.
func Run(ctx context.Context, cfg RunConfig) (RunResult, error) {
	g := cfg.Graph
	logsRoot := cfg.LogsRoot
	registry := cfg.Registry

	_ = os.MkdirAll(logsRoot, 0o755)

	pctx := NewContext()
	mirrorGraphAttributes(g, pctx)

	var completedNodes []string
	var warnings []string
	var totalUsage StageUsage
	nodeOutcomes := make(map[string]Outcome)
	nodeRetries := make(map[string]int)
	stageUsages := make(map[string]*StageUsage)
	stageSequence := 0

	recorder := cfg.Recorder
	if recorder == nil {
		recorder = store.NopRecorder{}
	}

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

	// Track budget threshold crossings (50%, 75%) to warn once each.
	budgetWarned50 := false
	budgetWarned75 := false

	for {
		if err := ctx.Err(); err != nil {
			slog.Warn("pipeline.canceled", "reason", err, "completed_nodes", len(completedNodes))
			return RunResult{
				Status:         StatusFail,
				CompletedNodes: completedNodes,
				NodeOutcomes:   nodeOutcomes,
				FailureReason:  fmt.Sprintf("pipeline canceled: %v", err),
				Warnings:       warnings,
				TotalUsage:     totalUsage,
				StageUsages:    stageUsages,
			}, nil
		}

		iterations++
		if iterations > maxIter {
			slog.Error("pipeline.max_iterations", "iterations", maxIter)
			return RunResult{
				Status:         StatusFail,
				CompletedNodes: completedNodes,
				NodeOutcomes:   nodeOutcomes,
				FailureReason:  fmt.Sprintf("max iterations (%d) exceeded -- possible cycle", maxIter),
				Warnings:       warnings,
				TotalUsage:     totalUsage,
				StageUsages:    stageUsages,
			}, nil
		}

		// Step 1: Check for terminal node.
		if isTerminal(current) {
			slog.Info("pipeline.terminal", "node", current.ID)
			gateOK, failedGate := checkGoalGates(g, nodeOutcomes)
			if !gateOK && failedGate != nil {
				retryTarget := getRetryTarget(failedGate, g)
				if retryTarget != "" {
					slog.Warn("pipeline.goal_gate.retry", "gate", failedGate.ID, "retry_target", retryTarget)
					current = g.NodeByID(retryTarget)
					if current == nil {
						return RunResult{}, fmt.Errorf("retry target %q not found", retryTarget)
					}
					continue
				}
				slog.Error("pipeline.goal_gate.fail", "gate", failedGate.ID)
				return RunResult{
					Status:         StatusFail,
					CompletedNodes: completedNodes,
					NodeOutcomes:   nodeOutcomes,
					FailureReason:  fmt.Sprintf("goal gate %q unsatisfied and no retry target", failedGate.ID),
					Warnings:       warnings,
					TotalUsage:     totalUsage,
					StageUsages:    stageUsages,
				}, nil
			}
			slog.Info("pipeline.success", "completed_nodes", len(completedNodes), "total_tokens", totalUsage.TotalTokens)
			return RunResult{
				Status:         StatusSuccess,
				CompletedNodes: completedNodes,
				NodeOutcomes:   nodeOutcomes,
				Warnings:       warnings,
				TotalUsage:     totalUsage,
				StageUsages:    stageUsages,
			}, nil
		}

		// Step 2: Execute node handler with retry policy.
		nodeModel := current.Model()
		slog.Info("pipeline.node.start", "node", current.ID, "model", nodeModel)
		nodeStart := time.Now()

		pctx.Set("current_node", current.ID)
		handler := registry.Resolve(current)
		outcome := executeWithRetry(ctx, handler, current, pctx, g, logsRoot, nodeRetries)

		nodeDuration := time.Since(nodeStart)

		// Step 3: Record completion.
		completedNodes = append(completedNodes, current.ID)
		nodeOutcomes[current.ID] = outcome
		stageSequence++

		if outcome.Usage != nil {
			stageUsages[current.ID] = outcome.Usage
			totalUsage.InputTokens += outcome.Usage.InputTokens
			totalUsage.OutputTokens += outcome.Usage.OutputTokens
			totalUsage.TotalTokens += outcome.Usage.TotalTokens
			totalUsage.Rounds += outcome.Usage.Rounds
		}

		logAttrs := []any{
			"node", current.ID,
			"outcome", string(outcome.Status),
			"duration", nodeDuration.Round(time.Second).String(),
		}
		if outcome.Usage != nil {
			logAttrs = append(logAttrs, "rounds", outcome.Usage.Rounds, "tokens", outcome.Usage.TotalTokens)
		}
		if outcome.Status.IsSuccess() {
			slog.Info("pipeline.node.done", logAttrs...)
		} else {
			logAttrs = append(logAttrs, "failure_reason", outcome.FailureReason)
			slog.Warn("pipeline.node.done", logAttrs...)
		}

		// Record stage to observability database.
		recordStageResult(ctx, recorder, cfg.RunID, current, outcome, stageSequence, nodeDuration)

		// Budget threshold warnings.
		if cfg.MaxBudgetTokens > 0 {
			pct := float64(totalUsage.TotalTokens) / float64(cfg.MaxBudgetTokens) * 100
			if pct >= 75 && !budgetWarned75 {
				slog.Warn("pipeline.budget", "used", totalUsage.TotalTokens, "max", cfg.MaxBudgetTokens, "pct", int(pct))
				budgetWarned75 = true
			} else if pct >= 50 && !budgetWarned50 {
				slog.Warn("pipeline.budget", "used", totalUsage.TotalTokens, "max", cfg.MaxBudgetTokens, "pct", int(pct))
				budgetWarned50 = true
			}
		}

		if cfg.MaxBudgetTokens > 0 && totalUsage.TotalTokens > cfg.MaxBudgetTokens {
			slog.Error("pipeline.budget.exceeded", "used", totalUsage.TotalTokens, "max", cfg.MaxBudgetTokens)
			return RunResult{
				Status:         StatusFail,
				CompletedNodes: completedNodes,
				NodeOutcomes:   nodeOutcomes,
				FailureReason:  fmt.Sprintf("budget cap exceeded: %d total tokens used (max %d)", totalUsage.TotalTokens, cfg.MaxBudgetTokens),
				Warnings:       warnings,
				TotalUsage:     totalUsage,
				StageUsages:    stageUsages,
			}, nil
		}

		// Step 4: Apply context updates.
		pctx.ApplyUpdates(outcome.ContextUpdates)
		pctx.Set("outcome", string(outcome.Status))
		if outcome.PreferredLabel != "" {
			pctx.Set("preferred_label", outcome.PreferredLabel)
		}
		pctx.AppendLog(fmt.Sprintf("node %s: %s", current.ID, outcome.Status))

		// Step 5: Save checkpoint.
		cp := NewCheckpoint(current.ID, completedNodes, nodeRetries, pctx)
		cpPath := filepath.Join(logsRoot, "checkpoint.json")
		if err := cp.Save(cpPath); err != nil {
			slog.Warn("pipeline.checkpoint.fail", "error", err)
			warnings = append(warnings, fmt.Sprintf("checkpoint save failed: %v", err))
		} else {
			slog.Info("pipeline.checkpoint", "node", current.ID, "completed", len(completedNodes))
		}

		// Step 6: Select next edge.
		nextEdge := SelectEdge(current.ID, outcome, pctx, g)
		if nextEdge == nil {
			if outcome.Status == StatusFail {
				slog.Error("pipeline.node.fail.terminal", "node", current.ID)
				return RunResult{
					Status:         StatusFail,
					CompletedNodes: completedNodes,
					NodeOutcomes:   nodeOutcomes,
					FailureReason:  fmt.Sprintf("node %q failed with no outgoing fail edge", current.ID),
					Warnings:       warnings,
					TotalUsage:     totalUsage,
					StageUsages:    stageUsages,
				}, nil
			}
			slog.Info("pipeline.complete", "node", current.ID)
			return RunResult{
				Status:         StatusSuccess,
				CompletedNodes: completedNodes,
				NodeOutcomes:   nodeOutcomes,
				Warnings:       warnings,
				TotalUsage:     totalUsage,
				StageUsages:    stageUsages,
			}, nil
		}

		// Halt if a failed node would follow an unconditional edge. Conditional
		// edges (e.g. condition="outcome=fail") are explicit recovery paths and
		// are allowed to proceed; unconditional edges should not silently carry
		// failures forward.
		if outcome.Status == StatusFail && nextEdge.Condition() == "" {
			slog.Error("pipeline.node.fail.unconditional", "node", current.ID, "next", nextEdge.To)
			return RunResult{
				Status:         StatusFail,
				CompletedNodes: completedNodes,
				NodeOutcomes:   nodeOutcomes,
				FailureReason:  fmt.Sprintf("node %q failed; halting (unconditional edge to %q)", current.ID, nextEdge.To),
				Warnings:       warnings,
				TotalUsage:     totalUsage,
				StageUsages:    stageUsages,
			}, nil
		}

		slog.Info("pipeline.edge", "from", current.ID, "to", nextEdge.To)

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

func executeWithRetry(ctx context.Context, h Handler, node *dot.Node, pctx *Context, g *dot.Graph, logsRoot string, nodeRetries map[string]int) Outcome {
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
		if err := ctx.Err(); err != nil {
			return Outcome{
				Status:        StatusFail,
				FailureReason: fmt.Sprintf("canceled before attempt %d: %v", attempt, err),
			}
		}
		if attempt > 1 {
			slog.Warn("pipeline.retry", "node", node.ID, "attempt", attempt, "max_attempts", maxAttempts)
		}
		outcome := h.Execute(ctx, node, pctx, g, logsRoot)

		if outcome.Status.IsSuccess() {
			nodeRetries[node.ID] = 0
			return outcome
		}

		if outcome.Status == StatusRetry && attempt < maxAttempts {
			nodeRetries[node.ID] = attempt
			delay := backoffDelay(attempt)
			slog.Warn("pipeline.retry.backoff", "node", node.ID, "attempt", attempt, "delay", delay.String())
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return Outcome{
					Status:        StatusFail,
					FailureReason: fmt.Sprintf("canceled during retry backoff: %v", ctx.Err()),
				}
			}
			continue
		}

		if outcome.Status == StatusFail {
			if attempt < maxAttempts {
				nodeRetries[node.ID] = attempt
				delay := backoffDelay(attempt)
				slog.Warn("pipeline.retry.backoff", "node", node.ID, "attempt", attempt, "delay", delay.String(), "reason", outcome.FailureReason)
				select {
				case <-time.After(delay):
				case <-ctx.Done():
					return Outcome{
						Status:        StatusFail,
						FailureReason: fmt.Sprintf("canceled during retry backoff: %v", ctx.Err()),
					}
				}
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

// recordStageResult persists a stage outcome and any derived events to the
// observability database. Errors are logged but do not halt the pipeline.
func recordStageResult(ctx context.Context, rec store.RunRecorder, runID uuid.UUID, node *dot.Node, outcome Outcome, seq int, dur time.Duration) {
	dbCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	sr := store.StageResult{
		RunID:                  runID,
		NodeID:                 node.ID,
		Sequence:               seq,
		Status:                 string(outcome.Status),
		FailureReason:          outcome.FailureReason,
		ExhaustionReason:       outcome.ExhaustionReason,
		DurationMs:             int(dur.Milliseconds()),
		PromptLength:           outcome.PromptLength,
		ResponseLength:         outcome.ResponseLength,
		ScratchSummaryProduced: outcome.ScratchSummaryProduced,
		BuildGateAttempts:      outcome.BuildGateAttempts,
		BuildGatePassed:        outcome.BuildGatePassed,
	}
	if outcome.Usage != nil {
		sr.Model = outcome.Usage.Model
		sr.Rounds = outcome.Usage.Rounds
		sr.InputTokens = outcome.Usage.InputTokens
		sr.OutputTokens = outcome.Usage.OutputTokens
		sr.TotalTokens = outcome.Usage.TotalTokens
	}
	if outcome.FileDiffCounts != nil {
		sr.FilesAdded = outcome.FileDiffCounts.Added
		sr.FilesModified = outcome.FileDiffCounts.Modified
		sr.FilesRemoved = outcome.FileDiffCounts.Removed
		sr.FilesUnchanged = outcome.FileDiffCounts.Unchanged
	}

	stageID, err := rec.RecordStage(dbCtx, sr)
	if err != nil {
		slog.Warn("store.record_stage.error", "node", node.ID, "error", err)
		return
	}

	// Derive events from the outcome.
	var events []store.StageEvent

	if outcome.ExhaustionReason == ExhaustionReadLoop {
		events = append(events, store.StageEvent{
			StageID:   stageID,
			RunID:     runID,
			EventType: "read_loop_terminated",
			Detail:    outcome.FailureReason,
		})
	}
	if outcome.BuildGatePassed != nil {
		if *outcome.BuildGatePassed {
			events = append(events, store.StageEvent{
				StageID:   stageID,
				RunID:     runID,
				EventType: "build_gate_pass",
				Detail:    fmt.Sprintf("passed on attempt %d", outcome.BuildGateAttempts),
			})
		} else {
			events = append(events, store.StageEvent{
				StageID:   stageID,
				RunID:     runID,
				EventType: "build_gate_fail",
				Detail:    outcome.FailureReason,
			})
		}
	}
	if outcome.FileDiffCounts != nil && outcome.FileDiffCounts.Added == 0 &&
		outcome.FileDiffCounts.Modified == 0 && outcome.FileDiffCounts.Removed == 0 {
		events = append(events, store.StageEvent{
			StageID:   stageID,
			RunID:     runID,
			EventType: "empty_output",
			Detail:    "no filesystem changes detected",
		})
	}

	for _, evt := range events {
		if err := rec.RecordEvent(dbCtx, evt); err != nil {
			slog.Warn("store.record_event.error", "node", node.ID, "event", evt.EventType, "error", err)
		}
	}
}
