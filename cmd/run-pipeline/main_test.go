package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/campallison/attractor/internal/dot"
	"github.com/campallison/attractor/internal/pipeline"
	"github.com/google/uuid"
)

func TestSandboxName_FromUUID(t *testing.T) {
	id := uuid.MustParse("a1b2c3d4-e5f6-7890-abcd-ef1234567890")
	got := sandboxName(id)
	want := "attractor-a1b2c3d4"
	if got != want {
		t.Errorf("sandboxName(%s) = %q, want %q", id, got, want)
	}
}

func TestSandboxName_NilUUID(t *testing.T) {
	got := sandboxName(uuid.Nil)
	if !strings.HasPrefix(got, "attractor-") {
		t.Errorf("sandboxName(Nil) = %q, want prefix 'attractor-'", got)
	}
	if len(got) != len("attractor-")+8 {
		t.Errorf("sandboxName(Nil) = %q, want 18-char result (attractor- + 8 hex)", got)
	}
}

func TestSandboxName_NilUUID_Unique(t *testing.T) {
	a := sandboxName(uuid.Nil)
	b := sandboxName(uuid.Nil)
	if a == b {
		t.Errorf("two calls with Nil should produce different names, both got %q", a)
	}
}

func TestBuildSummaryJSON(t *testing.T) {
	result := pipeline.RunResult{
		Status:         pipeline.StatusSuccess,
		CompletedNodes: []string{"start", "design"},
		FailureReason:  "",
		Warnings:       []string{"budget warning"},
		TotalUsage:     pipeline.StageUsage{InputTokens: 100, OutputTokens: 50, TotalTokens: 150},
		StageUsages:    map[string]*pipeline.StageUsage{},
	}
	elapsed := 30 * time.Second
	cfg := summaryConfig{
		EffectiveModel: "anthropic/claude-opus-4.6",
		ModelOverride:  false,
		ZDR:            true,
		PromptCache:    true,
		BudgetTokens:   1_000_000,
	}

	got := buildSummaryJSON(result, elapsed, cfg)

	if got["status"] != "success" {
		t.Errorf("status = %q, want %q", got["status"], "success")
	}
	nodes, ok := got["completed_nodes"].([]string)
	if !ok || len(nodes) != 2 {
		t.Errorf("completed_nodes = %v, want [start design]", got["completed_nodes"])
	}
	if got["elapsed_seconds"] != elapsed.Seconds() {
		t.Errorf("elapsed_seconds = %v, want %v", got["elapsed_seconds"], elapsed.Seconds())
	}
	if got["model"] != "anthropic/claude-opus-4.6" {
		t.Errorf("model = %v, want anthropic/claude-opus-4.6", got["model"])
	}
	if got["zdr"] != true {
		t.Errorf("zdr = %v, want true", got["zdr"])
	}
	if got["prompt_cache"] != true {
		t.Errorf("prompt_cache = %v, want true", got["prompt_cache"])
	}
	if got["budget_tokens"] != 1_000_000 {
		t.Errorf("budget_tokens = %v, want 1000000", got["budget_tokens"])
	}
	if got["model_override"] != false {
		t.Errorf("model_override = %v, want false", got["model_override"])
	}
}

func TestFormatUsageTable(t *testing.T) {
	completedNodes := []string{"design", "scaffold"}
	stageUsages := map[string]*pipeline.StageUsage{
		"design": {
			Model:        "anthropic/claude-opus-4.6",
			Rounds:       14,
			InputTokens:  272506,
			OutputTokens: 15966,
			TotalTokens:  288472,
		},
		"scaffold": {
			Model:        "anthropic/claude-opus-4.6",
			Rounds:       48,
			InputTokens:  615614,
			OutputTokens: 8119,
			TotalTokens:  623733,
		},
	}

	got := formatUsageTable(completedNodes, stageUsages)

	if !strings.Contains(got, "design") {
		t.Error("expected table to contain 'design'")
	}
	if !strings.Contains(got, "scaffold") {
		t.Error("expected table to contain 'scaffold'")
	}
	if !strings.Contains(got, "272506") {
		t.Error("expected table to contain design input tokens '272506'")
	}
	if !strings.Contains(got, "Stage") {
		t.Error("expected table to contain header 'Stage'")
	}
	// Verify ordering follows completedNodes, not map iteration order.
	designIdx := strings.Index(got, "design")
	scaffoldIdx := strings.Index(got, "scaffold")
	if designIdx >= scaffoldIdx {
		t.Error("expected design to appear before scaffold in table")
	}
}

