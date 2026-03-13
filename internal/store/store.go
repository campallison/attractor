// Package store provides persistence for pipeline run observability data.
// When a database URL is configured, run metadata, stage results, and notable
// events are recorded for cross-run analysis. When no database is available,
// NopRecorder silently discards all writes.
package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// RunRecorder is the interface for persisting pipeline run data.
type RunRecorder interface {
	StartRun(ctx context.Context, run PipelineRun) (uuid.UUID, error)
	FinishRun(ctx context.Context, id uuid.UUID, update RunFinish) error
	RecordStage(ctx context.Context, stage StageResult) (int64, error)
	RecordEvent(ctx context.Context, event StageEvent) error
	Close() error
}

// PipelineRun captures the initial metadata for a pipeline execution.
type PipelineRun struct {
	PipelineFile string
	GraphName    string
	Goal         string
	DefaultModel string
	ModelOverride string
	Simulate     bool
	DockerImage  string
	BudgetTokens int
	StagesTotal  int
}

// RunFinish captures the final state of a completed pipeline execution.
type RunFinish struct {
	FinishedAt       time.Time
	DurationMs       int
	Status           string
	FailureReason    string
	TotalInputTokens  int
	TotalOutputTokens int
	TotalTokens       int
	StagesCompleted   int
}

// StageResult captures the outcome of a single pipeline stage.
type StageResult struct {
	RunID                 uuid.UUID
	NodeID                string
	Sequence              int
	Model                 string
	Status                string
	FailureReason         string
	ExhaustionReason      string
	Rounds                int
	InputTokens           int
	OutputTokens          int
	TotalTokens           int
	DurationMs            int
	PromptLength          int
	ResponseLength        int
	FilesAdded            int
	FilesModified         int
	FilesRemoved          int
	FilesUnchanged        int
	ScratchSummaryProduced bool
	BuildGateAttempts     int
	BuildGatePassed       *bool // nil when no build gate configured
	EngineAttempts        int   // engine-level handler invocations (1 = no retries)
}

// StageEvent captures a notable moment within a stage execution.
type StageEvent struct {
	StageID   int64
	RunID     uuid.UUID
	EventType string
	Round     int
	Detail    string
}

// NopRecorder silently discards all writes. Used when no database is configured.
type NopRecorder struct{}

func (NopRecorder) StartRun(_ context.Context, _ PipelineRun) (uuid.UUID, error) {
	return uuid.New(), nil
}
func (NopRecorder) FinishRun(_ context.Context, _ uuid.UUID, _ RunFinish) error { return nil }
func (NopRecorder) RecordStage(_ context.Context, _ StageResult) (int64, error) { return 0, nil }
func (NopRecorder) RecordEvent(_ context.Context, _ StageEvent) error           { return nil }
func (NopRecorder) Close() error                                                { return nil }
