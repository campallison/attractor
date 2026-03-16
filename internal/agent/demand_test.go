package agent

import (
	"encoding/json"
	"testing"

	"github.com/campallison/attractor/internal/llm"
	"github.com/google/go-cmp/cmp"
)

func TestDemandTracker_ReadNewFile(t *testing.T) {
	d := newDemandTracker()
	d.classify(llm.ToolCall{Name: "read_file", Arguments: json.RawMessage(`{"path":"main.go"}`)})

	if diff := cmp.Diff(1, d.ValueCalls); diff != "" {
		t.Errorf("value calls (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(0, d.FailureCalls); diff != "" {
		t.Errorf("failure calls (-want +got):\n%s", diff)
	}
}

func TestDemandTracker_ReadWrittenFile(t *testing.T) {
	d := newDemandTracker()
	d.classify(llm.ToolCall{Name: "write_file", Arguments: json.RawMessage(`{"path":"main.go","content":"package main"}`)})
	d.classify(llm.ToolCall{Name: "read_file", Arguments: json.RawMessage(`{"path":"main.go"}`)})

	if diff := cmp.Diff(1, d.ValueCalls); diff != "" {
		t.Errorf("value calls (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(1, d.FailureCalls); diff != "" {
		t.Errorf("failure calls (-want +got):\n%s", diff)
	}
}

func TestDemandTracker_FirstWriteIsValue(t *testing.T) {
	d := newDemandTracker()
	d.classify(llm.ToolCall{Name: "write_file", Arguments: json.RawMessage(`{"path":"a.go","content":"x"}`)})
	d.classify(llm.ToolCall{Name: "write_file", Arguments: json.RawMessage(`{"path":"b.go","content":"y"}`)})

	if diff := cmp.Diff(2, d.ValueCalls); diff != "" {
		t.Errorf("value calls (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(0, d.FailureCalls); diff != "" {
		t.Errorf("failure calls (-want +got):\n%s", diff)
	}
}

func TestDemandTracker_RewriteIsFailure(t *testing.T) {
	d := newDemandTracker()
	d.classify(llm.ToolCall{Name: "write_file", Arguments: json.RawMessage(`{"path":"main.go","content":"v1"}`)})
	d.classify(llm.ToolCall{Name: "write_file", Arguments: json.RawMessage(`{"path":"main.go","content":"v2"}`)})

	if diff := cmp.Diff(1, d.ValueCalls); diff != "" {
		t.Errorf("value calls (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(1, d.FailureCalls); diff != "" {
		t.Errorf("failure calls (-want +got):\n%s", diff)
	}
}

func TestDemandTracker_EditNewFile(t *testing.T) {
	d := newDemandTracker()
	d.classify(llm.ToolCall{Name: "edit_file", Arguments: json.RawMessage(`{"path":"existing.go","old_string":"a","new_string":"b"}`)})

	if diff := cmp.Diff(1, d.ValueCalls); diff != "" {
		t.Errorf("value calls (-want +got):\n%s", diff)
	}
}

func TestDemandTracker_EditWrittenFile(t *testing.T) {
	d := newDemandTracker()
	d.classify(llm.ToolCall{Name: "write_file", Arguments: json.RawMessage(`{"path":"main.go","content":"x"}`)})
	d.classify(llm.ToolCall{Name: "edit_file", Arguments: json.RawMessage(`{"path":"main.go","old_string":"x","new_string":"y"}`)})

	if diff := cmp.Diff(1, d.ValueCalls); diff != "" {
		t.Errorf("value calls (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(1, d.FailureCalls); diff != "" {
		t.Errorf("failure calls (-want +got):\n%s", diff)
	}
}

func TestDemandTracker_ShellIsValue(t *testing.T) {
	d := newDemandTracker()
	d.classify(llm.ToolCall{Name: "shell", Arguments: json.RawMessage(`{"command":"go build ./..."}`)})

	if diff := cmp.Diff(1, d.ValueCalls); diff != "" {
		t.Errorf("value calls (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(0, d.FailureCalls); diff != "" {
		t.Errorf("failure calls (-want +got):\n%s", diff)
	}
}

func TestDemandTracker_Ratio(t *testing.T) {
	tests := []struct {
		name  string
		calls []llm.ToolCall
		want  float64
	}{
		{
			name:  "no calls",
			calls: nil,
			want:  0,
		},
		{
			name: "all value",
			calls: []llm.ToolCall{
				{Name: "read_file", Arguments: json.RawMessage(`{"path":"spec.md"}`)},
				{Name: "write_file", Arguments: json.RawMessage(`{"path":"main.go","content":"x"}`)},
				{Name: "shell", Arguments: json.RawMessage(`{"command":"go build"}`)},
			},
			want: 0,
		},
		{
			name: "50% failure",
			calls: []llm.ToolCall{
				{Name: "write_file", Arguments: json.RawMessage(`{"path":"a.go","content":"v1"}`)},
				{Name: "write_file", Arguments: json.RawMessage(`{"path":"a.go","content":"v2"}`)},
			},
			want: 0.5,
		},
		{
			name: "mixed scenario",
			calls: []llm.ToolCall{
				{Name: "read_file", Arguments: json.RawMessage(`{"path":"spec.md"}`)},
				{Name: "write_file", Arguments: json.RawMessage(`{"path":"main.go","content":"v1"}`)},
				{Name: "write_file", Arguments: json.RawMessage(`{"path":"handler.go","content":"x"}`)},
				{Name: "read_file", Arguments: json.RawMessage(`{"path":"main.go"}`)},
				{Name: "write_file", Arguments: json.RawMessage(`{"path":"main.go","content":"v2"}`)},
			},
			want: 0.4, // 2 failure (re-read main.go + rewrite main.go) out of 5
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := newDemandTracker()
			for _, call := range tc.calls {
				d.classify(call)
			}
			got := d.ratio()
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("ratio mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestDemandTracker_PathNormalization(t *testing.T) {
	d := newDemandTracker()
	d.classify(llm.ToolCall{Name: "write_file", Arguments: json.RawMessage(`{"path":"./main.go","content":"v1"}`)})
	d.classify(llm.ToolCall{Name: "read_file", Arguments: json.RawMessage(`{"path":"main.go"}`)})
	d.classify(llm.ToolCall{Name: "write_file", Arguments: json.RawMessage(`{"path":"internal/../main.go","content":"v2"}`)})

	if diff := cmp.Diff(1, d.ValueCalls); diff != "" {
		t.Errorf("value calls (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(2, d.FailureCalls); diff != "" {
		t.Errorf("failure calls — read and rewrite should both be failure (-want +got):\n%s", diff)
	}
}

func TestDemandTracker_InvalidJSON(t *testing.T) {
	d := newDemandTracker()
	d.classify(llm.ToolCall{Name: "write_file", Arguments: json.RawMessage(`{invalid}`)})

	if diff := cmp.Diff(1, d.ValueCalls); diff != "" {
		t.Errorf("invalid JSON should default to value (-want +got):\n%s", diff)
	}
}

func TestExtractPathArg(t *testing.T) {
	tests := []struct {
		name string
		args json.RawMessage
		want string
	}{
		{name: "valid path", args: json.RawMessage(`{"path":"main.go"}`), want: "main.go"},
		{name: "nested path", args: json.RawMessage(`{"path":"internal/db/repo.go","content":"x"}`), want: "internal/db/repo.go"},
		{name: "no path field", args: json.RawMessage(`{"command":"ls"}`), want: ""},
		{name: "invalid JSON", args: json.RawMessage(`{nope}`), want: ""},
		{name: "empty args", args: json.RawMessage(`{}`), want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := extractPathArg(tc.args)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("extractPathArg mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
