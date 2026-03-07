package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/campallison/attractor/internal/llm"
)

type writeFileArgs struct {
	FilePath string `json:"file_path"`
	Content  string `json:"content"`
}

// WriteFileTool returns the registered write_file tool.
func WriteFileTool() RegisteredTool {
	return RegisteredTool{
		Definition: llm.ToolDefinition{
			Name:        "write_file",
			Description: "Write content to a file. Creates the file and parent directories if needed.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"file_path": {
						"type": "string",
						"description": "Relative path to the file (must stay within working directory)"
					},
					"content": {
						"type": "string",
						"description": "The full file content to write"
					}
				},
				"required": ["file_path", "content"]
			}`),
		},
		Execute: executeWriteFile,
	}
}

func executeWriteFile(rawArgs json.RawMessage, workDir string) (string, error) {
	var args writeFileArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return "", fmt.Errorf("invalid write_file arguments: %w", err)
	}

	path, err := resolvePath(args.FilePath, workDir)
	if err != nil {
		return "", fmt.Errorf("write_file: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("write_file: failed to create directories: %w", err)
	}

	if err := os.WriteFile(path, []byte(args.Content), 0o644); err != nil {
		return "", fmt.Errorf("write_file: %w", err)
	}

	return fmt.Sprintf("Wrote %d bytes to %s", len(args.Content), args.FilePath), nil
}
