// Command check-behavioral starts the generated server, probes it for health,
// sweeps all registered routes for HTTP 500 errors, and reports results.
//
// It is designed to run inside the Docker sandbox container alongside the
// generated code, invoked as part of a pipeline's check_cmd.
//
// Usage:
//
//	check-behavioral [--root=.] [--timeout=15s] [--env-file=/opt/attractor/env]
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/campallison/attractor/internal/consistency"
)

func main() {
	root := flag.String("root", ".", "root directory of the Go project")
	timeout := flag.Duration("timeout", 15*time.Second, "how long to wait for the server to start")
	envFile := flag.String("env-file", "/opt/attractor/env", "path to env file with DATABASE_URL")
	flag.Parse()

	env := loadEnvFile(*envFile)

	serverBin, err := discoverAndBuild(*root)
	if err != nil {
		fmt.Printf("[CHECK:startup] FAIL (0 routes checked)\n")
		fmt.Printf("  could not build server: %v\n", err)
		os.Exit(1)
	}
	defer os.Remove(serverBin)

	port, err := freePort()
	if err != nil {
		fmt.Printf("[CHECK:startup] FAIL (0 routes checked)\n")
		fmt.Printf("  could not find free port: %v\n", err)
		os.Exit(1)
	}

	proc, stderr, err := startServer(serverBin, port, env, *root)
	if err != nil {
		fmt.Printf("[CHECK:startup] FAIL (0 routes checked)\n")
		fmt.Printf("  could not start server: %v\n", err)
		os.Exit(1)
	}

	// Continuously drain stderr so the server doesn't block on a full pipe buffer.
	serverLog := newStderrCapture(stderr, 50)

	exited := make(chan error, 1)
	go func() { exited <- proc.Wait() }()

	defer func() {
		proc.Process.Kill()
		<-exited
	}()

	baseURL := fmt.Sprintf("http://localhost:%d", port)

	if err := waitForHealth(baseURL, *timeout, exited); err != nil {
		fmt.Printf("[CHECK:startup] FAIL (0 routes checked)\n")
		fmt.Printf("  server did not become healthy within %s: %v\n", *timeout, err)
		if output := serverLog.String(); output != "" {
			fmt.Printf("  server stderr:\n%s\n", indent(output, "    "))
		}
		os.Exit(1)
	}
	fmt.Printf("[CHECK:startup] PASS\n")

	routes, err := consistency.ListRoutes(*root)
	if err != nil {
		fmt.Printf("[CHECK:sweep] FAIL (route extraction error)\n")
		fmt.Printf("  %v\n", err)
		os.Exit(1)
	}

	if len(routes) == 0 {
		fmt.Printf("[CHECK:sweep] PASS (0 routes, nothing to sweep)\n")
		return
	}

	failures, connErrors := sweepRoutes(baseURL, routes)

	if len(failures) > 0 || connErrors > 0 {
		fmt.Printf("[CHECK:sweep] FAIL (%d routes, %d returned 500, %d unreachable)\n",
			len(routes), len(failures), connErrors)
		for _, f := range failures {
			fmt.Printf("  %s %s → HTTP 500\n", f.method, f.path)
			if f.body != "" {
				fmt.Printf("    body: %s\n", f.body)
			}
		}
		if connErrors > 0 {
			fmt.Printf("  %d routes unreachable (server may have crashed)\n", connErrors)
		}
		if output := serverLog.String(); output != "" {
			fmt.Printf("  server log:\n%s\n", indent(output, "    "))
		}
		os.Exit(1)
	}

	fmt.Printf("[CHECK:sweep] PASS (%d routes, 0 returned 500)\n", len(routes))
}

// loadEnvFile reads KEY=VALUE pairs from the given file. Missing files are
// silently ignored (the env vars may already be set externally).
func loadEnvFile(path string) map[string]string {
	env := make(map[string]string)
	f, err := os.Open(path)
	if err != nil {
		return env
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			env[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return env
}

// discoverAndBuild finds the first cmd/*/main.go under root and builds it.
// If multiple cmd/ entries exist, the first one in lexicographic order is used.
func discoverAndBuild(root string) (string, error) {
	cmdDir := filepath.Join(root, "cmd")
	entries, err := os.ReadDir(cmdDir)
	if err != nil {
		return "", fmt.Errorf("cannot read %s: %w", cmdDir, err)
	}

	var serverPkg string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		mainFile := filepath.Join(cmdDir, e.Name(), "main.go")
		if _, err := os.Stat(mainFile); err == nil {
			serverPkg = "./cmd/" + e.Name() + "/"
			break
		}
	}
	if serverPkg == "" {
		return "", fmt.Errorf("no cmd/*/main.go found under %s", root)
	}

	tmpBin, err := os.CreateTemp("", "check-behavioral-server-*")
	if err != nil {
		return "", err
	}
	binPath := tmpBin.Name()
	tmpBin.Close()

	build := exec.Command("go", "build", "-o", binPath, serverPkg)
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		os.Remove(binPath)
		return "", fmt.Errorf("go build %s failed: %w\n%s", serverPkg, err, out)
	}

	return binPath, nil
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port, nil
}

