package tools

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const (
	companionDBImage = "postgres:17"
	companionDBUser  = "attractor"
	companionDBPass  = "attractor"
	companionDBName  = "attractor"
	envFilePath      = "/opt/attractor/env"
)

// SetupCompanionDB creates a Docker network, starts a PostgreSQL container on
// it, connects the sandbox container, and writes a DATABASE_URL env file into
// the sandbox at /opt/attractor/env.
//
// networkName and dbContainerName should be derived from the sandbox container
// name (e.g., "attractor-XXXXXXXX-net" and "attractor-XXXXXXXX-db").
//
// The caller must defer TeardownCompanionDB to clean up.
func SetupCompanionDB(ctx context.Context, networkName, dbContainerName, sandboxContainerName string) error {
	if err := createNetwork(ctx, networkName); err != nil {
		return fmt.Errorf("create network: %w", err)
	}

	if err := startPostgres(ctx, networkName, dbContainerName); err != nil {
		return fmt.Errorf("start postgres: %w", err)
	}

	if err := connectToNetwork(ctx, networkName, sandboxContainerName); err != nil {
		return fmt.Errorf("connect sandbox to network: %w", err)
	}

	if err := waitForPostgres(ctx, dbContainerName); err != nil {
		return fmt.Errorf("postgres health check: %w", err)
	}

	dbURL := fmt.Sprintf("postgres://%s:%s@%s:5432/%s?sslmode=disable",
		companionDBUser, companionDBPass, dbContainerName, companionDBName)

	if err := writeEnvFile(ctx, sandboxContainerName, dbURL); err != nil {
		return fmt.Errorf("write env file: %w", err)
	}

	return nil
}

// TeardownCompanionDB stops the PostgreSQL container and removes the Docker
// network. It is safe to call even if setup partially failed.
//
// Uses context.Background so teardown completes even when the caller's
// context has been cancelled (e.g., on SIGINT).
func TeardownCompanionDB(_ context.Context, networkName, dbContainerName, sandboxContainerName string) error {
	teardownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Disconnect sandbox from network first (best-effort — may not be connected).
	_ = exec.CommandContext(teardownCtx, "docker", "network", "disconnect", networkName, sandboxContainerName).Run()

	var errs []error
	if err := exec.CommandContext(teardownCtx, "docker", "rm", "-f", dbContainerName).Run(); err != nil {
		errs = append(errs, fmt.Errorf("rm container %s: %w", dbContainerName, err))
	}
	if err := exec.CommandContext(teardownCtx, "docker", "network", "rm", networkName).Run(); err != nil {
		errs = append(errs, fmt.Errorf("rm network %s: %w", networkName, err))
	}
	return errors.Join(errs...)
}

func createNetwork(ctx context.Context, networkName string) error {
	createCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Remove stale network if it exists from a previous incomplete run.
	_ = exec.CommandContext(createCtx, "docker", "network", "rm", networkName).Run()

	cmd := exec.CommandContext(createCtx, "docker", "network", "create", networkName)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w\n%s", err, out)
	}
	return nil
}

func startPostgres(ctx context.Context, networkName, dbContainerName string) error {
	startCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Remove stale container if it exists.
	_ = exec.CommandContext(startCtx, "docker", "rm", "-f", dbContainerName).Run()

	cmd := exec.CommandContext(startCtx, "docker", "run", "-d",
		"--name", dbContainerName,
		"--network", networkName,
		"-e", "POSTGRES_USER="+companionDBUser,
		"-e", "POSTGRES_PASSWORD="+companionDBPass,
		"-e", "POSTGRES_DB="+companionDBName,
		companionDBImage,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w\n%s", err, out)
	}
	return nil
}

func connectToNetwork(ctx context.Context, networkName, containerName string) error {
	connCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(connCtx, "docker", "network", "connect", networkName, containerName)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w\n%s", err, out)
	}
	return nil
}

// waitForPostgres polls pg_isready inside the container until the database is
// accepting connections or the timeout is reached.
func waitForPostgres(ctx context.Context, dbContainerName string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("postgres not ready: %w", ctx.Err())
		case <-ticker.C:
			checkCtx, checkCancel := context.WithTimeout(ctx, 5*time.Second)
			err := exec.CommandContext(checkCtx, "docker", "exec", dbContainerName,
				"pg_isready", "-U", companionDBUser).Run()
			checkCancel()
			if err == nil {
				return nil
			}
		}
	}
}

// writeEnvFile pipes the env content via stdin to avoid shell interpolation.
func writeEnvFile(ctx context.Context, sandboxContainerName, dbURL string) error {
	writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	envContent := "DATABASE_URL=" + dbURL + "\n"

	cmd := exec.CommandContext(writeCtx, "docker", "exec", "-i", sandboxContainerName,
		"sh", "-c", "mkdir -p /opt/attractor && cat > "+envFilePath)
	cmd.Stdin = strings.NewReader(envContent)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w\n%s", err, out)
	}
	return nil
}
