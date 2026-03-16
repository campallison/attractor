package pipeline

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestParseCheckOutput(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   []CheckResult
	}{
		{
			name:   "no markers returns nil",
			output: "internal/db/queries.go:15: undefined: models.Team\n",
			want:   nil,
		},
		{
			name:   "empty string",
			output: "",
			want:   nil,
		},
		{
			name:   "single PASS",
			output: "[CHECK:routes] PASS (10 routes, 10 handler methods)\n",
			want: []CheckResult{
				{Name: "routes", Passed: true, Summary: "(10 routes, 10 handler methods)"},
			},
		},
		{
			name:   "single FAIL with detail",
			output: "[CHECK:templates] FAIL (5 templates, 1 error)\n  templates/board.html:12: hx-post=\"/api/items\" has no matching route\n",
			want: []CheckResult{
				{
					Name:    "templates",
					Passed:  false,
					Summary: "(5 templates, 1 error)",
					Detail:  "  templates/board.html:12: hx-post=\"/api/items\" has no matching route",
				},
			},
		},
		{
			name: "mixed PASS and FAIL",
			output: "[CHECK:routes] PASS (10 routes, 10 handler methods)\n" +
				"[CHECK:templates] FAIL (5 templates, 1 error)\n" +
				"  templates/board.html:12: bad route\n" +
				"[CHECK:tmpl-names] PASS (8 template names)\n" +
				"[CHECK:store] PASS (4 interface methods)\n",
			want: []CheckResult{
				{Name: "routes", Passed: true, Summary: "(10 routes, 10 handler methods)"},
				{Name: "templates", Passed: false, Summary: "(5 templates, 1 error)", Detail: "  templates/board.html:12: bad route"},
				{Name: "tmpl-names", Passed: true, Summary: "(8 template names)"},
				{Name: "store", Passed: true, Summary: "(4 interface methods)"},
			},
		},
		{
			name: "behavioral checks with body and server log",
			output: "[CHECK:startup] PASS\n" +
				"[CHECK:sweep] FAIL (10 routes, 2 returned 500, 0 unreachable)\n" +
				"  GET /api/items → HTTP 500\n" +
				"    body: pq: relation \"items\" does not exist\n" +
				"  server log:\n" +
				"    2026/03/06 ERROR sql: relation \"items\"\n",
			want: []CheckResult{
				{Name: "startup", Passed: true, Summary: ""},
				{
					Name:    "sweep",
					Passed:  false,
					Summary: "(10 routes, 2 returned 500, 0 unreachable)",
					Detail: "  GET /api/items → HTTP 500\n" +
						"    body: pq: relation \"items\" does not exist\n" +
						"  server log:\n" +
						"    2026/03/06 ERROR sql: relation \"items\"",
				},
			},
		},
		{
			name: "multiple failures",
			output: "[CHECK:routes] FAIL (3 routes, 2 handler methods)\n" +
				"  routes.go:10: handler \"DeleteItem\" missing\n" +
				"[CHECK:templates] FAIL (5 templates, 1 error)\n" +
				"  board.html:12: bad route\n",
			want: []CheckResult{
				{Name: "routes", Passed: false, Summary: "(3 routes, 2 handler methods)", Detail: "  routes.go:10: handler \"DeleteItem\" missing"},
				{Name: "templates", Passed: false, Summary: "(5 templates, 1 error)", Detail: "  board.html:12: bad route"},
			},
		},
		{
			name:   "FAIL with no detail lines",
			output: "[CHECK:startup] FAIL (0 routes checked)\n",
			want: []CheckResult{
				{Name: "startup", Passed: false, Summary: "(0 routes checked)"},
			},
		},
		{
			name:   "check name with dots and hyphens",
			output: "[CHECK:tmpl-names] PASS (ok)\n[CHECK:store.interface] PASS (ok)\n",
			want: []CheckResult{
				{Name: "tmpl-names", Passed: true, Summary: "(ok)"},
				{Name: "store.interface", Passed: true, Summary: "(ok)"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseCheckOutput(tc.output)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("parseCheckOutput mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestExtractPreMarkerOutput(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   string
	}{
		{
			name:   "no pre-marker output",
			output: "[CHECK:routes] PASS (10 routes)\n",
			want:   "",
		},
		{
			name:   "compiler warnings before markers",
			output: "# my/pkg\nvet: ./handler.go:15: unreachable code\n[CHECK:routes] PASS (10 routes)\n",
			want:   "# my/pkg\nvet: ./handler.go:15: unreachable code",
		},
		{
			name:   "no markers at all",
			output: "build errors only\n",
			want:   "",
		},
		{
			name:   "whitespace only before marker",
			output: "  \n\n[CHECK:routes] PASS\n",
			want:   "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := extractPreMarkerOutput(tc.output)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("extractPreMarkerOutput mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestBuildRetryPrompt_NoMarkers(t *testing.T) {
	prompt := "Implement the DB layer"
	checkOutput := "internal/db/queries.go:15: undefined: models.Team"

	got := buildRetryPrompt(prompt, checkOutput)

	if !strings.HasPrefix(got, "Implement the DB layer") {
		t.Error("should start with original prompt")
	}
	if !strings.Contains(got, "--- BUILD GATE FAILURE ---") {
		t.Error("should contain failure header")
	}
	if !strings.Contains(got, "undefined: models.Team") {
		t.Error("should contain raw error output")
	}
	if !strings.Contains(got, "_scratch/") {
		t.Error("should contain scratch hint")
	}
	if strings.Contains(got, "Check results:") {
		t.Error("should not contain structured check summary for unstructured output")
	}
}

func TestBuildRetryPrompt_WithMarkers(t *testing.T) {
	prompt := "Build the handlers"
	checkOutput := "[CHECK:routes] PASS (10 routes, 10 handler methods)\n" +
		"[CHECK:templates] FAIL (5 templates, 1 error)\n" +
		"  templates/board.html:12: hx-post=\"/api/items\" has no matching route\n" +
		"[CHECK:tmpl-names] PASS (8 template names)\n" +
		"[CHECK:store] PASS (4 interface methods)\n"

	got := buildRetryPrompt(prompt, checkOutput)

	if !strings.HasPrefix(got, "Build the handlers") {
		t.Error("should start with original prompt")
	}
	if !strings.Contains(got, "--- BUILD GATE FAILURE ---") {
		t.Error("should contain failure header")
	}
	if !strings.Contains(got, "Check results:") {
		t.Error("should contain structured check summary")
	}
	if !strings.Contains(got, "PASS  [routes] (10 routes") {
		t.Error("should list passing checks in summary")
	}
	if !strings.Contains(got, "FAIL  [templates] (5 templates") {
		t.Error("should list failing checks in summary")
	}
	if !strings.Contains(got, "[CHECK:templates] FAIL (5 templates") {
		t.Error("should include failed check details")
	}
	if !strings.Contains(got, "hx-post=\"/api/items\" has no matching route") {
		t.Error("should include error detail for failing check")
	}
	if !strings.Contains(got, "Focus on fixing the failing checks") {
		t.Error("should include focus instruction")
	}
	if !strings.Contains(got, "_scratch/") {
		t.Error("should contain scratch hint")
	}

	// Passing check details should NOT appear in the detail section.
	detailSection := got[strings.Index(got, "Check results:"):]
	if strings.Contains(detailSection, "[CHECK:routes] PASS") || strings.Contains(detailSection, "[CHECK:routes] FAIL") {
		t.Error("should not repeat passing check details in the detail section")
	}
}

func TestBuildRetryPrompt_WithPreMarkerOutput(t *testing.T) {
	prompt := "Build it"
	checkOutput := "# my/pkg\nvet: handler.go:15: unreachable code\n[CHECK:routes] FAIL (bad)\n  routes.go:5: missing handler\n"

	got := buildRetryPrompt(prompt, checkOutput)

	if !strings.Contains(got, "Pre-check output:") {
		t.Error("should include pre-check output section")
	}
	if !strings.Contains(got, "unreachable code") {
		t.Error("should contain pre-marker text")
	}
	if !strings.Contains(got, "Check results:") {
		t.Error("should still contain structured check summary")
	}
}

func TestBuildRetryPrompt_AllPass(t *testing.T) {
	prompt := "Build it"
	checkOutput := "[CHECK:routes] PASS (ok)\n[CHECK:templates] PASS (ok)\n"

	got := buildRetryPrompt(prompt, checkOutput)

	if !strings.Contains(got, "Check results:") {
		t.Error("should contain check summary even when all pass")
	}
	if strings.Contains(got, "Focus on fixing") {
		t.Error("should not contain focus instruction when all checks pass")
	}
}

func TestBuildRetryPrompt_BehavioralDetail(t *testing.T) {
	prompt := "Fix the handlers"
	checkOutput := "[CHECK:startup] PASS\n" +
		"[CHECK:sweep] FAIL (10 routes, 2 returned 500, 0 unreachable)\n" +
		"  GET /api/items → HTTP 500\n" +
		"    body: pq: relation \"items\" does not exist\n" +
		"  server log:\n" +
		"    2026/03/06 ERROR sql: relation \"items\"\n"

	got := buildRetryPrompt(prompt, checkOutput)

	if !strings.Contains(got, "PASS  [startup]") {
		t.Error("should show startup as passing")
	}
	if !strings.Contains(got, "FAIL  [sweep] (10 routes") {
		t.Error("should show sweep as failing")
	}
	if !strings.Contains(got, "pq: relation \"items\" does not exist") {
		t.Error("should include response body in detail")
	}
	if !strings.Contains(got, "server log:") {
		t.Error("should include server log in detail")
	}
}
