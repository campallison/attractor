package agent

import (
	"fmt"
	"runtime"
	"time"
)

// BuildSystemPrompt constructs the system prompt for the coding agent.
// It includes the working directory, platform, and current date.
func BuildSystemPrompt(workDir string) string {
	return fmt.Sprintf(`You are a coding agent. You have access to tools for reading, writing, and editing files, and for running shell commands. Use these tools to complete the user's task.

When creating or modifying files, use the write_file tool to create new files and the edit_file tool to modify existing files. Use read_file to inspect files before editing them.

Working directory: %s
Platform: %s
Date: %s`, workDir, runtime.GOOS, time.Now().Format("2006-01-02"))
}
