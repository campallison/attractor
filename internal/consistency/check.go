// Package consistency provides static analysis checks that verify internal
// agreement among components of a generated Go web application. These checks
// are designed to run as pipeline build gates via check_cmd, catching
// recombination failures that compilation alone cannot detect.
package consistency

import (
	"fmt"
	"sort"
	"strings"
)

// Severity indicates the severity of a finding.
type Severity int

const (
	SeverityError Severity = iota
	SeverityWarning
)

func (s Severity) String() string {
	switch s {
	case SeverityError:
		return "ERROR"
	case SeverityWarning:
		return "WARNING"
	default:
		return "UNKNOWN"
	}
}

// Finding represents a single issue discovered by a check.
type Finding struct {
	Severity Severity
	Message  string
	File     string
	Line     int
}

func (f Finding) String() string {
	if f.File != "" {
		if f.Line > 0 {
			return fmt.Sprintf("  %s:%d: %s", f.File, f.Line, f.Message)
		}
		return fmt.Sprintf("  %s: %s", f.File, f.Message)
	}
	return fmt.Sprintf("  %s", f.Message)
}

// Result holds the outcome of a single check.
type Result struct {
	Name     string
	Passed   bool
	Summary  string
	Findings []Finding
}

func (r Result) String() string {
	var b strings.Builder
	if r.Passed {
		fmt.Fprintf(&b, "[CHECK:%s] PASS", r.Name)
	} else {
		fmt.Fprintf(&b, "[CHECK:%s] FAIL", r.Name)
	}
	if r.Summary != "" {
		fmt.Fprintf(&b, " (%s)", r.Summary)
	}
	b.WriteByte('\n')
	for _, f := range r.Findings {
		b.WriteString(f.String())
		b.WriteByte('\n')
	}
	return b.String()
}

// HasErrors returns true if any result contains an error-severity finding.
func HasErrors(results []Result) bool {
	for _, r := range results {
		if !r.Passed {
			return true
		}
	}
	return false
}

// CheckFunc runs a consistency check against the Go source tree rooted at dir.
type CheckFunc func(root string) Result

// Registry maps check names to their implementations.
var Registry = map[string]CheckFunc{
	"routes":    CheckRouteHandler,
	"templates": CheckTemplateRoutes,
}

// RunChecks runs the named checks (or all registered checks if names is empty).
func RunChecks(root string, names []string) []Result {
	if len(names) == 0 {
		names = allCheckNames()
	}

	results := make([]Result, 0, len(names))
	for _, name := range names {
		fn, ok := Registry[name]
		if !ok {
			results = append(results, Result{
				Name:   name,
				Passed: false,
				Findings: []Finding{{
					Severity: SeverityError,
					Message:  fmt.Sprintf("unknown check %q", name),
				}},
			})
			continue
		}
		results = append(results, fn(root))
	}
	return results
}

func allCheckNames() []string {
	names := make([]string, 0, len(Registry))
	for name := range Registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
