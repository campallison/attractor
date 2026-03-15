package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// ProvisionCheckTool cross-compiles the check-consistency binary for Linux
// and copies it into the named Docker container at /usr/local/bin/check-consistency.
//
// sourceDir should be the root of the attractor repository (containing cmd/check-consistency/).
// The binary is compiled for the host's architecture (runtime.GOARCH) on Linux, matching
// Docker Desktop's default container architecture.
//
// Returns nil on success. If compilation or copy fails, returns an error but
// does not prevent the pipeline from running — consistency checks are optional.
func ProvisionCheckTool(ctx context.Context, sourceDir, containerName string) error {
	cmdDir := filepath.Join(sourceDir, "cmd", "check-consistency")
	if _, err := os.Stat(cmdDir); err != nil {
		return fmt.Errorf("check-consistency source not found at %s: %w", cmdDir, err)
	}

	tmpBin, err := os.CreateTemp("", "check-consistency-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpBin.Name()
	tmpBin.Close()
	defer os.Remove(tmpPath)

	buildCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	build := exec.CommandContext(buildCtx, "go", "build", "-o", tmpPath, "./cmd/check-consistency/")
	build.Dir = sourceDir
	build.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+runtime.GOARCH, "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to build check-consistency (GOOS=linux GOARCH=%s): %w\n%s", runtime.GOARCH, err, out)
	}

	cpCtx, cpCancel := context.WithTimeout(ctx, 30*time.Second)
	defer cpCancel()
	cp := exec.CommandContext(cpCtx, "docker", "cp", tmpPath, containerName+":/usr/local/bin/check-consistency")
	if out, err := cp.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to copy check-consistency into container: %w\n%s", err, out)
	}

	// docker cp may not preserve execute permissions on all Docker versions.
	chmodCtx, chmodCancel := context.WithTimeout(ctx, 10*time.Second)
	defer chmodCancel()
	chmod := exec.CommandContext(chmodCtx, "docker", "exec", containerName, "chmod", "+x", "/usr/local/bin/check-consistency")
	if out, err := chmod.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to chmod check-consistency: %w\n%s", err, out)
	}

	return nil
}
