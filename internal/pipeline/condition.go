package pipeline

import "strings"

// EvaluateCondition evaluates a condition expression string against the
// current outcome and context. The condition language supports:
//   - key=value     (equality)
//   - key!=value    (inequality)
//   - clause && clause  (conjunction)
//
// An empty condition always evaluates to true (unconditional edge).
func EvaluateCondition(condition string, outcome Outcome, ctx *Context) bool {
	condition = strings.TrimSpace(condition)
	if condition == "" {
		return true
	}
	clauses := strings.Split(condition, "&&")
	for _, clause := range clauses {
		clause = strings.TrimSpace(clause)
		if clause == "" {
			continue
		}
		if !evaluateClause(clause, outcome, ctx) {
			return false
		}
	}
	return true
}

func evaluateClause(clause string, outcome Outcome, ctx *Context) bool {
	if idx := strings.Index(clause, "!="); idx >= 0 {
		key := strings.TrimSpace(clause[:idx])
		val := strings.TrimSpace(clause[idx+2:])
		return resolveKey(key, outcome, ctx) != val
	}
	if idx := strings.Index(clause, "="); idx >= 0 {
		key := strings.TrimSpace(clause[:idx])
		val := strings.TrimSpace(clause[idx+1:])
		return resolveKey(key, outcome, ctx) == val
	}
	// Bare key: truthy if non-empty.
	return resolveKey(strings.TrimSpace(clause), outcome, ctx) != ""
}

// resolveKey resolves a condition key against the outcome and context.
// Special keys: "outcome", "preferred_label". Keys starting with "context."
// look up values in the run context. Unqualified keys also try a direct
// context lookup.
func resolveKey(key string, outcome Outcome, ctx *Context) string {
	switch key {
	case "outcome":
		return string(outcome.Status)
	case "preferred_label":
		return outcome.PreferredLabel
	}
	if strings.HasPrefix(key, "context.") {
		suffix := strings.TrimPrefix(key, "context.")
		if v := ctx.GetString(key); v != "" {
			return v
		}
		return ctx.GetString(suffix)
	}
	return ctx.GetString(key)
}
