// Command test-pipeline runs a simple Attractor pipeline end-to-end.
// It parses an embedded DOT pipeline, validates it, and executes it with a
// real LLM backend via the Layer 2 agent loop.
//
// Usage: go run ./cmd/test-pipeline
//
// Requires OPENROUTER_API_KEY in .env or environment.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/campallison/attractor/internal/dot"
	"github.com/campallison/attractor/internal/llm"
	"github.com/campallison/attractor/internal/pipeline"
	"github.com/campallison/attractor/internal/tools"
)

const testPipeline = `digraph TestPipeline {
	graph [goal="Create a Python hello-world script"]
	rankdir=LR

	start [shape=Mdiamond, label="Start"]
	exit  [shape=Msquare, label="Exit"]

	plan [
		shape=box,
		label="Plan",
		prompt="Briefly plan how to create a simple Python hello-world script that prints 'Hello, Attractor!' to stdout. Just describe the plan in 2-3 sentences, do not write any code yet."
	]

	implement [
		shape=box,
		label="Implement",
		prompt="Based on the plan, create the hello-world Python script. Write a file called hello.py that prints 'Hello, Attractor!' to stdout.",
		goal_gate=true
	]

	start -> plan -> implement -> exit
}`

func main() {
	loadEnv()

	fmt.Println("=== Attractor Pipeline Test ===")
	fmt.Println()

	// 1. Parse
	fmt.Println("[1] Parsing pipeline...")
	g, err := dot.Parse(testPipeline)
	if err != nil {
		log.Fatalf("Parse error: %v", err)
	}
	fmt.Printf("    Graph: %s (%d nodes, %d edges)\n", g.Name, len(g.Nodes), len(g.Edges))
	fmt.Printf("    Goal: %s\n", g.Goal())

	// 2. Validate
	fmt.Println("[2] Validating...")
	diags, err := pipeline.ValidateOrError(g)
	if err != nil {
		log.Fatalf("Validation error: %v", err)
	}
	for _, d := range diags {
		fmt.Printf("    %s\n", d)
	}
	fmt.Println("    Validation passed.")

	// 3. Execute
	fmt.Println("[3] Executing pipeline...")

	logsRoot, err := os.MkdirTemp("", "attractor-pipeline-*")
	if err != nil {
		log.Fatalf("Failed to create logs directory: %v", err)
	}
	defer os.RemoveAll(logsRoot)
	fmt.Printf("    Logs: %s\n", logsRoot)

	client, err := llm.NewClientFromEnv()
	if err != nil {
		log.Fatalf("LLM client error: %v", err)
	}

	workDir, err := os.MkdirTemp("", "attractor-workdir-*")
	if err != nil {
		log.Fatalf("Failed to create work directory: %v", err)
	}
	defer os.RemoveAll(workDir)

	backend := pipeline.AgentBackend{
		Client:   client,
		Model:    "anthropic/claude-sonnet-4",
		WorkDir:  workDir,
		Registry: tools.DefaultRegistry("attractor-sandbox"),
	}

	registry := pipeline.DefaultHandlerRegistry(pipeline.CodergenHandler{Backend: backend})

	result, err := pipeline.Run(context.Background(), pipeline.RunConfig{
		Graph:    g,
		LogsRoot: logsRoot,
		Registry: registry,
	})
	if err != nil {
		log.Fatalf("Pipeline execution error: %v", err)
	}

	// 4. Report
	fmt.Println()
	fmt.Println("[4] Results:")
	fmt.Printf("    Status: %s\n", result.Status)
	fmt.Printf("    Completed nodes: %v\n", result.CompletedNodes)

	if result.Status == pipeline.StatusFail {
		fmt.Printf("    Failure: %s\n", result.FailureReason)
		os.Exit(1)
	}

	// 5. Verify artifacts
	fmt.Println("[5] Verifying artifacts...")
	for _, nodeID := range []string{"plan", "implement"} {
		for _, file := range []string{"prompt.md", "response.md", "status.json"} {
			path := filepath.Join(logsRoot, nodeID, file)
			info, err := os.Stat(path)
			if err != nil {
				fmt.Printf("    MISSING: %s/%s\n", nodeID, file)
			} else {
				fmt.Printf("    OK: %s/%s (%d bytes)\n", nodeID, file, info.Size())
			}
		}
	}

	// 6. Verify checkpoint
	cp, err := pipeline.LoadCheckpoint(filepath.Join(logsRoot, "checkpoint.json"))
	if err != nil {
		fmt.Printf("    MISSING: checkpoint.json\n")
	} else {
		fmt.Printf("    OK: checkpoint.json (last node: %s, completed: %v)\n", cp.CurrentNode, cp.CompletedNodes)
	}

	fmt.Println()
	fmt.Println("=== Pipeline test complete ===")
}

func loadEnv() {
	data, err := os.ReadFile(".env")
	if err != nil {
		return
	}
	for _, line := range splitLines(string(data)) {
		if idx := indexOf(line, '='); idx > 0 {
			key := line[:idx]
			val := line[idx+1:]
			os.Setenv(key, val)
		}
	}
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			line := s[start:i]
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			lines = append(lines, line)
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func indexOf(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}
