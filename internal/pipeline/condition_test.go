package pipeline

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestEvaluateCondition(t *testing.T) {
	tests := []struct {
		name      string
		condition string
		outcome   Outcome
		ctxValues map[string]string
		want      bool
	}{
		{
			name:      "empty condition is always true",
			condition: "",
			outcome:   Outcome{Status: StatusFail},
			want:      true,
		},
		{
			name:      "outcome equals success",
			condition: "outcome=success",
			outcome:   Outcome{Status: StatusSuccess},
			want:      true,
		},
		{
			name:      "outcome equals fail",
			condition: "outcome=fail",
			outcome:   Outcome{Status: StatusFail},
			want:      true,
		},
		{
			name:      "outcome not equals success (is fail)",
			condition: "outcome!=success",
			outcome:   Outcome{Status: StatusFail},
			want:      true,
		},
		{
			name:      "outcome not equals success (is success)",
			condition: "outcome!=success",
			outcome:   Outcome{Status: StatusSuccess},
			want:      false,
		},
		{
			name:      "preferred_label match",
			condition: "preferred_label=Fix",
			outcome:   Outcome{Status: StatusSuccess, PreferredLabel: "Fix"},
			want:      true,
		},
		{
			name:      "preferred_label mismatch",
			condition: "preferred_label=Deploy",
			outcome:   Outcome{Status: StatusSuccess, PreferredLabel: "Fix"},
			want:      false,
		},
		{
			name:      "context key with prefix",
			condition: "context.tests_passed=true",
			ctxValues: map[string]string{"tests_passed": "true"},
			outcome:   Outcome{Status: StatusSuccess},
			want:      true,
		},
		{
			name:      "context key with full prefix stored",
			condition: "context.tests_passed=true",
			ctxValues: map[string]string{"context.tests_passed": "true"},
			outcome:   Outcome{Status: StatusSuccess},
			want:      true,
		},
		{
			name:      "context key missing evaluates as empty",
			condition: "context.missing=yes",
			outcome:   Outcome{Status: StatusSuccess},
			want:      false,
		},
		{
			name:      "AND conjunction both true",
			condition: "outcome=success && context.tests_passed=true",
			outcome:   Outcome{Status: StatusSuccess},
			ctxValues: map[string]string{"tests_passed": "true"},
			want:      true,
		},
		{
			name:      "AND conjunction one false",
			condition: "outcome=success && context.tests_passed=true",
			outcome:   Outcome{Status: StatusSuccess},
			ctxValues: map[string]string{"tests_passed": "false"},
			want:      false,
		},
		{
			name:      "inequality with context",
			condition: "context.loop_state!=exhausted",
			outcome:   Outcome{Status: StatusSuccess},
			ctxValues: map[string]string{"loop_state": "running"},
			want:      true,
		},
		{
			name:      "bare key truthy",
			condition: "context.flag",
			outcome:   Outcome{Status: StatusSuccess},
			ctxValues: map[string]string{"flag": "yes"},
			want:      true,
		},
		{
			name:      "bare key falsy (missing)",
			condition: "context.flag",
			outcome:   Outcome{Status: StatusSuccess},
			want:      false,
		},
		{
			name:      "whitespace tolerance",
			condition: " outcome = success ",
			outcome:   Outcome{Status: StatusSuccess},
			want:      true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := NewContext()
			for k, v := range tt.ctxValues {
				ctx.Set(k, v)
			}
			got := EvaluateCondition(tt.condition, tt.outcome, ctx)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("EvaluateCondition mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
