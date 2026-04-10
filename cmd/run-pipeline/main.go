// Command run-pipeline executes an Attractor pipeline from a DOT file.
//
// It parses the DOT pipeline file, validates it, runs pre-flight checks,
// and executes it with a real LLM backend (or a simulated one for testing).
//
// Usage: go run ./cmd/run-pipeline -pipeline FILE -workdir DIR [-budget TOKENS] [-simulate]
//
// Requires OPENROUTER_API_KEY in .env or environment (not needed with -simulate).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/campallison/attractor/internal/dot"
	"github.com/campallison/attractor/internal/llm"
	"github.com/campallison/attractor/internal/logging"
	"github.com/campallison/attractor/internal/pipeline"
	"github.com/campallison/attractor/internal/store"
	"github.com/campallison/attractor/internal/tools"
	"github.com/google/uuid"
)

const (
	defaultModel        = "anthropic/claude-opus-4.6"
	defaultBudgetTokens = 10_000_000 // safety net; actual usage should be well below with compression + model tiering
	defaultDockerImage  = "golang:1.26"
)

func main() {
	os.Exit(run())
}

func run() int {
	pipelineFile := flag.String("pipeline", "", "path to DOT pipeline file (required)")
	budgetTokens := flag.Int("budget", defaultBudgetTokens, "max total tokens before stopping (0 = no limit)")
	workDir := flag.String("workdir", "", "working directory for the agent (required)")
	model := flag.String("model", defaultModel, "default LLM model")
	modelOverride := flag.String("model-override", "", "override ALL stage models with this model (useful for cheap test runs)")
	zdr := flag.Bool("zdr", false, "enforce Zero Data Retention routing on OpenRouter")
	promptCache := flag.Bool("prompt-cache", true, "prompt caching for Anthropic models (default on; disable with -prompt-cache=false)")
	dockerImage := flag.String("docker-image", defaultDockerImage, "Docker image for shell sandbox")
	noDocker := flag.Bool("no-docker", false, "skip Docker container setup (shell commands will fail)")
	companionDB := flag.Bool("companion-db", false, "start a companion PostgreSQL container for behavioral validation")
	simulate := flag.Bool("simulate", false, "use SimulatedBackend instead of real LLM (no API key or Docker needed)")
	flag.Parse()

	if *pipelineFile == "" {
		fmt.Fprintln(os.Stderr, "Error: -pipeline flag is required")
		flag.Usage()
		return 1
	}
	if *workDir == "" {
		fmt.Fprintln(os.Stderr, "Error: -workdir flag is required")
		flag.Usage()
		return 1
	}

	loadEnv()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		fmt.Fprintln(os.Stderr, "\nForced shutdown.")
		os.Exit(1)
	}()

	fmt.Println("=== Attractor Pipeline Runner ===")
	fmt.Println()

	// 1. Load and parse pipeline
	fmt.Printf("[1] Loading pipeline from %s...\n", *pipelineFile)
	dotSource, err := os.ReadFile(*pipelineFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to read pipeline file: %v\n", err)
		return 1
	}

	g, err := dot.Parse(string(dotSource))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Parse error: %v\n", err)
		return 1
	}
	fmt.Printf("    Graph: %s (%d nodes, %d edges)\n", g.Name, len(g.Nodes), len(g.Edges))
	fmt.Printf("    Goal: %s\n", truncate(g.Goal(), 80))

	// 2. Validate
	fmt.Println("[2] Validating...")
	diags, err := pipeline.ValidateOrError(g)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Validation error: %v\n", err)
		return 1
	}
	for _, d := range diags {
		fmt.Printf("    %s\n", d)
	}
	fmt.Println("    Validation passed.")

	// 3. Pre-flight checks
	fmt.Println("[3] Pre-flight checks...")
	pfChecks := evaluatePreflight(preflightConfig{
		Graph:         g,
		WorkDir:       *workDir,
		Model:         *model,
		ModelOverride: *modelOverride,
		BudgetTokens:  *budgetTokens,
		Simulate:      *simulate,
		NoDocker:      *noDocker,
		APIKey:        os.Getenv("OPENROUTER_API_KEY"),
	})
	printPreflightChecks(pfChecks)
	pfResult := preflightChecksToResult(pfChecks)
	for _, w := range pfResult.warnings {
		fmt.Printf("    (warning: %s)\n", w)
	}
	if pfResult.err != nil {
		fmt.Fprintf(os.Stderr, "Pre-flight failed: %v\n", pfResult.err)
		return 1
	}
	fmt.Println("    Pre-flight passed.")

	// 4. Set up execution
	fmt.Println("[4] Setting up execution...")

	logsRoot := filepath.Join(*workDir, ".attractor-logs", time.Now().Format("2006-01-02_15-04-05"))
	if err := os.MkdirAll(logsRoot, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create logs directory: %v\n", err)
		return 1
	}

	logFilePath := filepath.Join(logsRoot, "pipeline.log")
	teardownLog, err := logging.Setup(logFilePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to set up logging: %v\n", err)
		return 1
	}
	defer teardownLog()

	// Observability database: connect if ATTRACTOR_DB_URL is set.
	var recorder store.RunRecorder = store.NopRecorder{}
	if dbURL := os.Getenv("ATTRACTOR_DB_URL"); dbURL != "" {
		pgStore, dbErr := store.NewPostgresStore(context.Background(), dbURL)
		if dbErr != nil {
			fmt.Printf("    [!!] Database unavailable: %v (continuing without persistence)\n", dbErr)
		} else {
			recorder = pgStore
			defer pgStore.Close()
			fmt.Println("    [ok] Observability database connected")
		}
	} else {
		fmt.Println("    [--] Observability database (ATTRACTOR_DB_URL not set)")
	}

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
	if *promptCache {
		fmt.Println("    Prompt caching: enabled")
	}
	if *budgetTokens > 0 {
		fmt.Printf("    Budget: %d tokens\n", *budgetTokens)
	} else {
		fmt.Println("    Budget: unlimited")
	}

	// Record run start early so the run ID can seed the sandbox name.
	effectiveModel := *model
	if *modelOverride != "" {
		effectiveModel = *modelOverride
	}

	runID, startErr := recorder.StartRun(context.Background(), store.PipelineRun{
		PipelineFile:  *pipelineFile,
		GraphName:     g.Name,
		Goal:          g.Goal(),
		DefaultModel:  *model,
		ModelOverride: *modelOverride,
		Simulate:      *simulate,
		DockerImage:   *dockerImage,
		BudgetTokens:  *budgetTokens,
		StagesTotal:   countTaskNodes(g),
	})
	if startErr != nil {
		fmt.Printf("    [!!] Failed to record run start: %v (continuing without persistence)\n", startErr)
		runID = uuid.Nil
		recorder = store.NopRecorder{}
	}

	var handlerRegistry *pipeline.HandlerRegistry

	if *simulate {
		fmt.Println("    Mode: SIMULATED (no LLM calls, no Docker)")
		fmt.Println("    Build gates: skipped (simulate mode)")
		handlerRegistry = pipeline.DefaultHandlerRegistry(pipeline.CodergenHandler{Backend: pipeline.SimulatedBackend{}})
	} else {
		containerName := sandboxName(runID)

		if *noDocker {
			fmt.Println("    Docker: DISABLED (shell commands will fail)")
		} else {
			fmt.Printf("    Docker: starting container %s with image %s...\n", containerName, *dockerImage)
			if err := tools.EnsureContainer(*dockerImage, containerName, *workDir); err != nil {
				fmt.Fprintf(os.Stderr, "Failed to start Docker sandbox: %v\n", err)
				return 1
			}
			defer func() {
				fmt.Printf("Stopping Docker sandbox %s...\n", containerName)
				if err := tools.StopContainer(containerName); err != nil {
					fmt.Printf("Warning: failed to stop container: %v\n", err)
				}
			}()
			fmt.Printf("    Docker: container %s running\n", containerName)

			attractorRoot, err := os.Getwd()
			if err != nil {
				fmt.Printf("    Check tools: not provisioned (cannot determine working directory: %v)\n", err)
			} else {
				results := tools.ProvisionCheckTools(ctx, attractorRoot, containerName)
				for name, err := range results {
					if err != nil {
						fmt.Printf("    Check tools: %s not provisioned (%v)\n", name, err)
					} else {
						fmt.Printf("    Check tools: %s provisioned\n", name)
					}
				}
			}

			if *companionDB {
				networkName := containerName + "-net"
				dbContainerName := containerName + "-db"
				fmt.Printf("    Companion DB: starting %s...\n", dbContainerName)
				if err := tools.SetupCompanionDB(ctx, networkName, dbContainerName, containerName); err != nil {
					_ = tools.TeardownCompanionDB(ctx, networkName, dbContainerName, containerName)
					fmt.Fprintf(os.Stderr, "Failed to set up companion database: %v\n", err)
					return 1
				}
				defer func() {
					fmt.Printf("Stopping companion DB %s...\n", dbContainerName)
					if err := tools.TeardownCompanionDB(ctx, networkName, dbContainerName, containerName); err != nil {
						fmt.Printf("Warning: failed to tear down companion DB: %v\n", err)
					}
				}()
				fmt.Printf("    Companion DB: ready (DATABASE_URL written to sandbox)\n")
			}
		}

		toolRegistry := tools.DefaultRegistry(containerName)

		var clientOpts []llm.ClientOption
		if *zdr {
			clientOpts = append(clientOpts, llm.WithZDR())
		}
		if *promptCache {
			clientOpts = append(clientOpts, llm.WithPromptCaching())
		}
		client, err := llm.NewClientFromEnv(clientOpts...)
		if err != nil {
			fmt.Fprintf(os.Stderr, "LLM client error: %v\n", err)
			return 1
		}

		backend := pipeline.AgentBackend{
			Client:        client,
			Model:         *model,
			ModelOverride: *modelOverride,
			WorkDir:       *workDir,
			Registry:      toolRegistry,
		}

		var checkRunner pipeline.CheckRunner
		if !*noDocker {
			checkRunner = makeCheckRunner(containerName)
			fmt.Println("    Build gates: enabled")
		} else {
			fmt.Println("    Build gates: disabled (--no-docker)")
		}

		handlerRegistry = pipeline.DefaultHandlerRegistry(pipeline.CodergenHandler{
			Backend:     backend,
			CheckRunner: checkRunner,
			WorkDir:     *workDir,
		})
	}

	// 5. Run pipeline
	fmt.Println("[5] Running pipeline...")
	fmt.Println()
	startTime := time.Now()

	result, err := pipeline.Run(ctx, pipeline.RunConfig{
		Graph:           g,
		LogsRoot:        logsRoot,
		Registry:        handlerRegistry,
		MaxBudgetTokens: *budgetTokens,
		Recorder:        recorder,
		RunID:           runID,
	})
	elapsed := time.Since(startTime)

	// Record run completion regardless of error.
	if runID != uuid.Nil {
		finishStatus := string(result.Status)
		if err != nil {
			finishStatus = "error"
		}
		finishCtx, finishCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer finishCancel()
		finishErr := recorder.FinishRun(finishCtx, runID, store.RunFinish{
			FinishedAt:        time.Now(),
			DurationMs:        int(elapsed.Milliseconds()),
			Status:            finishStatus,
			FailureReason:     result.FailureReason,
			TotalInputTokens:  result.TotalUsage.InputTokens,
			TotalOutputTokens: result.TotalUsage.OutputTokens,
			TotalTokens:       result.TotalUsage.TotalTokens,
			StagesCompleted:   len(result.CompletedNodes),
		})
		if finishErr != nil {
			fmt.Printf("    [!!] Failed to record run finish: %v\n", finishErr)
		}
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Pipeline execution error: %v\n", err)
		return 1
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
		fmt.Print(formatUsageTable(result.CompletedNodes, result.StageUsages))
	}

	// 8. Write summary JSON
	summaryPath := filepath.Join(logsRoot, "summary.json")
	summaryData := buildSummaryJSON(result, elapsed, summaryConfig{
		EffectiveModel: effectiveModel,
		ModelOverride:  *modelOverride != "",
		ZDR:            *zdr,
		PromptCache:    *promptCache,
		BudgetTokens:   *budgetTokens,
	})
	if data, err := json.MarshalIndent(summaryData, "", "  "); err == nil {
		_ = os.WriteFile(summaryPath, data, 0o644)
		fmt.Printf("\nSummary written to %s\n", summaryPath)
	}

	if result.Status == pipeline.StatusFail {
		fmt.Println("\nPipeline FAILED.")
		return 1
	}

	fmt.Println("\nPipeline completed successfully!")
	return 0
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

