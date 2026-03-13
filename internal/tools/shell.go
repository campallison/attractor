package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/campallison/attractor/internal/llm"
)

const (
	defaultTimeoutMs = 10_000
	maxTimeoutMs     = 600_000
)

type shellArgs struct {
	Command   string `json:"command"`
	TimeoutMs int    `json:"timeout_ms"`
}

// ShellTool returns the registered shell tool. Commands are executed inside a
// Docker container identified by containerName.
func ShellTool(containerName string) RegisteredTool {
	return RegisteredTool{
		Definition: llm.ToolDefinition{
			Name:        "shell",
			Description: "Execute a shell command inside a Docker container. Returns stdout, stderr, and exit code.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"command": {
						"type": "string",
						"description": "The shell command to run"
					},
					"timeout_ms": {
						"type": "integer",
						"description": "Timeout in milliseconds (default: 10000, max: 600000)"
					}
				},
				"required": ["command"]
			}`),
		},
		Execute: makeShellExecutor(containerName),
	}
}

// makeShellExecutor returns a ToolExecutor that runs commands via docker exec.
//
// Timeout limitation: context.WithTimeout only kills the local docker-exec
// client process. The command running inside the container may continue after
// the client disconnects. For stronger enforcement, wrap commands with the
// `timeout` utility inside the container (e.g., `sh -c "timeout 120 <cmd>"`).
func makeShellExecutor(containerName string) ToolExecutor {
	return func(ctx context.Context, rawArgs json.RawMessage, workDir string) (string, error) {
		var args shellArgs
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			return "", fmt.Errorf("invalid shell arguments: %w", err)
		}

		if reason := isDeniedCommand(args.Command); reason != "" {
			slog.Warn("tool.shell.denied", "cmd", truncateStr(args.Command, 120), "reason", reason)
			return "", fmt.Errorf("shell: command blocked: %s", reason)
		}

		timeoutMs := args.TimeoutMs
		if timeoutMs <= 0 {
			timeoutMs = defaultTimeoutMs
		}
		if timeoutMs > maxTimeoutMs {
			timeoutMs = maxTimeoutMs
		}

		timeout := time.Duration(timeoutMs) * time.Millisecond
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		cmd := exec.CommandContext(ctx, "docker", "exec", containerName, "sh", "-c", args.Command)

		// Filter sensitive environment variables from being passed through.
		cmd.Env = filterEnvVars(os.Environ())

		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		start := time.Now()
		err := cmd.Run()
		duration := time.Since(start)

		var output strings.Builder
		if stdout.Len() > 0 {
			output.Write(stdout.Bytes())
		}
		if stderr.Len() > 0 {
			if output.Len() > 0 {
				output.WriteString("\n")
			}
			output.Write(stderr.Bytes())
		}

		exitCode := 0
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else if ctx.Err() == context.DeadlineExceeded {
				output.WriteString(fmt.Sprintf(
					"\n[ERROR: Command timed out after %dms. Partial output is shown above. "+
						"You can retry with a longer timeout by setting the timeout_ms parameter.]",
					timeoutMs,
				))
				exitCode = -1
			} else {
				return "", fmt.Errorf("shell: %w", err)
			}
		}

		slog.Info("tool.shell", "cmd", truncateStr(args.Command, 120), "exit", exitCode, "duration_ms", duration.Milliseconds())
		return fmt.Sprintf("Exit code: %d\nDuration: %dms\n\n%s",
			exitCode, duration.Milliseconds(), output.String()), nil
	}
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// filterEnvVars removes environment variables that may contain secrets.
// It keeps standard vars like PATH, HOME, GOPATH, etc.
func filterEnvVars(environ []string) []string {
	filtered := make([]string, 0, len(environ))
	for _, env := range environ {
		key, _, ok := strings.Cut(env, "=")
		if !ok {
			continue
		}
		if isSensitiveKey(key) {
			continue
		}
		filtered = append(filtered, env)
	}
	return filtered
}

// isSensitiveKey returns true if the environment variable name matches
// patterns known to contain secrets. The suffix list intentionally casts a
// wide net -- in a Docker sandbox, over-filtering is safer than leaking.
func isSensitiveKey(key string) bool {
	upper := strings.ToUpper(key)
	suffixes := []string{
		"_API_KEY", "_SECRET", "_TOKEN", "_PASSWORD", "_CREDENTIAL",
		"_KEY", "_CREDENTIALS", "_PASSWD", "_AUTH", "_PRIVATE",
	}
	for _, s := range suffixes {
		if strings.HasSuffix(upper, s) {
			return true
		}
	}
	return false
}

// deniedGitSubcommands lists git subcommands that agents must not execute.
// Safe commands (add, commit, status, diff, log, stash) are allowed.
var deniedGitSubcommands = []string{
	"push", "remote", "config", "clean", "rebase",
}

// isDeniedCommand checks whether a shell command contains a blocked operation.
// Returns a human-readable reason if denied, or empty string if allowed.
func isDeniedCommand(command string) string {
	tokens := strings.Fields(command)
	for i, tok := range tokens {
		if tok != "git" {
			continue
		}
		if i+1 >= len(tokens) {
			continue
		}
		sub := tokens[i+1]
		for _, denied := range deniedGitSubcommands {
			if sub == denied {
				return fmt.Sprintf("git %s is not allowed", denied)
			}
		}
		if sub == "reset" && containsFlag(tokens[i+2:], "--hard") {
			return "git reset --hard is not allowed"
		}
	}
	return ""
}

// containsFlag checks if a flag appears in a slice of command tokens.
func containsFlag(tokens []string, flag string) bool {
	for _, t := range tokens {
		if t == flag {
			return true
		}
	}
	return false
}

// EnsureContainer starts a named Docker container if it is not already
// running. It mounts workDir into /workspace inside the container.
//
// This function is not called automatically by the shell tool executor.
// The caller (e.g., the pipeline runner or test harness) is responsible for
// calling EnsureContainer before running tasks and StopContainer afterward.
func EnsureContainer(dockerImage, containerName, workDir string) error {
	check := exec.Command("docker", "inspect", "-f", "{{.State.Running}}", containerName)
	out, err := check.Output()
	if err == nil && strings.TrimSpace(string(out)) == "true" {
		return nil
	}

	_ = exec.Command("docker", "rm", "-f", containerName).Run()

	run := exec.Command("docker", "run", "-d",
		"--name", containerName,
		"-v", workDir+":/workspace",
		"-w", "/workspace",
		dockerImage,
		"sleep", "infinity",
	)
	run.Stdout = os.Stdout
	run.Stderr = os.Stderr
	return run.Run()
}

// StopContainer stops and removes the named sandbox container.
//
// Must be called explicitly by the caller when the pipeline run ends.
// If not called, the container will continue running indefinitely.
func StopContainer(containerName string) error {
	return exec.Command("docker", "rm", "-f", containerName).Run()
}