func TestFormatUsageTable_Empty(t *testing.T) {
	got := formatUsageTable(nil, nil)
	if !strings.Contains(got, "Stage") {
		t.Error("expected header even with no stages")
	}
	// Should just be the header lines, no data rows.
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != 2 {
		t.Errorf("expected 2 header lines, got %d", len(lines))
	}
}

// --- Preflight evaluation tests ---

func TestEvaluatePreflight_SimulateMode(t *testing.T) {
	g := &dot.Graph{Attrs: map[string]string{}}
	cfg := preflightConfig{
		Graph:    g,
		WorkDir:  t.TempDir(),
		Model:    "test/model",
		Simulate: true,
	}
	checks := evaluatePreflight(cfg)

	skipCount := 0
	for _, c := range checks {
		if c.Status == checkFail {
			t.Errorf("unexpected failure: %s: %s", c.Name, c.Detail)
		}
		if c.Status == checkSkip {
			skipCount++
		}
	}
	if skipCount < 2 {
		t.Errorf("simulate mode should skip at least API key and Docker checks, got %d skips", skipCount)
	}
}

func TestEvaluatePreflight_MissingAPIKey(t *testing.T) {
	g := &dot.Graph{Attrs: map[string]string{}}
	cfg := preflightConfig{
		Graph:    g,
		WorkDir:  t.TempDir(),
		Model:    "test/model",
		Simulate: false,
		NoDocker: true,
		APIKey:   "",
	}
	checks := evaluatePreflight(cfg)

	found := false
	for _, c := range checks {
		if c.Name == "api_key" {
			found = true
			if c.Status != checkFail {
				t.Errorf("api_key status = %q, want %q", c.Status, checkFail)
			}
		}
	}
	if !found {
		t.Error("expected api_key check in results")
	}
}

func TestEvaluatePreflight_BadAPIKeyFormat(t *testing.T) {
	g := &dot.Graph{Attrs: map[string]string{}}
	cfg := preflightConfig{
		Graph:    g,
		WorkDir:  t.TempDir(),
		Model:    "test/model",
		Simulate: false,
		NoDocker: true,
		APIKey:   "bad-prefix-key",
	}
	checks := evaluatePreflight(cfg)

	for _, c := range checks {
		if c.Name == "api_key" && c.Status != checkFail {
			t.Errorf("api_key status = %q, want %q for bad format", c.Status, checkFail)
		}
	}
}

func TestEvaluatePreflight_ValidAPIKey(t *testing.T) {
	g := &dot.Graph{Attrs: map[string]string{}}
	cfg := preflightConfig{
		Graph:    g,
		WorkDir:  t.TempDir(),
		Model:    "test/model",
		Simulate: false,
		NoDocker: true,
		APIKey:   "sk-or-valid-key",
	}
	checks := evaluatePreflight(cfg)

	for _, c := range checks {
		if c.Name == "api_key" && c.Status != checkPass {
			t.Errorf("api_key status = %q, want %q", c.Status, checkPass)
		}
	}
}

