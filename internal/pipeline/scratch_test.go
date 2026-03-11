package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestSetupScratch_CreatesDirectoryAndContext(t *testing.T) {
	workDir := t.TempDir()

	err := SetupScratch(workDir, "analyze", nil, "Analyze the codebase")
	if err != nil {
		t.Fatalf("SetupScratch: %v", err)
	}

	scratchPath := filepath.Join(workDir, "_scratch")
	if _, err := os.Stat(scratchPath); os.IsNotExist(err) {
		t.Fatal("_scratch/ directory was not created")
	}
	if _, err := os.Stat(filepath.Join(scratchPath, "prior")); os.IsNotExist(err) {
		t.Fatal("_scratch/prior/ directory was not created")
	}

	data, err := os.ReadFile(filepath.Join(scratchPath, "stage_context.md"))
	if err != nil {
		t.Fatalf("reading stage_context.md: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "# Stage: analyze") {
		t.Error("stage_context.md missing stage header")
	}
	if !strings.Contains(content, "Analyze the codebase") {
		t.Error("stage_context.md missing task description")
	}
}

func TestSetupScratch_IncludesPriorStages(t *testing.T) {
	workDir := t.TempDir()

	err := SetupScratch(workDir, "backend", []string{"analyze", "design", "scaffold"}, "Implement backend")
	if err != nil {
		t.Fatalf("SetupScratch: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(workDir, "_scratch", "stage_context.md"))
	if err != nil {
		t.Fatalf("reading stage_context.md: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "analyze, design, scaffold") {
		t.Errorf("stage_context.md missing prior stages, got: %s", content)
	}
}

func TestSetupScratch_PreservesExistingPrior(t *testing.T) {
	workDir := t.TempDir()
	priorDir := filepath.Join(workDir, "_scratch", "prior")
	if err := os.MkdirAll(priorDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(priorDir, "analyze_summary.md"), []byte("prior notes"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := SetupScratch(workDir, "design", []string{"analyze"}, "Design contracts")
	if err != nil {
		t.Fatalf("SetupScratch: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(priorDir, "analyze_summary.md"))
	if err != nil {
		t.Fatal("prior summary was deleted during setup")
	}
	if string(data) != "prior notes" {
		t.Error("prior summary contents were modified")
	}
}

func TestArchiveAndCleanScratch_FullLifecycle(t *testing.T) {
	workDir := t.TempDir()
	logsRoot := t.TempDir()

	scratchPath := filepath.Join(workDir, "_scratch")
	if err := os.MkdirAll(filepath.Join(scratchPath, "prior"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scratchPath, "notes.md"), []byte("working notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scratchPath, "SUMMARY.md"), []byte("Key findings: X, Y, Z"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scratchPath, "stage_context.md"), []byte("context"), 0o644); err != nil {
		t.Fatal(err)
	}

	summaryText, err := ArchiveAndCleanScratch(workDir, logsRoot, "analyze")
	if err != nil {
		t.Fatalf("ArchiveAndCleanScratch: %v", err)
	}

	if diff := cmp.Diff("Key findings: X, Y, Z", summaryText); diff != "" {
		t.Errorf("summary text mismatch (-want +got):\n%s", diff)
	}

	archivePath := filepath.Join(logsRoot, "analyze", "scratch")
	for _, name := range []string{"notes.md", "SUMMARY.md", "stage_context.md"} {
		if _, err := os.Stat(filepath.Join(archivePath, name)); os.IsNotExist(err) {
			t.Errorf("archived file missing: %s", name)
		}
	}

	entries, _ := os.ReadDir(scratchPath)
	var remaining []string
	for _, e := range entries {
		remaining = append(remaining, e.Name())
	}
	if diff := cmp.Diff([]string{"prior"}, remaining); diff != "" {
		t.Errorf("scratch dir should only contain prior/ after cleanup (-want +got):\n%s", diff)
	}

	priorSummary, err := os.ReadFile(filepath.Join(scratchPath, "prior", "analyze_summary.md"))
	if err != nil {
		t.Fatal("summary was not moved to prior/")
	}
	if diff := cmp.Diff("Key findings: X, Y, Z", string(priorSummary)); diff != "" {
		t.Errorf("prior summary mismatch (-want +got):\n%s", diff)
	}
}

func TestArchiveAndCleanScratch_MissingSummary(t *testing.T) {
	workDir := t.TempDir()
	logsRoot := t.TempDir()

	scratchPath := filepath.Join(workDir, "_scratch")
	if err := os.MkdirAll(filepath.Join(scratchPath, "prior"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scratchPath, "notes.md"), []byte("just notes"), 0o644); err != nil {
		t.Fatal(err)
	}

	summaryText, err := ArchiveAndCleanScratch(workDir, logsRoot, "design")
	if err != nil {
		t.Fatalf("ArchiveAndCleanScratch: %v", err)
	}

	if summaryText != "" {
		t.Errorf("expected empty summary, got %q", summaryText)
	}

	if _, err := os.Stat(filepath.Join(scratchPath, "prior", "design_summary.md")); !os.IsNotExist(err) {
		t.Error("should not create prior summary when SUMMARY.md is missing")
	}
}

func TestArchiveAndCleanScratch_NoScratchDir(t *testing.T) {
	workDir := t.TempDir()
	logsRoot := t.TempDir()

	summaryText, err := ArchiveAndCleanScratch(workDir, logsRoot, "scaffold")
	if err != nil {
		t.Fatalf("ArchiveAndCleanScratch: %v", err)
	}
	if summaryText != "" {
		t.Errorf("expected empty summary when no scratch dir, got %q", summaryText)
	}
}

func TestScratch_MultiStageAccumulation(t *testing.T) {
	workDir := t.TempDir()
	logsRoot := t.TempDir()

	stages := []struct {
		nodeID  string
		summary string
	}{
		{"analyze", "Found 5 entities"},
		{"design", "Designed 3 interfaces"},
		{"scaffold", "Created project structure"},
	}

	var completedStages []string
	for _, stage := range stages {
		err := SetupScratch(workDir, stage.nodeID, completedStages, "task for "+stage.nodeID)
		if err != nil {
			t.Fatalf("SetupScratch(%s): %v", stage.nodeID, err)
		}

		scratchPath := filepath.Join(workDir, "_scratch")
		if err := os.WriteFile(filepath.Join(scratchPath, "SUMMARY.md"), []byte(stage.summary), 0o644); err != nil {
			t.Fatal(err)
		}

		stageLogsRoot := filepath.Join(logsRoot, "run1")
		summaryText, err := ArchiveAndCleanScratch(workDir, stageLogsRoot, stage.nodeID)
		if err != nil {
			t.Fatalf("ArchiveAndCleanScratch(%s): %v", stage.nodeID, err)
		}
		if summaryText != stage.summary {
			t.Errorf("stage %s: summary = %q, want %q", stage.nodeID, summaryText, stage.summary)
		}

		completedStages = append(completedStages, stage.nodeID)
	}

	priorDir := filepath.Join(workDir, "_scratch", "prior")
	entries, err := os.ReadDir(priorDir)
	if err != nil {
		t.Fatalf("reading prior dir: %v", err)
	}

	var priorFiles []string
	for _, e := range entries {
		priorFiles = append(priorFiles, e.Name())
	}
	want := []string{"analyze_summary.md", "design_summary.md", "scaffold_summary.md"}
	if diff := cmp.Diff(want, priorFiles); diff != "" {
		t.Errorf("prior files mismatch (-want +got):\n%s", diff)
	}

	for _, stage := range stages {
		data, err := os.ReadFile(filepath.Join(priorDir, stage.nodeID+"_summary.md"))
		if err != nil {
			t.Errorf("reading prior summary for %s: %v", stage.nodeID, err)
			continue
		}
		if string(data) != stage.summary {
			t.Errorf("prior summary for %s = %q, want %q", stage.nodeID, string(data), stage.summary)
		}
	}
}
