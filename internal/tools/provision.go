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

// ProvisionCheckTools cross-compiles check-consistency and check-behavioral
// for Linux and copies them into the named Docker container at /usr/local/bin/.
//
// sourceDir should be the root of the attractor repository.
// Each binary is built and provisioned independently; a failure in one does
// not prevent the other from being provisioned.
//
// Returns a map of tool name → error (nil on success).
func ProvisionCheckTools(ctx context.Context, sourceDir, containerName string) map[string]error {
	tools := []struct {
		name   string
		cmdPkg string
	}{
		{"check-consistency", "cmd/check-consistency"},
		{"check-behavioral", "cmd/check-behavioral"},
	}

	results := make(map[string]error, len(tools))
	for _, t := range tools {
		results[t.name] = provisionBinary(ctx, sourceDir, containerName, t.name, t.cmdPkg)
	}
	return results
}

// ProvisionCheckTool cross-compiles the check-consistency binary for Linux
// and copies it into the named Docker container at /usr/local/bin/check-consistency.
//
// Deprecated: use ProvisionCheckTools which provisions all check binaries.
// Kept for backward compatibility.
func ProvisionCheckTool(ctx context.Context, sourceDir, containerName string) error {
	return provisionBinary(ctx, sourceDir, containerName, "check-consistency", "cmd/check-consistency")
}

// provisionBinary cross-compiles a Go binary from sourceDir/cmdPkg for Linux,
// copies it into the container at /usr/local/bin/<name>, and sets the execute bit.
func provisionBinary(ctx context.Context, sourceDir, containerName, name, cmdPkg string) error {
	cmdDir := filepath.Join(sourceDir, cmdPkg)
	if _, err := os.Stat(cmdDir); err != nil {
		return fmt.Errorf("%s source not found at %s: %w", name, cmdDir, err)
	}

	tmpBin, err := os.CreateTemp("", name+"-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpBin.Name()
	tmpBin.Close()
	defer os.Remove(tmpPath)

	buildCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	build := exec.CommandContext(buildCtx, "go", "build", "-o", tmpPath, "./"+cmdPkg+"/")
	build.Dir = sourceDir
	build.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+runtime.GOARCH, "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to build %s (GOOS=linux GOARCH=%s): %w\n%s", name, runtime.GOARCH, err, out)
	}

	cpCtx, cpCancel := context.WithTimeout(ctx, 30*time.Second)
	defer cpCancel()
	cp := exec.CommandContext(cpCtx, "docker", "cp", tmpPath, containerName+":/usr/local/bin/"+name)
	if out, err := cp.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to copy %s into container: %w\n%s", name, err, out)
	}

	// docker cp may not preserve execute permissions on all Docker versions.
	chmodCtx, chmodCancel := context.WithTimeout(ctx, 10*time.Second)
	defer chmodCancel()
	chmod := exec.CommandContext(chmodCtx, "docker", "exec", containerName, "chmod", "+x", "/usr/local/bin/"+name)
	if out, err := chmod.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to chmod %s: %w\n%s", name, err, out)
	}

	return nil
}