func TestEvaluatePreflight_LowBudgetWarning(t *testing.T) {
	g := &dot.Graph{
		Attrs: map[string]string{},
		Nodes: []*dot.Node{
			{ID: "a", Attrs: map[string]string{"shape": "box"}},
			{ID: "b", Attrs: map[string]string{"shape": "box"}},
		},
	}
	cfg := preflightConfig{
		Graph:        g,
		WorkDir:      t.TempDir(),
		Model:        "test/model",
		Simulate:     true,
		BudgetTokens: 50_000,
	}
	checks := evaluatePreflight(cfg)

	found := false
	for _, c := range checks {
		if c.Name == "budget" {
			found = true
			if c.Status != checkWarn {
				t.Errorf("budget status = %q, want %q for low budget", c.Status, checkWarn)
			}
		}
	}
	if !found {
		t.Error("expected budget check in results")
	}
}

func TestPreflightChecksToResult_WithFailures(t *testing.T) {
	checks := []preflightCheck{
		{Name: "work_dir", Status: checkPass, Detail: "exists and is writable"},
		{Name: "api_key", Status: checkFail, Detail: "not set"},
		{Name: "budget", Status: checkWarn, Detail: "very low"},
	}
	result := preflightChecksToResult(checks)
	if result.err == nil {
		t.Error("expected error when checks contain failures")
	}
	if len(result.warnings) != 1 || !strings.Contains(result.warnings[0], "very low") {
		t.Errorf("warnings = %v, want 1 warning about 'very low'", result.warnings)
	}
}

func TestPreflightChecksToResult_AllPass(t *testing.T) {
	checks := []preflightCheck{
		{Name: "work_dir", Status: checkPass, Detail: "ok"},
		{Name: "api_key", Status: checkPass, Detail: "ok"},
	}
	result := preflightChecksToResult(checks)
	if result.err != nil {
		t.Errorf("expected nil error, got: %v", result.err)
	}
	if len(result.warnings) != 0 {
		t.Errorf("expected no warnings, got: %v", result.warnings)
	}
}

// --- Sandbox lifecycle tests ---

func TestSetupSandbox_Success(t *testing.T) {
	var callOrder []string
	ops := sandboxOps{
		EnsureContainer: func(image, name, workDir string) error {
			callOrder = append(callOrder, "ensure")
			return nil
		},
		StopContainer: func(name string) error {
			callOrder = append(callOrder, "stop")
			return nil
		},
		ProvisionCheckTools: func(ctx context.Context, root, container string) map[string]error {
			callOrder = append(callOrder, "provision")
			return nil
		},
	}
	result, err := setupSandbox(context.Background(), sandboxConfig{
		Image:         "test-image",
		WorkDir:       t.TempDir(),
		ContainerName: "test-container",
	}, ops)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Cleanup == nil {
		t.Fatal("expected non-nil cleanup")
	}
	if len(callOrder) != 2 {
		t.Fatalf("expected ensure + provision, got: %v", callOrder)
	}

	result.Cleanup()
	if callOrder[len(callOrder)-1] != "stop" {
		t.Errorf("expected stop as last call, got: %v", callOrder)
	}
}

func TestSetupSandbox_WithCompanionDB(t *testing.T) {
	var callOrder []string
	ops := sandboxOps{
		EnsureContainer: func(image, name, workDir string) error {
			callOrder = append(callOrder, "ensure")
			return nil
		},
		StopContainer: func(name string) error {
			callOrder = append(callOrder, "stop")
			return nil
		},
		ProvisionCheckTools: func(ctx context.Context, root, container string) map[string]error {
			return nil
		},
		SetupCompanionDB: func(ctx context.Context, network, dbName, mainContainer string) error {
			callOrder = append(callOrder, "setup-db")
			return nil
		},
		TeardownCompanionDB: func(ctx context.Context, network, dbName, mainContainer string) error {
			callOrder = append(callOrder, "teardown-db")
			return nil
		},
	}
	result, err := setupSandbox(context.Background(), sandboxConfig{
		Image:         "test-image",
		WorkDir:       t.TempDir(),
		ContainerName: "test-container",
		CompanionDB:   true,
	}, ops)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result.Cleanup()

	// Companion DB must tear down BEFORE the container stops.
	teardownIdx := -1
	stopIdx := -1
	for i, c := range callOrder {
		if c == "teardown-db" {
			teardownIdx = i
		}
		if c == "stop" {
			stopIdx = i
		}
	}
	if teardownIdx < 0 || stopIdx < 0 {
		t.Fatalf("expected both teardown-db and stop, got: %v", callOrder)
	}
	if teardownIdx > stopIdx {
		t.Errorf("teardown-db must come before stop, got: %v", callOrder)
	}
}

