package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
