package pipeline

import (
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// snapshotExcludeDirs lists directory names excluded from filesystem snapshots.
// These are either engine-managed, version control, or dependency directories
// that would add noise without useful signal for downstream stages.
var snapshotExcludeDirs = map[string]bool{
	"_scratch":        true,
	".attractor-logs": true,
	".git":            true,
	"node_modules":    true,
	"vendor":          true,
}

// FileEntry records a file's relative path and size in bytes.
type FileEntry struct {
	Path string
	Size int64
}

// FileDiff represents the difference between two directory snapshots.
type FileDiff struct {
	Added     []FileEntry
	Removed   []string
	Modified  []FileEntry // size in the "after" snapshot
	Unchanged int
}

// IsEmpty returns true when no files were added, removed, or modified.
func (d FileDiff) IsEmpty() bool {
	return len(d.Added) == 0 && len(d.Removed) == 0 && len(d.Modified) == 0
}

// String returns a human-readable summary of the diff suitable for logging
// and injection into downstream stage prompts.
func (d FileDiff) String() string {
	if d.IsEmpty() {
		return "(no filesystem changes)"
	}

	var b strings.Builder
	if len(d.Added) > 0 {
		fmt.Fprintf(&b, "Added (%d):\n", len(d.Added))
		for _, f := range d.Added {
			fmt.Fprintf(&b, "  + %s (%d bytes)\n", f.Path, f.Size)
		}
	}
	if len(d.Removed) > 0 {
		fmt.Fprintf(&b, "Removed (%d):\n", len(d.Removed))
		for _, p := range d.Removed {
			fmt.Fprintf(&b, "  - %s\n", p)
		}
	}
	if len(d.Modified) > 0 {
		fmt.Fprintf(&b, "Modified (%d):\n", len(d.Modified))
		for _, f := range d.Modified {
			fmt.Fprintf(&b, "  ~ %s (%d bytes)\n", f.Path, f.Size)
		}
	}
	if d.Unchanged > 0 {
		fmt.Fprintf(&b, "Unchanged: %d files\n", d.Unchanged)
	}
	return strings.TrimRight(b.String(), "\n")
}

// SnapshotDir walks rootDir and returns a map of relative file paths to their
// sizes. Directories in snapshotExcludeDirs are skipped entirely. Symlinks are
// skipped to prevent agents from creating symlinks that would misrepresent the
// directory contents.
func SnapshotDir(rootDir string) (map[string]int64, error) {
	snapshot := make(map[string]int64)

	err := filepath.WalkDir(rootDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			slog.Debug("pipeline.snapshot.walk_error", "path", path, "error", err)
			return nil
		}

		if d.IsDir() && path != rootDir {
			if snapshotExcludeDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}

		info, infoErr := d.Info()
		if infoErr != nil {
			slog.Debug("pipeline.snapshot.info_error", "path", path, "error", infoErr)
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		if info.Mode().IsRegular() {
			rel, relErr := filepath.Rel(rootDir, path)
			if relErr != nil {
				return nil
			}
			snapshot[rel] = info.Size()
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("snapshot: walk %s: %w", rootDir, err)
	}

	return snapshot, nil
}

// DiffSnapshots compares two directory snapshots and returns a structured diff.
// Files present in after but not before are added; files present in before but
// not after are removed; files present in both with different sizes are
// modified; files present in both with the same size are unchanged.
func DiffSnapshots(before, after map[string]int64) FileDiff {
	var diff FileDiff

	for path, afterSize := range after {
		beforeSize, existed := before[path]
		if !existed {
			diff.Added = append(diff.Added, FileEntry{Path: path, Size: afterSize})
		} else if beforeSize != afterSize {
			diff.Modified = append(diff.Modified, FileEntry{Path: path, Size: afterSize})
		} else {
			diff.Unchanged++
		}
	}

	for path := range before {
		if _, exists := after[path]; !exists {
			diff.Removed = append(diff.Removed, path)
		}
	}

	sort.Slice(diff.Added, func(i, j int) bool { return diff.Added[i].Path < diff.Added[j].Path })
	sort.Slice(diff.Modified, func(i, j int) bool { return diff.Modified[i].Path < diff.Modified[j].Path })
	sort.Strings(diff.Removed)

	return diff
}