type checkStatus string

const (
	checkPass checkStatus = "pass"
	checkFail checkStatus = "fail"
	checkSkip checkStatus = "skip"
	checkWarn checkStatus = "warn"
)

type preflightCheck struct {
	Name   string
	Status checkStatus
	Detail string
}

type preflightConfig struct {
	Graph         *dot.Graph
	WorkDir       string
	Model         string
	ModelOverride string
	BudgetTokens  int
	Simulate      bool
	NoDocker      bool
	APIKey        string
}

// evaluatePreflight runs all preflight validations and returns structured
// results with no printing. The caller is responsible for presentation.
func evaluatePreflight(cfg preflightConfig) []preflightCheck {
	var checks []preflightCheck

	// 1. Work directory exists and is writable.
	info, err := os.Stat(cfg.WorkDir)
	if err != nil {
		checks = append(checks, preflightCheck{"work_dir", checkFail, fmt.Sprintf("work directory %q does not exist", cfg.WorkDir)})
	} else if !info.IsDir() {
		checks = append(checks, preflightCheck{"work_dir", checkFail, fmt.Sprintf("%q is not a directory", cfg.WorkDir)})
	} else {
		tmp := filepath.Join(cfg.WorkDir, ".attractor-preflight-check")
		if err := os.WriteFile(tmp, []byte("ok"), 0o644); err != nil {
			checks = append(checks, preflightCheck{"work_dir", checkFail, fmt.Sprintf("work directory %q is not writable: %v", cfg.WorkDir, err)})
		} else {
			_ = os.Remove(tmp)
			checks = append(checks, preflightCheck{"work_dir", checkPass, "exists and is writable"})
		}
	}

	// 2. API key is present and well-formed.
	if cfg.Simulate {
		checks = append(checks, preflightCheck{"api_key", checkSkip, "simulate mode"})
	} else if cfg.APIKey == "" {
		checks = append(checks, preflightCheck{"api_key", checkFail, "OPENROUTER_API_KEY environment variable is not set"})
	} else if !strings.HasPrefix(cfg.APIKey, "sk-or-") {
		checks = append(checks, preflightCheck{"api_key", checkFail,
			fmt.Sprintf("OPENROUTER_API_KEY does not start with \"sk-or-\" -- check for stray quotes or wrong key (got %q...)", truncate(cfg.APIKey, 10))})
	} else {
		checks = append(checks, preflightCheck{"api_key", checkPass, "present and well-formed"})
	}

	// 3. Docker daemon is reachable.
	if cfg.Simulate || cfg.NoDocker {
		reason := "simulate mode"
		if cfg.NoDocker {
			reason = "--no-docker"
		}
		checks = append(checks, preflightCheck{"docker", checkSkip, reason})
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := exec.CommandContext(ctx, "docker", "info").Run(); err != nil {
			checks = append(checks, preflightCheck{"docker", checkFail, "Docker daemon is not reachable -- is Docker Desktop running?"})
		} else {
			checks = append(checks, preflightCheck{"docker", checkPass, "Docker daemon is reachable"})
		}
	}

	// 4. Model IDs exist on OpenRouter.
	if cfg.Simulate {
		checks = append(checks, preflightCheck{"models", checkSkip, "simulate mode"})
	} else {
		var allModels []string
		if cfg.ModelOverride != "" {
			allModels = []string{cfg.ModelOverride}
		} else {
			allModels = collectModelIDs(cfg.Graph, cfg.Model)
		}
		knownModels, apiErr := fetchOpenRouterModels(http.DefaultClient, "https://openrouter.ai/api/v1", cfg.APIKey)
		if apiErr != nil {
			var badFormat []string
			for _, m := range allModels {
				if !strings.Contains(m, "/") {
					badFormat = append(badFormat, m)
				}
			}
			if len(badFormat) > 0 {
				checks = append(checks, preflightCheck{"models", checkFail,
					fmt.Sprintf("model IDs missing provider/ prefix: %s", strings.Join(badFormat, ", "))})
			} else {
				checks = append(checks, preflightCheck{"models", checkWarn,
					fmt.Sprintf("Could not reach OpenRouter to validate models (%v); format check passed", apiErr)})
			}
		} else {
			var unknown []string
			for _, m := range allModels {
				if !knownModels[m] {
					unknown = append(unknown, m)
				}
			}
			if len(unknown) > 0 {
				checks = append(checks, preflightCheck{"models", checkFail,
					fmt.Sprintf("model IDs not found on OpenRouter: %s", strings.Join(unknown, ", "))})
			} else {
				checks = append(checks, preflightCheck{"models", checkPass,
					fmt.Sprintf("All %d model IDs verified on OpenRouter", len(allModels))})
			}
		}
	}

	// 5. Budget sanity check.
	codergenCount := 0
	for _, n := range cfg.Graph.Nodes {
		if n.Shape() == "box" || n.Type() == "codergen" {
			codergenCount++
		}
	}
	if cfg.BudgetTokens > 0 && cfg.BudgetTokens < 100_000 && codergenCount > 1 {
		checks = append(checks, preflightCheck{"budget", checkWarn,
			fmt.Sprintf("Budget is very low (%d tokens) for a %d-stage pipeline; stages may be cut short", cfg.BudgetTokens, codergenCount)})
	} else if cfg.BudgetTokens > 20_000_000 {
		checks = append(checks, preflightCheck{"budget", checkWarn,
			fmt.Sprintf("Budget is very high (%d tokens); consider lowering for safety", cfg.BudgetTokens)})
	} else {
		checks = append(checks, preflightCheck{"budget", checkPass, "Budget is reasonable"})
	}

	return checks
}

