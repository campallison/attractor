package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
)

func TestNopRecorder(t *testing.T) {
	var rec RunRecorder = NopRecorder{}
	ctx := context.Background()

	id, err := rec.StartRun(ctx, PipelineRun{PipelineFile: "test.dot"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if id == uuid.Nil {
		t.Error("expected non-nil UUID from NopRecorder")
	}

	if err := rec.FinishRun(ctx, id, RunFinish{Status: "success"}); err != nil {
		t.Fatalf("FinishRun: %v", err)
	}

	stageID, err := rec.RecordStage(ctx, StageResult{RunID: id, NodeID: "test"})
	if err != nil {
		t.Fatalf("RecordStage: %v", err)
	}
	if stageID != 0 {
		t.Errorf("expected stage ID 0 from NopRecorder, got %d", stageID)
	}

	if err := rec.RecordEvent(ctx, StageEvent{StageID: 0, RunID: id}); err != nil {
		t.Fatalf("RecordEvent: %v", err)
	}

	if err := rec.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestNopRecorder_ImplementsInterface(t *testing.T) {
	var _ RunRecorder = NopRecorder{}
	var _ RunRecorder = &NopRecorder{}
}

// TestPostgresStore runs integration tests against a real Postgres database.
// Skipped when ATTRACTOR_DB_URL is not set.
func TestPostgresStore(t *testing.T) {
	dbURL := os.Getenv("ATTRACTOR_DB_URL")
	if dbURL == "" {
		t.Skip("ATTRACTOR_DB_URL not set; skipping Postgres integration tests")
	}

	ctx := context.Background()
	store, err := NewPostgresStore(ctx, dbURL)
	if err != nil {
		t.Fatalf("NewPostgresStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	t.Run("full_lifecycle", func(t *testing.T) {
		runID, err := store.StartRun(ctx, PipelineRun{
			PipelineFile: "test-pipeline.dot",
			GraphName:    "test_graph",
			Goal:         "Build a test project",
			DefaultModel: "test-model",
			Simulate:     false,
			DockerImage:  "golang:1.26",
			BudgetTokens: 100000,
			StagesTotal:  3,
		})
		if err != nil {
			t.Fatalf("StartRun: %v", err)
		}
		if runID == uuid.Nil {
			t.Fatal("expected non-nil run UUID")
		}

		boolTrue := true
		stageID, err := store.RecordStage(ctx, StageResult{
			RunID:                 runID,
			NodeID:                "scaffold",
			Sequence:              1,
			Model:                 "test-model",
			Status:                "success",
			Rounds:                5,
			InputTokens:           1000,
			OutputTokens:          500,
			TotalTokens:           1500,
			DurationMs:            30000,
			FilesAdded:            10,
			FilesModified:         2,
			ScratchSummaryProduced: true,
			BuildGateAttempts:     1,
			BuildGatePassed:       &boolTrue,
		})
		if err != nil {
			t.Fatalf("RecordStage: %v", err)
		}
		if stageID <= 0 {
			t.Fatalf("expected positive stage ID, got %d", stageID)
		}

		err = store.RecordEvent(ctx, StageEvent{
			StageID:   stageID,
			RunID:     runID,
			EventType: "build_gate_pass",
			Round:     5,
			Detail:    "go build ./... passed",
		})
		if err != nil {
			t.Fatalf("RecordEvent: %v", err)
		}

		now := time.Now()
		err = store.FinishRun(ctx, runID, RunFinish{
			FinishedAt:       now,
			DurationMs:       60000,
			Status:           "success",
			TotalInputTokens:  1000,
			TotalOutputTokens: 500,
			TotalTokens:       1500,
			StagesCompleted:   3,
		})
		if err != nil {
			t.Fatalf("FinishRun: %v", err)
		}

		// Verify the run was recorded by querying directly.
		var status string
		var stagesCompleted int
		err = store.pool.QueryRow(ctx,
			"SELECT status, stages_completed FROM pipeline_runs WHERE id = $1", runID,
		).Scan(&status, &stagesCompleted)
		if err != nil {
			t.Fatalf("verify run: %v", err)
		}
		if d := cmp.Diff("success", status); d != "" {
			t.Errorf("run status mismatch (-want +got):\n%s", d)
		}
		if d := cmp.Diff(3, stagesCompleted); d != "" {
			t.Errorf("stages_completed mismatch (-want +got):\n%s", d)
		}

		// Verify stage was recorded.
		var stageNode string
		var tokens int
		err = store.pool.QueryRow(ctx,
			"SELECT node_id, total_tokens FROM stage_results WHERE id = $1", stageID,
		).Scan(&stageNode, &tokens)
		if err != nil {
			t.Fatalf("verify stage: %v", err)
		}
		if d := cmp.Diff("scaffold", stageNode); d != "" {
			t.Errorf("stage node_id mismatch (-want +got):\n%s", d)
		}
		if d := cmp.Diff(1500, tokens); d != "" {
			t.Errorf("stage total_tokens mismatch (-want +got):\n%s", d)
		}

		// Verify event was recorded.
		var eventType, detail string
		err = store.pool.QueryRow(ctx,
			"SELECT event_type, detail FROM stage_events WHERE stage_id = $1", stageID,
		).Scan(&eventType, &detail)
		if err != nil {
			t.Fatalf("verify event: %v", err)
		}
		if d := cmp.Diff("build_gate_pass", eventType); d != "" {
			t.Errorf("event type mismatch (-want +got):\n%s", d)
		}
	})

	t.Run("nullable_fields", func(t *testing.T) {
		runID, err := store.StartRun(ctx, PipelineRun{
			PipelineFile: "minimal.dot",
			StagesTotal:  1,
		})
		if err != nil {
			t.Fatalf("StartRun: %v", err)
		}

		stageID, err := store.RecordStage(ctx, StageResult{
			RunID:    runID,
			NodeID:   "only_stage",
			Sequence: 1,
			Status:   "success",
		})
		if err != nil {
			t.Fatalf("RecordStage with minimal fields: %v", err)
		}
		if stageID <= 0 {
			t.Fatalf("expected positive stage ID, got %d", stageID)
		}

		err = store.RecordEvent(ctx, StageEvent{
			StageID:   stageID,
			RunID:     runID,
			EventType: "empty_output",
		})
		if err != nil {
			t.Fatalf("RecordEvent with minimal fields: %v", err)
		}
	})

	t.Run("failed_run", func(t *testing.T) {
		runID, err := store.StartRun(ctx, PipelineRun{
			PipelineFile: "fail.dot",
			StagesTotal:  5,
		})
		if err != nil {
			t.Fatalf("StartRun: %v", err)
		}

		now := time.Now()
		err = store.FinishRun(ctx, runID, RunFinish{
			FinishedAt:    now,
			DurationMs:    5000,
			Status:        "fail",
			FailureReason: "scaffold stage exhausted",
			StagesCompleted: 1,
		})
		if err != nil {
			t.Fatalf("FinishRun: %v", err)
		}

		var status string
		var reason *string
		err = store.pool.QueryRow(ctx,
			"SELECT status, failure_reason FROM pipeline_runs WHERE id = $1", runID,
		).Scan(&status, &reason)
		if err != nil {
			t.Fatalf("verify: %v", err)
		}
		if d := cmp.Diff("fail", status); d != "" {
			t.Errorf("status mismatch (-want +got):\n%s", d)
		}
		if reason == nil || *reason != "scaffold stage exhausted" {
			t.Errorf("failure_reason: got %v, want %q", reason, "scaffold stage exhausted")
		}
	})
}

func TestPostgresStore_BadURL(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := NewPostgresStore(ctx, "postgres://invalid:5432/nope")
	if err == nil {
		t.Fatal("expected error for bad connection URL")
	}
}
