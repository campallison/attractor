package pipeline

import (
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

const (
	scratchDir     = "_scratch"
	scratchSummary = "SUMMARY.md"
	scratchPrior   = "prior"
	scratchContext  = "stage_context.md"
)

// SetupScratch creates the _scratch/ directory in workDir and seeds it with a
// stage_context.md file describing the current stage. Any existing
// _scratch/prior/ directory is preserved so downstream stages can read
// summaries from earlier stages.
func SetupScratch(workDir, nodeID string, completedStages []string, stageDescription string) error {
	dir := filepath.Join(workDir, scratchDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("scratch: create dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, scratchPrior), 0o755); err != nil {
		return fmt.Errorf("scratch: create prior dir: %w", err)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# Stage: %s\n\n", nodeID)
	if len(completedStages) > 0 {
		fmt.Fprintf(&b, "Prior stages completed: %s\n\n", strings.Join(completedStages, ", "))
	}
	if stageDescription != "" {
		fmt.Fprintf(&b, "Task: %s\n", stageDescription)
	}

	contextPath := filepath.Join(dir, scratchContext)
	if err := os.WriteFile(contextPath, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("scratch: write context: %w", err)
	}

	slog.Info("pipeline.scratch.setup", "node", nodeID, "dir", dir)
	return nil
}

// ArchiveAndCleanScratch copies all _scratch/ contents to the stage's log
// directory, reads SUMMARY.md if present, then cleans _scratch/ — removing
// everything except prior/. Finally, it moves SUMMARY.md into
// _scratch/prior/<nodeID>_summary.md for downstream stages.
//
// Returns the summary text (empty if no SUMMARY.md was found) and any error.
func ArchiveAndCleanScratch(workDir, logsRoot, nodeID string) (string, error) {
	scratchPath := filepath.Join(workDir, scratchDir)
	if _, err := os.Stat(scratchPath); os.IsNotExist(err) {
		slog.Debug("pipeline.scratch.archive.skip", "node", nodeID, "reason", "no scratch dir")
		return "", nil
	}

	archivePath := filepath.Join(logsRoot, sanitizeNodeID(nodeID), "scratch")
	if err := copyDir(scratchPath, archivePath); err != nil {
		slog.Warn("pipeline.scratch.archive.error", "node", nodeID, "error", err)
	} else {
		slog.Info("pipeline.scratch.archived", "node", nodeID, "archive", archivePath)
	}

	summaryPath := filepath.Join(scratchPath, scratchSummary)
	var summaryText string
	if data, err := os.ReadFile(summaryPath); err == nil {
		summaryText = string(data)
		slog.Info("pipeline.scratch.summary_found", "node", nodeID, "len", len(summaryText))
	} else {
		slog.Warn("pipeline.scratch.summary_missing", "node", nodeID)
	}

	priorPath := filepath.Join(scratchPath, scratchPrior)
	entries, _ := os.ReadDir(scratchPath)
	for _, entry := range entries {
		name := entry.Name()
		if name == scratchPrior {
			continue
		}
		_ = os.RemoveAll(filepath.Join(scratchPath, name))
	}

	if summaryText != "" {
		destName := nodeID + "_summary.md"
		destPath := filepath.Join(priorPath, destName)
		if err := os.WriteFile(destPath, []byte(summaryText), 0o644); err != nil {
			slog.Warn("pipeline.scratch.prior.write_error", "node", nodeID, "error", err)
		} else {
			slog.Info("pipeline.scratch.prior.saved", "node", nodeID, "path", destPath)
		}
	}

	return summaryText, nil
}

// copyDir recursively copies src to dst. It creates dst if it doesn't exist.
func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}
