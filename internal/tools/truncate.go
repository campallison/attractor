package tools

import "fmt"

// Default character limits per tool, following the spec Section 5.2.
var DefaultCharLimits = map[string]int{
	"read_file":  50_000,
	"shell":      30_000,
	"grep":       20_000,
	"glob":       20_000,
	"edit_file":  10_000,
	"write_file": 1_000,
}

// TruncateOutput applies head/tail split truncation to tool output.
// If the output length is within maxChars, it is returned unchanged.
// Otherwise, the first half and last half are kept, with a warning
// message inserted in the middle indicating how many characters were removed.
func TruncateOutput(output string, maxChars int) string {
	if len(output) <= maxChars {
		return output
	}

	half := maxChars / 2
	removed := len(output) - maxChars

	return output[:half] +
		fmt.Sprintf(
			"\n\n[WARNING: Tool output was truncated. %d characters were removed from the middle. "+
				"The full output is available in the event stream. "+
				"If you need to see specific parts, re-run the tool with more targeted parameters.]\n\n",
			removed,
		) +
		output[len(output)-half:]
}

// TruncateToolOutput truncates tool output using the default limit for the
// given tool name. If no default is found, the output is returned unchanged.
func TruncateToolOutput(output, toolName string) string {
	limit, ok := DefaultCharLimits[toolName]
	if !ok {
		return output
	}
	return TruncateOutput(output, limit)
}