func TestSetupSandbox_EnsureContainerError(t *testing.T) {
	ops := sandboxOps{
		EnsureContainer: func(image, name, workDir string) error {
			return fmt.Errorf("docker not running")
		},
	}
	_, err := setupSandbox(context.Background(), sandboxConfig{
		Image:         "test-image",
		WorkDir:       t.TempDir(),
		ContainerName: "test-container",
	}, ops)
	if err == nil {
		t.Fatal("expected error when container fails to start")
	}
	if !strings.Contains(err.Error(), "docker not running") {
		t.Errorf("error = %q, want mention of 'docker not running'", err.Error())
	}
}

func TestSetupSandbox_CompanionDBError_CleansUpContainer(t *testing.T) {
	var callOrder []string
	ops := sandboxOps{
		EnsureContainer: func(image, name, workDir string) error {
			callOrder = append(callOrder, "ensure")
			return nil
		},
		StopContainer: func(name string) error {
			callOrder = append(callOrder, "stop")
			return nil
		},
		ProvisionCheckTools: func(ctx context.Context, root, container string) map[string]error {
			return nil
		},
		SetupCompanionDB: func(ctx context.Context, network, dbName, mainContainer string) error {
			return fmt.Errorf("db start failed")
		},
		TeardownCompanionDB: func(ctx context.Context, network, dbName, mainContainer string) error {
			callOrder = append(callOrder, "teardown-db")
			return nil
		},
	}
	_, err := setupSandbox(context.Background(), sandboxConfig{
		Image:         "test-image",
		WorkDir:       t.TempDir(),
		ContainerName: "test-container",
		CompanionDB:   true,
	}, ops)
	if err == nil {
		t.Fatal("expected error when companion DB fails")
	}

	// On companion DB failure, both DB teardown and container stop should be called.
	hasStop := false
	hasTeardown := false
	for _, c := range callOrder {
		if c == "stop" {
			hasStop = true
		}
		if c == "teardown-db" {
			hasTeardown = true
		}
	}
	if !hasStop {
		t.Error("expected container to be stopped on companion DB failure")
	}
	if !hasTeardown {
		t.Error("expected companion DB teardown attempt on failure")
	}
}

func TestFetchOpenRouterModels_WithInjectedDeps(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer test-key")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data":[{"id":"anthropic/claude-opus-4.6"},{"id":"openai/gpt-4o"}]}`))
	}))
	defer srv.Close()

	models, err := fetchOpenRouterModels(srv.Client(), srv.URL, "test-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !models["anthropic/claude-opus-4.6"] {
		t.Error("expected anthropic/claude-opus-4.6 in result")
	}
	if !models["openai/gpt-4o"] {
		t.Error("expected openai/gpt-4o in result")
	}
	if len(models) != 2 {
		t.Errorf("expected 2 models, got %d", len(models))
	}
}

func TestFetchOpenRouterModels_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := fetchOpenRouterModels(srv.Client(), srv.URL, "test-key")
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error = %q, want mention of 500", err.Error())
	}
}

func TestFetchOpenRouterModels_NoAPIKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth := r.Header.Get("Authorization"); auth != "" {
			t.Errorf("expected no Authorization header, got %q", auth)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data":[{"id":"test/model"}]}`))
	}))
	defer srv.Close()

	models, err := fetchOpenRouterModels(srv.Client(), srv.URL, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !models["test/model"] {
		t.Error("expected test/model in result")
	}
}