// printPreflightChecks renders structured check results to stdout using the
// same [ok] / [FAIL] / [--] / [!!] format as before.
func printPreflightChecks(checks []preflightCheck) {
	for _, c := range checks {
		switch c.Status {
		case checkPass:
			fmt.Printf("    [ok] %s\n", c.Detail)
		case checkFail:
			fmt.Printf("    [FAIL] %s\n", c.Detail)
		case checkSkip:
			fmt.Printf("    [--] %s (skipped: %s)\n", c.Name, c.Detail)
		case checkWarn:
			fmt.Printf("    [!!] %s\n", c.Detail)
		}
	}
}

// preflightChecksToResult converts structured checks into the legacy
// preflightResult for backward compatibility with the run() call site.
func preflightChecksToResult(checks []preflightCheck) preflightResult {
	var warnings []string
	var failures []string
	for _, c := range checks {
		switch c.Status {
		case checkFail:
			failures = append(failures, c.Detail)
		case checkWarn:
			warnings = append(warnings, c.Detail)
		}
	}
	if len(failures) > 0 {
		return preflightResult{
			warnings: warnings,
			err:      fmt.Errorf("pre-flight failed:\n    - %s", strings.Join(failures, "\n    - ")),
		}
	}
	return preflightResult{warnings: warnings}
}

