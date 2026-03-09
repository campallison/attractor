package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/campallison/attractor/internal/llm"
)

// maxReadFileSize is the largest file (in bytes) that read_file will load into
// memory. Defined as a variable so tests can temporarily lower it.
var maxReadFileSize int64 = 10 * 1024 * 1024 // 10MB

type readFileArgs struct {
	FilePath string `json:"file_path"`
	Offset   int    `json:"offset"`
	Limit    int    `json:"limit"`
}

// ReadFileTool returns the registered read_file tool.
func ReadFileTool() RegisteredTool {
	return RegisteredTool{
		Definition: llm.ToolDefinition{
			Name:        "read_file",
			Description: "Read a file from the filesystem. Returns line-numbered content.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"file_path": {
						"type": "string",
						"description": "Relative path to the file (must stay within working directory)"
					},
					"offset": {
						"type": "integer",
						"description": "1-based line number to start reading from"
					},
					"limit": {
						"type": "integer",
						"description": "Maximum number of lines to read (default: 2000)"
					}
				},
				"required": ["file_path"]
			}`),
		},
		Execute: executeReadFile,
	}
}

func executeReadFile(rawArgs json.RawMessage, workDir string) (string, error) {
	var args readFileArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return "", fmt.Errorf("invalid read_file arguments: %w", err)
	}

	path, err := resolvePath(args.FilePath, workDir)
	if err != nil {
		return "", fmt.Errorf("read_file: %w", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("read_file: %w", err)
	}
	if info.Size() > maxReadFileSize {
		return "", fmt.Errorf("read_file: %s is too large (%d bytes, max %d)", args.FilePath, info.Size(), maxReadFileSize)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read_file: %w", err)
	}

	if isBinary(data) {
		return "", fmt.Errorf("read_file: %s appears to be a binary file", args.FilePath)
	}

	if len(data) == 0 {
		return fmt.Sprintf("File %s is empty (0 bytes).", args.FilePath), nil
	}

	lines := strings.Split(string(data), "\n")

	offset := args.Offset
	if offset < 1 {
		offset = 1
	}
	limit := args.Limit
	if limit < 1 {
		limit = 2000
	}

	startIdx := offset - 1
	if startIdx >= len(lines) {
		return fmt.Sprintf("File %s has %d lines; offset %d is beyond end of file.", args.FilePath, len(lines), offset), nil
	}

	endIdx := startIdx + limit
	if endIdx > len(lines) {
		endIdx = len(lines)
	}

	maxLineNo := endIdx
	width := len(fmt.Sprintf("%d", maxLineNo))

	var buf strings.Builder
	for i := startIdx; i < endIdx; i++ {
		fmt.Fprintf(&buf, "%*d | %s\n", width, i+1, lines[i])
	}

	return buf.String(), nil
}

// isBinary checks the first 512 bytes for null bytes as a heuristic.
func isBinary(data []byte) bool {
	checkLen := 512
	if len(data) < checkLen {
		checkLen = len(data)
	}
	return bytes.ContainsRune(data[:checkLen], 0)
}

// sensitiveFileNames lists filenames that tools must never read, write, or
// edit. Matched against the base name of the resolved path (case-insensitive).
var sensitiveFileNames = []string{
	".env",
	".env.local",
	".env.production",
	".env.staging",
}

// isSensitiveFile returns true if the base filename matches the deny-list.
func isSensitiveFile(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	for _, name := range sensitiveFileNames {
		if base == name {
			return true
		}
	}
	return false
}

// resolvePath resolves a relative path against workDir and enforces that the
// result stays inside workDir. Absolute paths are rejected outright. Symlinks
// are resolved via filepath.EvalSymlinks and containment is re-checked on the
// real path to prevent symlink-based escapes. Files matching the sensitive
// filename deny-list (e.g. .env) are rejected to prevent secret leakage.
func resolvePath(path, workDir string) (string, error) {
	if filepath.IsAbs(path) {
		return "", fmt.Errorf("absolute paths are not allowed: %s", path)
	}
	joined := filepath.Join(workDir, path)
	cleaned := filepath.Clean(joined)
	cleanedRoot := filepath.Clean(workDir)

	if isSensitiveFile(cleaned) {
		return "", fmt.Errorf("access denied: %s is a sensitive file", filepath.Base(cleaned))
	}
	if cleaned != cleanedRoot &&
		!strings.HasPrefix(cleaned, cleanedRoot+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes working directory: %s", path)
	}

	// Resolve symlinks and re-check containment on the real path.
	realPath, err := filepath.EvalSymlinks(cleaned)
	if err != nil {
		if os.IsNotExist(err) {
			return cleaned, nil // file doesn't exist yet (e.g., write_file)
		}
		return "", fmt.Errorf("failed to resolve path: %w", err)
	}
	realRoot, err := filepath.EvalSymlinks(cleanedRoot)
	if err != nil {
		return "", fmt.Errorf("failed to resolve working directory: %w", err)
	}
	if realPath != realRoot &&
		!strings.HasPrefix(realPath, realRoot+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes working directory via symlink: %s", path)
	}
	return cleaned, nil
}
