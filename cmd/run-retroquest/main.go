// Command run-retroquest executes the RetroQuest Returns pipeline.
//
// It parses the DOT pipeline file, validates it, and runs it with a real LLM
// backend to build the RetroQuest Returns application end-to-end.
//
// Usage: go run ./cmd/run-retroquest [-budget TOKENS] [-pipeline FILE] [-docker-image IMAGE]
//
// Requires OPENROUTER_API_KEY in .env or environment.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/campallison/attractor/internal/dot"
	"github.com/campallison/attractor/internal/llm"
	"github.com/campallison/attractor/internal/logging"
	"github.com/campallison/attractor/internal/pipeline"
	"github.com/campallison/attractor/internal/tools"
)

const (
	defaultModel        = "anthropic/claude-opus-4.6"
	defaultPipeline     = "pipelines/retroquest-returns-v2.dot"
	defaultWorkDir      = "/Users/allison/workspace/retroquest-returns-v2"
	defaultBudgetTokens = 10_000_000 // safety net; actual usage should be well below with compression + model tiering
	defaultDockerImage  = "golang:1.23"
)

func main() {
	pipelineFile := flag.String("pipeline", defaultPipeline, "path to DOT pipeline file")
	budgetTokens := flag.Int("budget", defaultBudgetTokens, "max total tokens before stopping (0 = no limit)")
	workDir := flag.String("workdir", defaultWorkDir, "working directory for the agent")
	model := flag.String("model", defaultModel, "default LLM model")
	dockerImage := flag.String("docker-image", defaultDockerImage, "Docker image for shell sandbox")
	noDocker := flag.Bool("no-docker", false, "skip Docker container setup (shell commands will fail)")
	flag.Parse()

	loadEnv()

	fmt.Println("=== RetroQuest Returns Pipeline ===")
	fmt.Println()

	// 1. Load and parse pipeline
	fmt.Printf("[1] Loading pipeline from %s...\n", *pipelineFile)
	dotSource, err := os.ReadFile(*pipelineFile)
	if err != nil {
		log.Fatalf("Failed to read pipeline file: %v", err)
	}

	g, err := dot.Parse(string(dotSource))
	if err != nil {
		log.Fatalf("Parse error: %v", err)
	}
	fmt.Printf("    Graph: %s (%d nodes, %d edges)\n", g.Name, len(g.Nodes), len(g.Edges))
	fmt.Printf("    Goal: %s\n", truncate(g.Goal(), 80))

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

	// 3. Set up execution
	fmt.Println("[3] Setting up execution...")

	logsRoot := filepath.Join(*workDir, ".attractor-logs", time.Now().Format("2006-01-02_15-04-05"))
	if err := os.MkdirAll(logsRoot, 0o755); err != nil {
		log.Fatalf("Failed to create logs directory: %v", err)
	}

	logFilePath := filepath.Join(logsRoot, "pipeline.log")
	if err := logging.Setup(logFilePath); err != nil {
		log.Fatalf("Failed to set up logging: %v", err)
	}
	defer logging.Teardown()

	fmt.Printf("    Logs: %s\n", logsRoot)
	fmt.Printf("    Log file: %s\n", logFilePath)
	fmt.Printf("    Work dir: %s\n", *workDir)
	fmt.Printf("    Model: %s\n", *model)
	if *budgetTokens > 0 {
		fmt.Printf("    Budget: %d tokens\n", *budgetTokens)
	} else {
		fmt.Println("    Budget: unlimited")
	}

	// 3b. Start Docker sandbox
	if *noDocker {
		fmt.Println("    Docker: DISABLED (shell commands will fail)")
	} else {
		fmt.Printf("    Docker: starting container with image %s...\n", *dockerImage)
		if err := tools.EnsureContainer(*dockerImage, *workDir); err != nil {
			log.Fatalf("Failed to start Docker sandbox: %v", err)
		}
		defer func() {
			fmt.Println("Stopping Docker sandbox...")
			if err := tools.StopContainer(); err != nil {
				fmt.Printf("Warning: failed to stop container: %v\n", err)
			}
		}()
		fmt.Println("    Docker: container running")
	}

	client, err := llm.NewClientFromEnv()
	if err != nil {
		log.Fatalf("LLM client error: %v", err)
	}

	backend := pipeline.AgentBackend{
		Client:  client,
		Model:   *model,
		WorkDir: *workDir,
	}

	registry := pipeline.DefaultHandlerRegistry(backend)

	// 4. Run pipeline
	fmt.Println("[4] Running pipeline...")
	fmt.Println()
	startTime := time.Now()

	result, err := pipeline.Run(pipeline.RunConfig{
		Graph:           g,
		LogsRoot:        logsRoot,
		Registry:        registry,
		MaxBudgetTokens: *budgetTokens,
	})
	elapsed := time.Since(startTime)

	if err != nil {
		log.Fatalf("Pipeline execution error: %v", err)
	}

	// 5. Report results
	fmt.Println()
	fmt.Println("=== Pipeline Results ===")
	fmt.Printf("Status: %s\n", result.Status)
	fmt.Printf("Duration: %s\n", elapsed.Round(time.Second))
	fmt.Printf("Completed nodes: %v\n", result.CompletedNodes)

	if result.FailureReason != "" {
		fmt.Printf("Failure reason: %s\n", result.FailureReason)
	}

	if len(result.Warnings) > 0 {
		fmt.Println("Warnings:")
		for _, w := range result.Warnings {
			fmt.Printf("  - %s\n", w)
		}
	}

	// 6. Token usage summary
	fmt.Println()
	fmt.Println("=== Token Usage ===")
	fmt.Printf("Total: %d tokens (input: %d, output: %d)\n",
		result.TotalUsage.TotalTokens, result.TotalUsage.InputTokens, result.TotalUsage.OutputTokens)

	if len(result.StageUsages) > 0 {
		fmt.Println()
		fmt.Printf("%-20s %8s %8s %8s %6s  %s\n", "Stage", "Input", "Output", "Total", "Rounds", "Model")
		fmt.Println("-------------------- -------- -------- -------- ------  -----")
		for _, nodeID := range result.CompletedNodes {
			if su, ok := result.StageUsages[nodeID]; ok {
				fmt.Printf("%-20s %8d %8d %8d %6d  %s\n",
					truncate(nodeID, 20), su.InputTokens, su.OutputTokens, su.TotalTokens, su.Rounds, su.Model)
			}
		}
	}

	// 7. Write summary JSON
	summaryPath := filepath.Join(logsRoot, "summary.json")
	summaryData := map[string]any{
		"status":          string(result.Status),
		"completed_nodes": result.CompletedNodes,
		"failure_reason":  result.FailureReason,
		"warnings":        result.Warnings,
		"total_usage":     result.TotalUsage,
		"stage_usages":    result.StageUsages,
		"elapsed_seconds": elapsed.Seconds(),
		"model":           *model,
		"budget_tokens":   *budgetTokens,
	}
	if data, err := json.MarshalIndent(summaryData, "", "  "); err == nil {
		_ = os.WriteFile(summaryPath, data, 0o644)
		fmt.Printf("\nSummary written to %s\n", summaryPath)
	}

	if result.Status == pipeline.StatusFail {
		fmt.Println("\nPipeline FAILED.")
		os.Exit(1)
	}

	fmt.Println("\nPipeline completed successfully!")
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func loadEnv() {
	data, err := os.ReadFile(".env")
	if err != nil {
		return
	}
	for _, line := range splitLines(string(data)) {
		if idx := indexOf(line, '='); idx > 0 {
			key := strings.TrimSpace(line[:idx])
			val := strings.TrimSpace(line[idx+1:])
			val = strings.Trim(val, `"'`)
			if key != "" {
				os.Setenv(key, val)
			}
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
