package pipeline

import (
	"fmt"
	"regexp"
	"strings"
)

// CheckResult represents a parsed [CHECK:name] PASS/FAIL marker from check
// command output.
type CheckResult struct {
	Name    string
	Passed  bool
	Summary string // parenthetical portion, e.g. "(10 routes, 10 handler methods)"
	Detail  string // indented detail lines following a FAIL marker
}

var checkMarkerRe = regexp.MustCompile(`\[CHECK:(\w[\w.-]*)\]\s+(PASS|FAIL)\b(.*)`)

// parseCheckOutput extracts structured [CHECK:name] PASS/FAIL markers from
// check command output. Lines between markers are captured as detail for the
// preceding check. Returns nil when no markers are found.
func parseCheckOutput(output string) []CheckResult {
	lines := strings.Split(output, "\n")
	var results []CheckResult
	var current *CheckResult

	for _, line := range lines {
		if m := checkMarkerRe.FindStringSubmatch(line); m != nil {
			if current != nil {
				current.Detail = strings.TrimRight(current.Detail, "\n")
				results = append(results, *current)
			}
			current = &CheckResult{
				Name:    m[1],
				Passed:  m[2] == "PASS",
				Summary: strings.TrimSpace(m[3]),
			}
			continue
		}
		if current != nil && !current.Passed {
			trimmed := strings.TrimRight(line, "\r")
			if current.Detail != "" || strings.TrimSpace(trimmed) != "" {
				if current.Detail != "" {
					current.Detail += "\n"
				}
				current.Detail += trimmed
			}
		}
	}
	if current != nil {
		current.Detail = strings.TrimRight(current.Detail, "\n")
		results = append(results, *current)
	}

	return results
}

// buildRetryPrompt constructs the prompt for a build gate fix attempt. When
// the check output contains [CHECK:name] markers, the prompt is structured
// with a summary table and only the details of failing checks, helping the
// agent focus. When no markers are found (e.g. bare compiler errors), it
// falls back to including the raw output.
func buildRetryPrompt(prompt, checkOutput string) string {
	results := parseCheckOutput(checkOutput)

	if len(results) == 0 {
		return prompt +
			"\n\n--- BUILD GATE FAILURE ---\n" +
			"The following compilation/check errors were found after your changes. Fix them:\n\n" +
			checkOutput +
			"\n\nIf you maintained working notes in _scratch/, check them for context from your previous attempt before starting your fix."
	}

	var b strings.Builder
	b.WriteString(prompt)
	b.WriteString("\n\n--- BUILD GATE FAILURE ---\n")

	// Any output before the first [CHECK:] marker (e.g. compiler warnings).
	if preMarker := extractPreMarkerOutput(checkOutput); preMarker != "" {
		b.WriteString("Pre-check output:\n")
		b.WriteString(preMarker)
		b.WriteString("\n\n")
	}

	b.WriteString("Check results:\n")
	for _, r := range results {
		if r.Passed {
			fmt.Fprintf(&b, "  PASS  [%s] %s\n", r.Name, r.Summary)
		} else {
			fmt.Fprintf(&b, "  FAIL  [%s] %s\n", r.Name, r.Summary)
		}
	}

	var failCount int
	for _, r := range results {
		if r.Passed {
			continue
		}
		failCount++
		fmt.Fprintf(&b, "\n[CHECK:%s] FAIL %s\n", r.Name, r.Summary)
		if r.Detail != "" {
			b.WriteString(r.Detail)
			b.WriteString("\n")
		}
	}

	if failCount > 0 {
		b.WriteString("\nFocus on fixing the failing checks above. The passing checks do not need changes.\n")
	}
	b.WriteString("If you maintained working notes in _scratch/, check them for context from your previous attempt before starting your fix.")

	return b.String()
}

// extractPreMarkerOutput returns any text before the first [CHECK:] marker.
// This captures compiler output or other diagnostic text that precedes the
// structured check results.
func extractPreMarkerOutput(output string) string {
	idx := checkMarkerRe.FindStringIndex(output)
	if idx == nil || idx[0] == 0 {
		return ""
	}
	return strings.TrimSpace(output[:idx[0]])
}
