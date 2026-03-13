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

// FileDiffCounts holds the aggregate counts from a filesystem snapshot diff.
type FileDiffCounts struct {
	Added     int
	Modified  int
	Removed   int
	Unchanged int
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
	Usage            *StageUsage

	Attempts             int             // engine-level handler invocations (1 = no retries, 0 = canceled before any attempt)
	ExhaustionReason      string         // ExhaustionRoundLimit, ExhaustionReadLoop, or empty
	PromptLength          int            // length of the prompt sent to the agent
	ResponseLength        int            // length of the final response text
	ScratchSummaryProduced bool          // true if _scratch/SUMMARY.md was found
	BuildGateAttempts     int            // number of build gate check attempts (0 if no gate)
	BuildGatePassed       *bool          // nil if no build gate, true/false otherwise
	FileDiffCounts        *FileDiffCounts // nil if no snapshot was captured
}

// StageUsage records token consumption for a single pipeline stage.
type StageUsage struct {
	Model        string `json:"model"`
	Rounds       int    `json:"rounds"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
	TotalTokens  int    `json:"total_tokens"`
}
