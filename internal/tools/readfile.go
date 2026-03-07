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
						"description": "Absolute or relative path to the file"
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

	path := resolvePath(args.FilePath, workDir)

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

// resolvePath makes a relative path absolute against the working directory.
func resolvePath(path, workDir string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(workDir, path)
}
