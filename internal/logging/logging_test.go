package logging

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestSetup_ReturnsCleanupThatClosesFile(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "test.log")

	cleanup, err := Setup(logPath)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}
	if cleanup == nil {
		t.Fatal("expected non-nil cleanup function")
	}

	slog.Info("hello from test")

	cleanup()

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("log file should exist: %v", err)
	}
	if len(data) == 0 {
		t.Error("log file should contain data after writing")
	}
}

func TestSetup_EmptyPath_ReturnsNoopCleanup(t *testing.T) {
	cleanup, err := Setup("")
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}
	if cleanup == nil {
		t.Fatal("expected non-nil cleanup function even for empty path")
	}
	cleanup() // should not panic
}

func TestSetup_CreatesParentDirectories(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "nested", "deep", "test.log")

	cleanup, err := Setup(logPath)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}
	defer cleanup()

	if _, err := os.Stat(logPath); err != nil {
		t.Errorf("log file should exist at nested path: %v", err)
	}
}

func TestSetup_CleanupIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "test.log")

	cleanup, err := Setup(logPath)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	cleanup()
	cleanup() // second call should not panic
}
