// Package envfile provides a pure parser for .env files. It does not read
// from the filesystem or modify environment variables -- callers handle I/O
// and decide the application policy (overwrite vs. set-if-absent).
package envfile

import "strings"

// Parse extracts key-value pairs from .env file content. It handles comments
// (lines starting with #), blank lines, whitespace trimming, and surrounding
// quotes (matched double or single). Lines without an = sign or with an empty
// key are silently skipped. Values containing = signs are preserved (only the
// first = is treated as the delimiter).
func Parse(content string) map[string]string {
	result := make(map[string]string)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimRight(line, "\r")
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		value = strings.TrimSpace(value)
		value = stripQuotes(value)
		result[key] = value
	}
	return result
}

func stripQuotes(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') ||
			(s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
