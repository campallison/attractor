package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestSnapshotDir_BasicFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "package main")
	writeFile(t, dir, "internal/server.go", "package internal")
	writeFile(t, dir, "go.mod", "module example")

	snap, err := SnapshotDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantPaths := []string{"main.go", "internal/server.go", "go.mod"}
	for _, p := range wantPaths {
		if _, ok := snap[p]; !ok {
			t.Errorf("expected %q in snapshot, not found", p)
		}
	}
	if diff := cmp.Diff(int64(len("package main")), snap["main.go"]); diff != "" {
		t.Errorf("main.go size mismatch (-want +got):\n%s", diff)
	}
}

func TestSnapshotDir_ExcludesIgnoredDirs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "package main")
	writeFile(t, dir, "_scratch/notes.md", "notes")
	writeFile(t, dir, ".git/config", "git config")
	writeFile(t, dir, ".attractor-logs/run1/log.json", "{}")
	writeFile(t, dir, "node_modules/pkg/index.js", "module.exports = {}")
	writeFile(t, dir, "vendor/lib/lib.go", "package lib")

	snap, err := SnapshotDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := snap["main.go"]; !ok {
		t.Error("expected main.go in snapshot")
	}

	excluded := []string{
		"_scratch/notes.md",
		".git/config",
		".attractor-logs/run1/log.json",
		"node_modules/pkg/index.js",
		"vendor/lib/lib.go",
	}
	for _, p := range excluded {
		if _, ok := snap[p]; ok {
			t.Errorf("expected %q to be excluded from snapshot", p)
		}
	}
}

func TestSnapshotDir_SkipsSymlinks(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "real.go", "package main")

	target := filepath.Join(dir, "real.go")
	link := filepath.Join(dir, "link.go")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}

	snap, err := SnapshotDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := snap["real.go"]; !ok {
		t.Error("expected real.go in snapshot")
	}
	if _, ok := snap["link.go"]; ok {
		t.Error("expected link.go (symlink) to be excluded")
	}
}

func TestSnapshotDir_SkipsDirectorySymlinks(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "src/main.go", "package main")

	target := filepath.Join(dir, "src")
	link := filepath.Join(dir, "fake")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}

	snap, err := SnapshotDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := snap["src/main.go"]; !ok {
		t.Error("expected src/main.go in snapshot")
	}
	if _, ok := snap["fake/main.go"]; ok {
		t.Error("expected directory symlink contents to be excluded")
	}
}

func TestSnapshotDir_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	snap, err := SnapshotDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(snap) != 0 {
		t.Errorf("expected empty snapshot, got %d entries", len(snap))
	}
}

func TestDiffSnapshots_AddedRemovedModified(t *testing.T) {
	before := map[string]int64{
		"existing.go": 100,
		"removed.go":  200,
		"changed.go":  300,
	}
	after := map[string]int64{
		"existing.go": 100,
		"changed.go":  350,
		"new.go":      150,
	}

	diff := DiffSnapshots(before, after)

	if d := cmp.Diff(1, len(diff.Added)); d != "" {
		t.Errorf("added count (-want +got):\n%s", d)
	}
	if diff.Added[0].Path != "new.go" {
		t.Errorf("expected added file 'new.go', got %q", diff.Added[0].Path)
	}
	if diff.Added[0].Size != 150 {
		t.Errorf("expected added size 150, got %d", diff.Added[0].Size)
	}

	if d := cmp.Diff(1, len(diff.Removed)); d != "" {
		t.Errorf("removed count (-want +got):\n%s", d)
	}
	if diff.Removed[0] != "removed.go" {
		t.Errorf("expected removed file 'removed.go', got %q", diff.Removed[0])
	}

	if d := cmp.Diff(1, len(diff.Modified)); d != "" {
		t.Errorf("modified count (-want +got):\n%s", d)
	}
	if diff.Modified[0].Path != "changed.go" {
		t.Errorf("expected modified file 'changed.go', got %q", diff.Modified[0].Path)
	}

	if d := cmp.Diff(1, diff.Unchanged); d != "" {
		t.Errorf("unchanged count (-want +got):\n%s", d)
	}

	if diff.IsEmpty() {
		t.Error("diff should not be empty")
	}
}

func TestDiffSnapshots_IdenticalSnapshots(t *testing.T) {
	snap := map[string]int64{
		"a.go": 100,
		"b.go": 200,
	}
	diff := DiffSnapshots(snap, snap)

	if !diff.IsEmpty() {
		t.Error("identical snapshots should produce empty diff")
	}
	if diff.Unchanged != 2 {
		t.Errorf("expected 2 unchanged, got %d", diff.Unchanged)
	}
}

func TestDiffSnapshots_EmptyToPopulated(t *testing.T) {
	before := map[string]int64{}
	after := map[string]int64{
		"new1.go": 100,
		"new2.go": 200,
	}

	diff := DiffSnapshots(before, after)
	if len(diff.Added) != 2 {
		t.Errorf("expected 2 added, got %d", len(diff.Added))
	}
	if len(diff.Removed) != 0 {
		t.Errorf("expected 0 removed, got %d", len(diff.Removed))
	}
}

func TestDiffSnapshots_SortedOutput(t *testing.T) {
	before := map[string]int64{}
	after := map[string]int64{
		"z.go":          10,
		"a.go":          20,
		"internal/m.go": 30,
	}

	diff := DiffSnapshots(before, after)
	if len(diff.Added) != 3 {
		t.Fatalf("expected 3 added, got %d", len(diff.Added))
	}
	if diff.Added[0].Path != "a.go" {
		t.Errorf("expected sorted order, first item: %q", diff.Added[0].Path)
	}
	if diff.Added[1].Path != "internal/m.go" {
		t.Errorf("expected sorted order, second item: %q", diff.Added[1].Path)
	}
	if diff.Added[2].Path != "z.go" {
		t.Errorf("expected sorted order, third item: %q", diff.Added[2].Path)
	}
}

func TestFileDiff_String_Empty(t *testing.T) {
	diff := FileDiff{}
	got := diff.String()
	if got != "(no filesystem changes)" {
		t.Errorf("expected empty diff string, got %q", got)
	}
}

func TestFileDiff_String_WithChanges(t *testing.T) {
	diff := FileDiff{
		Added:     []FileEntry{{Path: "new.go", Size: 150}},
		Removed:   []string{"old.go"},
		Modified:  []FileEntry{{Path: "main.go", Size: 300}},
		Unchanged: 5,
	}
	got := diff.String()

	if !strings.Contains(got, "Added (1):") {
		t.Error("expected 'Added' section")
	}
	if !strings.Contains(got, "+ new.go (150 bytes)") {
		t.Error("expected added file entry")
	}
	if !strings.Contains(got, "Removed (1):") {
		t.Error("expected 'Removed' section")
	}
	if !strings.Contains(got, "- old.go") {
		t.Error("expected removed file entry")
	}
	if !strings.Contains(got, "Modified (1):") {
		t.Error("expected 'Modified' section")
	}
	if !strings.Contains(got, "~ main.go (300 bytes)") {
		t.Error("expected modified file entry")
	}
	if !strings.Contains(got, "Unchanged: 5 files") {
		t.Error("expected unchanged count")
	}
}

// writeFile is a test helper that creates a file with the given content,
// creating parent directories as needed.
func writeFile(t *testing.T, base, relPath, content string) {
	t.Helper()
	full := filepath.Join(base, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("creating dir for %s: %v", relPath, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", relPath, err)
	}
}