// startServer launches the server binary with DATABASE_URL and addr configured.
// It returns the running command and a scanner on stderr for diagnostics.
func startServer(binPath string, port int, env map[string]string, workDir string) (*exec.Cmd, *bufio.Scanner, error) {
	addr := fmt.Sprintf(":%d", port)
	cmd := exec.Command(binPath, "--addr", addr)
	if dbFlag := env["DATABASE_URL"]; dbFlag != "" {
		cmd.Args = append(cmd.Args, "--db", dbFlag)
	}

	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), envToSlice(env)...)

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, err
	}
	stderr := bufio.NewScanner(stderrPipe)

	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}

	return cmd, stderr, nil
}

func envToSlice(env map[string]string) []string {
	s := make([]string, 0, len(env))
	for k, v := range env {
		s = append(s, k+"="+v)
	}
	return s
}

// waitForHealth polls the server's root URL until it responds or the timeout
// is reached. Any non-error HTTP response (including 3xx, 4xx) counts as
// healthy — it means the server is accepting connections. If the server process
// exits before responding, the health check fails immediately.
func waitForHealth(baseURL string, timeout time.Duration, exited <-chan error) error {
	deadline := time.After(timeout)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	client := &http.Client{
		Timeout: 2 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	for {
		select {
		case err := <-exited:
			if err != nil {
				return fmt.Errorf("server exited: %v", err)
			}
			return fmt.Errorf("server exited with code 0 before accepting connections")
		case <-deadline:
			return fmt.Errorf("timeout after %s", timeout)
		case <-ticker.C:
			resp, err := client.Get(baseURL + "/")
			if err != nil {
				continue
			}
			resp.Body.Close()
			return nil
		}
	}
}

var routeParamRe = regexp.MustCompile(`\{[^}]+\}`)

const maxBodyCapture = 2048

type sweepFailure struct {
	method string
	path   string
	body   string
}

// sweepRoutes makes one request to each route and collects any that return 500.
// For 500 responses, up to maxBodyCapture bytes of the response body are
// captured to help the agent diagnose the root cause.
// Connection errors (server crashed mid-sweep) are counted separately.
func sweepRoutes(baseURL string, routes []consistency.Route) (failures []sweepFailure, connErrors int) {
	client := &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	for _, r := range routes {
		method, path := parsePattern(r.Pattern)
		path = routeParamRe.ReplaceAllString(path, "test-placeholder")

		url := baseURL + path
		req, err := http.NewRequest(method, url, nil)
		if err != nil {
			continue
		}

		resp, err := client.Do(req)
		if err != nil {
			connErrors++
			continue
		}

		if resp.StatusCode == http.StatusInternalServerError {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, maxBodyCapture))
			resp.Body.Close()
			failures = append(failures, sweepFailure{
				method: method,
				path:   path,
				body:   strings.TrimSpace(string(body)),
			})
		} else {
			resp.Body.Close()
		}
	}

	return failures, connErrors
}

// parsePattern splits a Go 1.22+ route pattern like "GET /foo/{id}" into
// method and path. Patterns without a method prefix default to GET.
func parsePattern(pattern string) (method, path string) {
	pattern = strings.TrimSpace(pattern)
	if i := strings.Index(pattern, " "); i > 0 {
		return pattern[:i], pattern[i+1:]
	}
	return "GET", pattern
}

// stderrCapture reads from a scanner in a background goroutine and retains the
// last maxLines lines in a ring buffer. This prevents the server process from
// blocking on a full stderr pipe while providing diagnostic output on demand.
type stderrCapture struct {
	mu       sync.Mutex
	lines    []string
	maxLines int
}

func newStderrCapture(sc *bufio.Scanner, maxLines int) *stderrCapture {
	c := &stderrCapture{maxLines: maxLines}
	go func() {
		for sc.Scan() {
			c.mu.Lock()
			c.lines = append(c.lines, sc.Text())
			if len(c.lines) > c.maxLines {
				c.lines = c.lines[len(c.lines)-c.maxLines:]
			}
			c.mu.Unlock()
		}
	}()
	return c
}

func (c *stderrCapture) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Join(c.lines, "\n")
}

func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n")
}
