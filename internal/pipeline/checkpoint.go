package pipeline

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// Checkpoint is a serializable snapshot of execution state, saved after each
// node completes. It enables crash recovery and resume.
type Checkpoint struct {
	Timestamp      time.Time         `json:"timestamp"`
	CurrentNode    string            `json:"current_node"`
	CompletedNodes []string          `json:"completed_nodes"`
	NodeRetries    map[string]int    `json:"node_retries"`
	ContextValues  map[string]string `json:"context"`
	Logs           []string          `json:"logs"`
}

// NewCheckpoint creates a checkpoint from the current engine state.
func NewCheckpoint(currentNode string, completedNodes []string, nodeRetries map[string]int, ctx *Context) Checkpoint {
	return Checkpoint{
		Timestamp:      time.Now(),
		CurrentNode:    currentNode,
		CompletedNodes: append([]string{}, completedNodes...),
		NodeRetries:    copyIntMap(nodeRetries),
		ContextValues:  ctx.Snapshot(),
		Logs:           ctx.Logs(),
	}
}

// Save writes the checkpoint to a JSON file at the given path.
func (c Checkpoint) Save(path string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("checkpoint marshal: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

// LoadCheckpoint reads a checkpoint from a JSON file.
func LoadCheckpoint(path string) (Checkpoint, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Checkpoint{}, fmt.Errorf("checkpoint read: %w", err)
	}
	var cp Checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return Checkpoint{}, fmt.Errorf("checkpoint unmarshal: %w", err)
	}
	return cp, nil
}

// RestoreContext creates a Context populated with the checkpoint's saved values
// and log entries.
func (c Checkpoint) RestoreContext() *Context {
	ctx := NewContext()
	for k, v := range c.ContextValues {
		ctx.Set(k, v)
	}
	for _, entry := range c.Logs {
		ctx.AppendLog(entry)
	}
	return ctx
}

// IsCompleted returns true if the given node ID appears in CompletedNodes.
func (c Checkpoint) IsCompleted(nodeID string) bool {
	for _, id := range c.CompletedNodes {
		if id == nodeID {
			return true
		}
	}
	return false
}

func copyIntMap(m map[string]int) map[string]int {
	c := make(map[string]int, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}
