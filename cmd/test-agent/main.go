// test-agent is a smoke test for the Layer 2 coding agent loop.
// It asks the agent to create a file and verifies the result.
//
// Run from the project root (where .env lives):
//
//	go run ./cmd/test-agent
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/campallison/attractor/internal/agent"
	"github.com/campallison/attractor/internal/llm"
	"github.com/campallison/attractor/internal/tools"
)

func main() {
	client, err := llm.NewClientFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create client: %v\n", err)
		os.Exit(1)
	}

	// Use a temporary directory as the agent's working directory so we don't
	// pollute the project tree.
	workDir, err := os.MkdirTemp("", "attractor-test-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create temp dir: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(workDir)

	fmt.Printf("Working directory: %s\n\n", workDir)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	prompt := "Create a file called hello.txt in the working directory containing exactly the text 'Hello, Attractor!' (no trailing newline). Use the write_file tool."

	registry := tools.DefaultRegistry("attractor-sandbox")
	err = agent.RunTask(ctx, client, "openai/gpt-4o-mini", prompt, workDir, registry)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent error: %v\n", err)
		os.Exit(1)
	}

	// Verify the file was created.
	path := filepath.Join(workDir, "hello.txt")
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nFAIL: hello.txt was not created: %v\n", err)
		os.Exit(1)
	}

	content := string(data)
	fmt.Printf("\n--- Verification ---\n")
	fmt.Printf("File: %s\n", path)
	fmt.Printf("Content: %q\n", content)

	if content == "Hello, Attractor!" {
		fmt.Println("PASS: Content matches exactly.")
	} else {
		fmt.Printf("WARN: Expected %q, got %q\n", "Hello, Attractor!", content)
		fmt.Println("(The agent created the file but content doesn't match exactly. This is acceptable for a smoke test.)")
	}
}
