// Command run-pipeline executes an Attractor pipeline from a DOT file.
//
// It parses the DOT pipeline file, validates it, runs pre-flight checks,
// and executes it with a real LLM backend (or a simulated one for testing).
//
// Usage: go run ./cmd/run-pipeline [-pipeline FILE] [-workdir DIR] [-budget TOKENS] [-simulate]
//
// Requires OPENROUTER_API_KEY in .env or environment (not needed with -simulate).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
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
	defaultPipeline     = "pipelines/retroquest-returns-v3.dot"
	defaultWorkDir      = "/Users/allison/workspace/retroquest-returns-v3"
	defaultBudgetTokens = 10_000_000 // safety net; actual usage should be well below with compression + model tiering
	defaultDockerImage  = "golang:1.23"
)

func main() {
	pipelineFile := flag.String("pipeline", defaultPipeline, "path to DOT pipeline file")
	budgetTokens := flag.Int("budget", defaultBudgetTokens, "max total tokens before stopping (0 = no limit)")
	workDir := flag.String("workdir", defaultWorkDir, "working directory for the agent")
	model := flag.String("model", defaultModel, "default LLM model")
	modelOverride := flag.String("model-override", "", "override ALL stage models with this model (useful for cheap test runs)")
	zdr := flag.Bool("zdr", false, "enforce Zero Data Retention routing on OpenRouter")
	dockerImage := flag.String("docker-image", defaultDockerImage, "Docker image for shell sandbox")
	noDocker := flag.Bool("no-docker", false, "skip Docker container setup (shell commands will fail)")
	simulate := flag.Bool("simulate", false, "use SimulatedBackend instead of real LLM (no API key or Docker needed)")
	flag.Parse()

	loadEnv()

	fmt.Println("=== Attractor Pipeline Runner ===")
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

	// 3. Pre-flight checks
	fmt.Println("[3] Pre-flight checks...")
	pfResult := runPreflight(g, *workDir, *model, *modelOverride, *budgetTokens, *simulate, *noDocker)
	for _, w := range pfResult.warnings {
		fmt.Printf("    (warning: %s)\n", w)
	}
	if pfResult.err != nil {
		log.Fatalf("Pre-flight failed: %v", pfResult.err)
	}
	fmt.Println("    Pre-flight passed.")

	// 4. Set up execution
	fmt.Println("[4] Setting up execution...")

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
	if *modelOverride != "" {
		fmt.Printf("    Model override: %s (all stages)\n", *modelOverride)
	} else {
		fmt.Printf("    Model: %s\n", *model)
	}
	if *zdr {
		fmt.Println("    ZDR: enabled (Zero Data Retention)")
	}
	if *budgetTokens > 0 {
		fmt.Printf("    Budget: %d tokens\n", *budgetTokens)
	} else {
		fmt.Println("    Budget: unlimited")
	}

	var registry *pipeline.HandlerRegistry

	if *simulate {
		fmt.Println("    Mode: SIMULATED (no LLM calls, no Docker)")
		fmt.Println("    Build gates: skipped (simulate mode)")
		registry = pipeline.DefaultHandlerRegistry(pipeline.CodergenHandler{Backend: pipeline.SimulatedBackend{}})
	} else {
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

		var clientOpts []llm.ClientOption
		if *zdr {
			clientOpts = append(clientOpts, llm.WithZDR())
		}
		client, err := llm.NewClientFromEnv(clientOpts...)
		if err != nil {
			log.Fatalf("LLM client error: %v", err)
		}

		backend := pipeline.AgentBackend{
			Client:        client,
			Model:         *model,
			ModelOverride: *modelOverride,
			WorkDir:       *workDir,
		}

		var checkRunner pipeline.CheckRunner
		if !*noDocker {
			checkRunner = makeCheckRunner()
			fmt.Println("    Build gates: enabled")
		} else {
			fmt.Println("    Build gates: disabled (--no-docker)")
		}

		registry = pipeline.DefaultHandlerRegistry(pipeline.CodergenHandler{
			Backend:     backend,
			CheckRunner: checkRunner,
		})
	}

	// 5. Run pipeline
	fmt.Println("[5] Running pipeline...")
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

	// 6. Report results
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

	// 7. Token usage summary
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

	// 8. Write summary JSON
	summaryPath := filepath.Join(logsRoot, "summary.json")
	effectiveModel := *model
	if *modelOverride != "" {
		effectiveModel = *modelOverride
	}
	summaryData := map[string]any{
		"status":          string(result.Status),
		"completed_nodes": result.CompletedNodes,
		"failure_reason":  result.FailureReason,
		"warnings":        result.Warnings,
		"total_usage":     result.TotalUsage,
		"stage_usages":    result.StageUsages,
		"elapsed_seconds": elapsed.Seconds(),
		"model":           effectiveModel,
		"model_override":  *modelOverride != "",
		"zdr":             *zdr,
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

type preflightResult struct {
	warnings []string
	err      error
}

func runPreflight(g *dot.Graph, workDir, model, modelOverride string, budgetTokens int, simulate, noDocker bool) preflightResult {
	var warnings []string
	var failures []string

	// 1. Work directory exists and is writable.
	info, err := os.Stat(workDir)
	if err != nil {
		fmt.Printf("    [FAIL] Work directory: %s does not exist\n", workDir)
		failures = append(failures, fmt.Sprintf("work directory %q does not exist", workDir))
	} else if !info.IsDir() {
		fmt.Printf("    [FAIL] Work directory: %s is not a directory\n", workDir)
		failures = append(failures, fmt.Sprintf("%q is not a directory", workDir))
	} else {
		tmp := filepath.Join(workDir, ".attractor-preflight-check")
		if err := os.WriteFile(tmp, []byte("ok"), 0o644); err != nil {
			fmt.Printf("    [FAIL] Work directory: %s is not writable\n", workDir)
			failures = append(failures, fmt.Sprintf("work directory %q is not writable: %v", workDir, err))
		} else {
			_ = os.Remove(tmp)
			fmt.Printf("    [ok] Work directory exists and is writable\n")
		}
	}

	// 2. API key is present and well-formed.
	if simulate {
		fmt.Printf("    [--] API key (skipped: simulate mode)\n")
	} else {
		apiKey := os.Getenv("OPENROUTER_API_KEY")
		if apiKey == "" {
			fmt.Printf("    [FAIL] API key: OPENROUTER_API_KEY is not set\n")
			failures = append(failures, "OPENROUTER_API_KEY environment variable is not set")
		} else if !strings.HasPrefix(apiKey, "sk-or-") {
			fmt.Printf("    [FAIL] API key: does not start with sk-or- (got %q...)\n", truncate(apiKey, 10))
			failures = append(failures, "OPENROUTER_API_KEY does not start with \"sk-or-\" -- check for stray quotes or wrong key")
		} else {
			fmt.Printf("    [ok] API key present and well-formed\n")
		}
	}

	// 3. Docker daemon is reachable.
	if simulate || noDocker {
		reason := "simulate mode"
		if noDocker {
			reason = "--no-docker"
		}
		fmt.Printf("    [--] Docker daemon (skipped: %s)\n", reason)
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := exec.CommandContext(ctx, "docker", "info").Run(); err != nil {
			fmt.Printf("    [FAIL] Docker daemon is not reachable\n")
			failures = append(failures, "Docker daemon is not reachable -- is Docker Desktop running?")
		} else {
			fmt.Printf("    [ok] Docker daemon is reachable\n")
		}
	}

	// 4. Model IDs exist on OpenRouter (falls back to format check if API unreachable).
	if simulate {
		fmt.Printf("    [--] Model validation (skipped: simulate mode)\n")
	} else {
		var allModels []string
		if modelOverride != "" {
			allModels = []string{modelOverride}
		} else {
			allModels = collectModelIDs(g, model)
		}
		knownModels, apiErr := fetchOpenRouterModels()
		if apiErr != nil {
			// API unreachable -- fall back to format check.
			var badFormat []string
			for _, m := range allModels {
				if !strings.Contains(m, "/") {
					badFormat = append(badFormat, m)
				}
			}
			if len(badFormat) > 0 {
				fmt.Printf("    [FAIL] Model IDs: invalid format (missing provider/ prefix): %s\n", strings.Join(badFormat, ", "))
				failures = append(failures, fmt.Sprintf("model IDs missing provider/ prefix: %s", strings.Join(badFormat, ", ")))
			} else {
				w := fmt.Sprintf("Could not reach OpenRouter to validate models (%v); format check passed", apiErr)
				fmt.Printf("    [!!] %s\n", w)
				warnings = append(warnings, w)
			}
		} else {
			var unknown []string
			for _, m := range allModels {
				if !knownModels[m] {
					unknown = append(unknown, m)
				}
			}
			if len(unknown) > 0 {
				fmt.Printf("    [FAIL] Model IDs not found on OpenRouter: %s\n", strings.Join(unknown, ", "))
				failures = append(failures, fmt.Sprintf("model IDs not found on OpenRouter: %s", strings.Join(unknown, ", ")))
			} else {
				fmt.Printf("    [ok] All %d model IDs verified on OpenRouter\n", len(allModels))
			}
		}
	}

	// 5. Budget sanity check (warnings only).
	codergenCount := 0
	for _, n := range g.Nodes {
		if n.Shape() == "box" || n.Type() == "codergen" {
			codergenCount++
		}
	}
	if budgetTokens > 0 && budgetTokens < 100_000 && codergenCount > 1 {
		w := fmt.Sprintf("Budget is very low (%d tokens) for a %d-stage pipeline; stages may be cut short", budgetTokens, codergenCount)
		fmt.Printf("    [!!] %s\n", w)
		warnings = append(warnings, w)
	} else if budgetTokens > 20_000_000 {
		w := fmt.Sprintf("Budget is very high (%d tokens); consider lowering for safety", budgetTokens)
		fmt.Printf("    [!!] %s\n", w)
		warnings = append(warnings, w)
	} else {
		fmt.Printf("    [ok] Budget is reasonable\n")
	}

	if len(failures) > 0 {
		return preflightResult{
			warnings: warnings,
			err:      fmt.Errorf("pre-flight failed:\n    - %s", strings.Join(failures, "\n    - ")),
		}
	}
	return preflightResult{warnings: warnings}
}

func collectModelIDs(g *dot.Graph, defaultModel string) []string {
	seen := make(map[string]bool)
	seen[defaultModel] = true
	for _, n := range g.Nodes {
		if m := n.Model(); m != "" {
			seen[m] = true
		}
	}
	models := make([]string, 0, len(seen))
	for m := range seen {
		models = append(models, m)
	}
	return models
}

func makeCheckRunner() pipeline.CheckRunner {
	return func(cmd string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		dockerCmd := exec.CommandContext(ctx, "docker", "exec", "attractor-sandbox", "sh", "-c", cmd)
		out, err := dockerCmd.CombinedOutput()
		return string(out), err
	}
}

func fetchOpenRouterModels() (map[string]bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://openrouter.ai/api/v1/models", nil)
	if err != nil {
		return nil, err
	}
	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	models := make(map[string]bool, len(result.Data))
	for _, m := range result.Data {
		models[m.ID] = true
	}
	return models, nil
}
