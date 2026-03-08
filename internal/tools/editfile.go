package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/campallison/attractor/internal/llm"
)

type editFileArgs struct {
	FilePath   string `json:"file_path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all"`
}

// EditFileTool returns the registered edit_file tool.
func EditFileTool() RegisteredTool {
	return RegisteredTool{
		Definition: llm.ToolDefinition{
			Name:        "edit_file",
			Description: "Replace an exact string occurrence in a file.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"file_path": {
						"type": "string",
						"description": "Relative path to the file (must stay within working directory)"
					},
					"old_string": {
						"type": "string",
						"description": "Exact text to find in the file"
					},
					"new_string": {
						"type": "string",
						"description": "Replacement text"
					},
					"replace_all": {
						"type": "boolean",
						"description": "Replace all occurrences (default: false)"
					}
				},
				"required": ["file_path", "old_string", "new_string"]
			}`),
		},
		Execute: executeEditFile,
	}
}

// executeEditFile reads the file, performs the replacement in memory, and writes
// it back. This is not atomic -- concurrent modifications to the file between
// the read and write will be silently overwritten. This is acceptable in the
// current single-threaded agent loop but should be revisited if parallel tool
// execution is added.
func executeEditFile(rawArgs json.RawMessage, workDir string) (string, error) {
	var args editFileArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return "", fmt.Errorf("invalid edit_file arguments: %w", err)
	}

	path, err := resolvePath(args.FilePath, workDir)
	if err != nil {
		return "", fmt.Errorf("edit_file: %w", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("edit_file: %w", err)
	}

	content := string(data)
	count := strings.Count(content, args.OldString)

	if count == 0 {
		return "", fmt.Errorf("edit_file: old_string not found in %s", args.FilePath)
	}

	if !args.ReplaceAll && count > 1 {
		return "", fmt.Errorf("edit_file: old_string is not unique in %s (%d occurrences). Provide more context or set replace_all=true", args.FilePath, count)
	}

	var result string
	if args.ReplaceAll {
		result = strings.ReplaceAll(content, args.OldString, args.NewString)
	} else {
		result = strings.Replace(content, args.OldString, args.NewString, 1)
	}

	if err := os.WriteFile(path, []byte(result), 0o644); err != nil {
		return "", fmt.Errorf("edit_file: %w", err)
	}

	if args.ReplaceAll {
		return fmt.Sprintf("Replaced %d occurrences in %s", count, args.FilePath), nil
	}
	return fmt.Sprintf("Replaced 1 occurrence in %s", args.FilePath), nil
}
