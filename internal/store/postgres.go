package store

import (
	"context"
	_ "embed"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var schemaDDL string

// PostgresStore implements RunRecorder backed by a PostgreSQL database.
type PostgresStore struct {
	pool *pgxpool.Pool
}

// NewPostgresStore connects to the database and runs schema migration.
func NewPostgresStore(ctx context.Context, databaseURL string) (*PostgresStore, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("store: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	s := &PostgresStore{pool: pool}
	if err := s.migrate(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: migrate: %w", err)
	}
	slog.Info("store.connected", "url_redacted", true)
	return s, nil
}

func (s *PostgresStore) migrate(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, schemaDDL)
	return err
}

func (s *PostgresStore) StartRun(ctx context.Context, run PipelineRun) (uuid.UUID, error) {
	id := uuid.New()
	_, err := s.pool.Exec(ctx, `
		INSERT INTO pipeline_runs (
			id, pipeline_file, graph_name, goal, default_model,
			model_override, simulate, docker_image, budget_tokens, stages_total
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		id, run.PipelineFile, run.GraphName, run.Goal, run.DefaultModel,
		nullIfEmpty(run.ModelOverride), run.Simulate, run.DockerImage,
		run.BudgetTokens, run.StagesTotal,
	)
	if err != nil {
		return uuid.Nil, fmt.Errorf("store: start run: %w", err)
	}
	return id, nil
}

func (s *PostgresStore) FinishRun(ctx context.Context, id uuid.UUID, update RunFinish) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE pipeline_runs SET
			finished_at = $2, duration_ms = $3, status = $4, failure_reason = $5,
			total_input_tokens = $6, total_output_tokens = $7, total_tokens = $8,
			stages_completed = $9
		WHERE id = $1`,
		id, update.FinishedAt, update.DurationMs, update.Status,
		nullIfEmpty(update.FailureReason),
		update.TotalInputTokens, update.TotalOutputTokens, update.TotalTokens,
		update.StagesCompleted,
	)
	if err != nil {
		return fmt.Errorf("store: finish run: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("store: finish run: no row found for id %s", id)
	}
	return nil
}

func (s *PostgresStore) RecordStage(ctx context.Context, stage StageResult) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO stage_results (
			run_id, node_id, sequence, model, status, failure_reason,
			exhaustion_reason, rounds, input_tokens, output_tokens, total_tokens,
			duration_ms, prompt_length, response_length,
			files_added, files_modified, files_removed, files_unchanged,
			scratch_summary_produced, build_gate_attempts, build_gate_passed
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)
		RETURNING id`,
		stage.RunID, stage.NodeID, stage.Sequence, stage.Model, stage.Status,
		nullIfEmpty(stage.FailureReason), nullIfEmpty(stage.ExhaustionReason),
		stage.Rounds, stage.InputTokens, stage.OutputTokens, stage.TotalTokens,
		stage.DurationMs, stage.PromptLength, stage.ResponseLength,
		stage.FilesAdded, stage.FilesModified, stage.FilesRemoved, stage.FilesUnchanged,
		stage.ScratchSummaryProduced, stage.BuildGateAttempts, stage.BuildGatePassed,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("store: record stage: %w", err)
	}
	return id, nil
}

func (s *PostgresStore) RecordEvent(ctx context.Context, event StageEvent) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO stage_events (stage_id, run_id, event_type, round, detail)
		VALUES ($1, $2, $3, $4, $5)`,
		event.StageID, event.RunID, event.EventType, event.Round,
		nullIfEmpty(event.Detail),
	)
	if err != nil {
		return fmt.Errorf("store: record event: %w", err)
	}
	return nil
}

func (s *PostgresStore) Close() error {
	s.pool.Close()
	return nil
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