// countTaskNodes returns the number of codergen/task nodes (shape=box or
// type=codergen), excluding structural nodes like start, exit, and conditionals.
func countTaskNodes(g *dot.Graph) int {
	count := 0
	for _, n := range g.Nodes {
		switch n.Shape() {
		case "Mdiamond", "Msquare", "diamond":
			continue
		default:
			count++
		}
	}
	return count
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

// sandboxName returns a Docker container name derived from the run UUID.
// Format: "attractor-<first 8 hex chars>". If runID is uuid.Nil (e.g.
// StartRun failed), a fresh UUID is generated so the name is always unique.
func sandboxName(runID uuid.UUID) string {
	id := runID
	if id == uuid.Nil {
		id = uuid.New()
	}
	hexID := strings.ReplaceAll(id.String(), "-", "")
	return "attractor-" + hexID[:8]
}

// summaryConfig holds the configuration values needed to build the summary JSON.
// Extracted from run() so the summary can be constructed and tested independently.
type summaryConfig struct {
	EffectiveModel string
	ModelOverride  bool
	ZDR            bool
	PromptCache    bool
	BudgetTokens   int
}

// buildSummaryJSON constructs the summary data map written to summary.json.
// Pure function: takes result + config, returns a map.
func buildSummaryJSON(result pipeline.RunResult, elapsed time.Duration, cfg summaryConfig) map[string]any {
	return map[string]any{
		"status":          string(result.Status),
		"completed_nodes": result.CompletedNodes,
		"failure_reason":  result.FailureReason,
		"warnings":        result.Warnings,
		"total_usage":     result.TotalUsage,
		"stage_usages":    result.StageUsages,
		"elapsed_seconds": elapsed.Seconds(),
		"model":           cfg.EffectiveModel,
		"model_override":  cfg.ModelOverride,
		"zdr":             cfg.ZDR,
		"prompt_cache":    cfg.PromptCache,
		"budget_tokens":   cfg.BudgetTokens,
	}
}

// formatUsageTable returns a formatted table of per-stage token usage.
// Rows follow the order of completedNodes, not map iteration order.
func formatUsageTable(completedNodes []string, stageUsages map[string]*pipeline.StageUsage) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-20s %8s %8s %8s %6s  %s\n", "Stage", "Input", "Output", "Total", "Rounds", "Model")
	b.WriteString("-------------------- -------- -------- -------- ------  -----\n")
	for _, nodeID := range completedNodes {
		if su, ok := stageUsages[nodeID]; ok {
			fmt.Fprintf(&b, "%-20s %8d %8d %8d %6d  %s\n",
				truncate(nodeID, 20), su.InputTokens, su.OutputTokens, su.TotalTokens, su.Rounds, su.Model)
		}
	}
	return b.String()
}

func makeCheckRunner(containerName string) pipeline.CheckRunner {
	return func(ctx context.Context, cmd string) (string, error) {
		checkCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
		defer cancel()
		dockerCmd := exec.CommandContext(checkCtx, "docker", "exec", containerName, "sh", "-c", cmd)
		out, err := dockerCmd.CombinedOutput()
		return string(out), err
	}
}

// fetchOpenRouterModels queries the OpenRouter API for available model IDs.
// Dependencies (HTTP client, base URL, API key) are passed as parameters so the
// function is testable without real network calls or environment variables.
func fetchOpenRouterModels(httpClient *http.Client, baseURL, apiKey string) (map[string]bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := httpClient.Do(req)
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
