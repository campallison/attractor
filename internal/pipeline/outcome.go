// Package pipeline implements the Attractor pipeline execution engine.
// Layer 3: graph traversal, handler dispatch, context management, and
// checkpointing for DOT-defined AI workflows.
package pipeline

// StageStatus represents the result of executing a node handler.
type StageStatus string

const (
	StatusSuccess        StageStatus = "success"
	StatusFail           StageStatus = "fail"
	StatusPartialSuccess StageStatus = "partial_success"
	StatusRetry          StageStatus = "retry"
	StatusSkipped        StageStatus = "skipped"
)

// IsSuccess returns true for SUCCESS and PARTIAL_SUCCESS.
func (s StageStatus) IsSuccess() bool {
	return s == StatusSuccess || s == StatusPartialSuccess
}

// Outcome is the result of executing a node handler. It drives routing
// decisions and context state updates.
type Outcome struct {
	Status           StageStatus
	PreferredLabel   string
	SuggestedNextIDs []string
	ContextUpdates   map[string]string
	Notes            string
	FailureReason    string
}
