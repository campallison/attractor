package pipeline

import (
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestCheckpoint_SaveAndLoad(t *testing.T) {
	ctx := NewContext()
	ctx.Set("outcome", "success")
	ctx.Set("last_stage", "plan")
	ctx.AppendLog("started pipeline")
	ctx.AppendLog("completed plan")

	cp := NewCheckpoint("plan", []string{"start", "plan"}, map[string]int{"plan": 1}, ctx)

	path := filepath.Join(t.TempDir(), "checkpoint.json")
	if err := cp.Save(path); err != nil {
		t.Fatalf("Save error: %v", err)
	}

	loaded, err := LoadCheckpoint(path)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	if diff := cmp.Diff("plan", loaded.CurrentNode); diff != "" {
		t.Errorf("CurrentNode mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"start", "plan"}, loaded.CompletedNodes); diff != "" {
		t.Errorf("CompletedNodes mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(map[string]int{"plan": 1}, loaded.NodeRetries); diff != "" {
		t.Errorf("NodeRetries mismatch (-want +got):\n%s", diff)
	}
	wantCtx := map[string]string{"outcome": "success", "last_stage": "plan"}
	if diff := cmp.Diff(wantCtx, loaded.ContextValues); diff != "" {
		t.Errorf("ContextValues mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"started pipeline", "completed plan"}, loaded.Logs); diff != "" {
		t.Errorf("Logs mismatch (-want +got):\n%s", diff)
	}
}

func TestCheckpoint_RestoreContext(t *testing.T) {
	cp := Checkpoint{
		ContextValues: map[string]string{"a": "1", "b": "2"},
		Logs:          []string{"log entry"},
	}
	ctx := cp.RestoreContext()
	if diff := cmp.Diff("1", ctx.GetString("a")); diff != "" {
		t.Errorf("restored value mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"log entry"}, ctx.Logs()); diff != "" {
		t.Errorf("restored logs mismatch (-want +got):\n%s", diff)
	}
}

func TestCheckpoint_IsCompleted(t *testing.T) {
	tests := []struct {
		name      string
		completed []string
		nodeID    string
		want      bool
	}{
		{name: "found", completed: []string{"a", "b"}, nodeID: "b", want: true},
		{name: "not found", completed: []string{"a", "b"}, nodeID: "c", want: false},
		{name: "empty", completed: nil, nodeID: "a", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cp := Checkpoint{CompletedNodes: tt.completed}
			got := cp.IsCompleted(tt.nodeID)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("IsCompleted mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestCheckpoint_LoadNotFound(t *testing.T) {
	_, err := LoadCheckpoint(filepath.Join(t.TempDir(), "nonexistent.json"))
	if err == nil {
		t.Error("expected error for missing checkpoint file")
	}
}
