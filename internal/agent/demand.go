package agent

import (
	"encoding/json"
	"path"

	"github.com/campallison/attractor/internal/llm"
)

// demandTracker classifies agent tool calls as value demand (productive work
// that directly serves the user's goal) or failure demand (work caused by the
// agent's own prior mistakes). The ratio of failure to total calls is a leading
// indicator of agent effectiveness.
//
// Classification heuristics:
//   - read_file of a file the agent previously wrote → failure demand
//   - read_file of a file the agent never wrote → value demand
//   - First write_file/edit_file to a path → value demand
//   - Subsequent write_file/edit_file to the same path → failure demand
//   - shell and other tools → value demand
type demandTracker struct {
	writtenFiles map[string]bool
	ValueCalls   int
	FailureCalls int
}

func newDemandTracker() *demandTracker {
	return &demandTracker{writtenFiles: make(map[string]bool)}
}

func (d *demandTracker) classify(tc llm.ToolCall) {
	switch tc.Name {
	case "write_file", "edit_file":
		p := normalizePath(extractPathArg(tc.Arguments))
		if p == "" {
			d.ValueCalls++
			return
		}
		if d.writtenFiles[p] {
			d.FailureCalls++
		} else {
			d.ValueCalls++
			d.writtenFiles[p] = true
		}

	case "read_file":
		p := normalizePath(extractPathArg(tc.Arguments))
		if p != "" && d.writtenFiles[p] {
			d.FailureCalls++
		} else {
			d.ValueCalls++
		}

	default:
		d.ValueCalls++
	}
}

// ratio returns the fraction of tool calls classified as failure demand.
// Returns 0 when no calls have been tracked.
func (d *demandTracker) ratio() float64 {
	total := d.ValueCalls + d.FailureCalls
	if total == 0 {
		return 0
	}
	return float64(d.FailureCalls) / float64(total)
}

// extractPathArg pulls the "path" field from a tool call's JSON arguments.
// Returns empty string on parse failure or missing field.
func extractPathArg(args json.RawMessage) string {
	var parsed struct {
		Path string `json:"path"`
	}
	if json.Unmarshal(args, &parsed) != nil {
		return ""
	}
	return parsed.Path
}

// normalizePath cleans a file path so that "main.go", "./main.go", and
// "internal/../main.go" all map to the same key.
func normalizePath(p string) string {
	if p == "" {
		return ""
	}
	return path.Clean(p)
}
