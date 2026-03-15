package consistency

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCheckRouteHandler_AllMatched(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "routes.go", `package handler

import "net/http"

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /", h.Home)
	mux.HandleFunc("POST /items", h.CreateItem)
}
`)
	writeFile(t, dir, "handler.go", `package handler

import "net/http"

type Handler struct{}

func (h *Handler) Home(w http.ResponseWriter, r *http.Request) {}
func (h *Handler) CreateItem(w http.ResponseWriter, r *http.Request) {}
`)

	result := CheckRouteHandler(dir)

	if !result.Passed {
		t.Errorf("expected PASS, got FAIL:\n%s", result)
	}
	if result.Summary != "2 routes, 2 handler methods" {
		t.Errorf("unexpected summary: %s", result.Summary)
	}
}

func TestCheckRouteHandler_MissingHandler(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "routes.go", `package handler

import "net/http"

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /", h.Home)
	mux.HandleFunc("POST /items", h.CreateItem)
	mux.HandleFunc("DELETE /items/{id}", h.DeleteItem)
}
`)
	writeFile(t, dir, "handler.go", `package handler

import "net/http"

type Handler struct{}

func (h *Handler) Home(w http.ResponseWriter, r *http.Request) {}
func (h *Handler) CreateItem(w http.ResponseWriter, r *http.Request) {}
`)

	result := CheckRouteHandler(dir)

	if result.Passed {
		t.Error("expected FAIL, got PASS")
	}

	var errors []Finding
	for _, f := range result.Findings {
		if f.Severity == SeverityError {
			errors = append(errors, f)
		}
	}
	if len(errors) != 1 {
		t.Fatalf("expected 1 error, got %d: %v", len(errors), errors)
	}
	want := "route \"DELETE /items/{id}\" references handler \"DeleteItem\", but no such method exists"
	if errors[0].Message != want {
		t.Errorf("unexpected message:\n got: %s\nwant: %s", errors[0].Message, want)
	}
}

func TestCheckRouteHandler_UnroutedHandler(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "routes.go", `package handler

import "net/http"

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /", h.Home)
}
`)
	writeFile(t, dir, "handler.go", `package handler

import "net/http"

type Handler struct{}

func (h *Handler) Home(w http.ResponseWriter, r *http.Request) {}
func (h *Handler) Orphan(w http.ResponseWriter, r *http.Request) {}
`)

	result := CheckRouteHandler(dir)

	if !result.Passed {
		t.Error("expected PASS (warnings don't cause failure)")
	}

	var warnings []Finding
	for _, f := range result.Findings {
		if f.Severity == SeverityWarning {
			warnings = append(warnings, f)
		}
	}
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
	if warnings[0].Message != `handler method "Orphan" on Handler is not registered to any route` {
		t.Errorf("unexpected warning: %s", warnings[0].Message)
	}
}

func TestCheckRouteHandler_MiddlewareWrapping(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "routes.go", `package handler

import "net/http"

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /", h.Home)
	mux.HandleFunc("GET /board", h.requireSession(h.Board))
	mux.HandleFunc("POST /items", h.requireSession(h.CreateItem))
}
`)
	writeFile(t, dir, "handler.go", `package handler

import "net/http"

type Handler struct{}

func (h *Handler) requireSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { next(w, r) }
}

func (h *Handler) Home(w http.ResponseWriter, r *http.Request) {}
func (h *Handler) Board(w http.ResponseWriter, r *http.Request) {}
func (h *Handler) CreateItem(w http.ResponseWriter, r *http.Request) {}
`)

	result := CheckRouteHandler(dir)

	if !result.Passed {
		t.Errorf("expected PASS, got FAIL:\n%s", result)
	}
	if result.Summary != "3 routes, 3 handler methods" {
		t.Errorf("unexpected summary: %s", result.Summary)
	}

	// requireSession itself should not appear as a handler (wrong signature)
	for _, f := range result.Findings {
		if f.Severity == SeverityError {
			t.Errorf("unexpected error: %s", f.Message)
		}
	}
}

func TestCheckRouteHandler_NestedMiddleware(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "routes.go", `package handler

import "net/http"

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin", h.requireSession(h.requireAdmin(h.Admin)))
}
`)
	writeFile(t, dir, "handler.go", `package handler

import "net/http"

type Handler struct{}

func (h *Handler) requireSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { next(w, r) }
}
func (h *Handler) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { next(w, r) }
}
func (h *Handler) Admin(w http.ResponseWriter, r *http.Request) {}
`)

	result := CheckRouteHandler(dir)

	if !result.Passed {
		t.Errorf("expected PASS, got FAIL:\n%s", result)
	}
}

func TestCheckRouteHandler_StandaloneFunction(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "main.go", `package main

import "net/http"

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", HealthCheck)
}

func HealthCheck(w http.ResponseWriter, r *http.Request) {}
`)

	result := CheckRouteHandler(dir)

	if !result.Passed {
		t.Errorf("expected PASS, got FAIL:\n%s", result)
	}
	if result.Summary != "1 routes, 1 handler methods" {
		t.Errorf("unexpected summary: %s", result.Summary)
	}
}

func TestCheckRouteHandler_HandleNotJustHandleFunc(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "routes.go", `package handler

import "net/http"

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.Handle("GET /health", http.HandlerFunc(h.Health))
}
`)
	writeFile(t, dir, "handler.go", `package handler

import "net/http"

type Handler struct{}

func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {}
`)

	result := CheckRouteHandler(dir)

	if !result.Passed {
		t.Errorf("expected PASS for Handle call, got FAIL:\n%s", result)
	}
	if result.Summary != "1 routes, 1 handler methods" {
		t.Errorf("unexpected summary: %s", result.Summary)
	}
}

func TestCheckRouteHandler_NoRoutes(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "main.go", `package main

func main() {}
`)

	result := CheckRouteHandler(dir)

	if !result.Passed {
		t.Errorf("expected PASS with no routes, got FAIL")
	}
	if result.Summary != "0 routes, 0 handler methods" {
		t.Errorf("unexpected summary: %s", result.Summary)
	}
}

func TestCheckRouteHandler_IgnoresTestFiles(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "routes.go", `package handler

import "net/http"

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /", h.Home)
}
`)
	writeFile(t, dir, "handler.go", `package handler

import "net/http"

type Handler struct{}

func (h *Handler) Home(w http.ResponseWriter, r *http.Request) {}
`)
	// This test file registers a route to a handler that doesn't exist in prod code.
	// It should be ignored entirely.
	writeFile(t, dir, "handler_test.go", `package handler

import "net/http"

func testSetup(mux *http.ServeMux) {
	mux.HandleFunc("GET /test-only", TestHandler)
}

func TestHandler(w http.ResponseWriter, r *http.Request) {}
`)

	result := CheckRouteHandler(dir)

	if !result.Passed {
		t.Errorf("expected PASS (test files ignored), got FAIL:\n%s", result)
	}
	if result.Summary != "1 routes, 1 handler methods" {
		t.Errorf("unexpected summary: %s", result.Summary)
	}
}

func TestCheckRouteHandler_SkipsVendor(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "routes.go", `package handler

import "net/http"

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /", h.Home)
}
`)
	writeFile(t, dir, "handler.go", `package handler

import "net/http"

type Handler struct{}

func (h *Handler) Home(w http.ResponseWriter, r *http.Request) {}
`)
	writeFile(t, dir, "vendor/third/party.go", `package third

import "net/http"

func Serve(w http.ResponseWriter, r *http.Request) {}
`)

	result := CheckRouteHandler(dir)

	if !result.Passed {
		t.Errorf("expected PASS, got FAIL:\n%s", result)
	}
	if result.Summary != "1 routes, 1 handler methods" {
		t.Errorf("unexpected summary: %s (vendor handler should be excluded)", result.Summary)
	}
}

func TestCheckRouteHandler_Subdirectories(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "cmd/server/main.go", `package main

import "net/http"

func setupRoutes(mux *http.ServeMux, h *handler) {
	mux.HandleFunc("GET /", h.Home)
	mux.HandleFunc("POST /items", h.CreateItem)
}
`)
	writeFile(t, dir, "internal/handler/handler.go", `package handler

import "net/http"

type Handler struct{}

func (h *Handler) Home(w http.ResponseWriter, r *http.Request) {}
func (h *Handler) CreateItem(w http.ResponseWriter, r *http.Request) {}
`)

	result := CheckRouteHandler(dir)

	if !result.Passed {
		t.Errorf("expected PASS, got FAIL:\n%s", result)
	}
}

func TestExtractHandlerName(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "selector",
			src:  `package p; import "net/http"; func f(m *http.ServeMux, h *H) { m.HandleFunc("/", h.Foo) }`,
			want: "Foo",
		},
		{
			name: "ident",
			src:  `package p; import "net/http"; func f(m *http.ServeMux) { m.HandleFunc("/", Bar) }`,
			want: "Bar",
		},
		{
			name: "wrapped",
			src:  `package p; import "net/http"; func f(m *http.ServeMux, h *H) { m.HandleFunc("/", h.mw(h.Baz)) }`,
			want: "Baz",
		},
		{
			name: "double_wrapped",
			src:  `package p; import "net/http"; func f(m *http.ServeMux, h *H) { m.HandleFunc("/", h.a(h.b(h.Deep))) }`,
			want: "Deep",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			routes := parseRoutesFromSource(t, tt.src)
			if len(routes) != 1 {
				t.Fatalf("expected 1 route, got %d", len(routes))
			}
			if routes[0].Handler != tt.want {
				t.Errorf("got handler %q, want %q", routes[0].Handler, tt.want)
			}
		})
	}
}

func TestIsHandlerSignature(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want bool
	}{
		{
			name: "standard",
			src:  `package p; import "net/http"; func Foo(w http.ResponseWriter, r *http.Request) {}`,
			want: true,
		},
		{
			name: "unnamed_params",
			src:  `package p; import "net/http"; func Foo(http.ResponseWriter, *http.Request) {}`,
			want: true,
		},
		{
			name: "wrong_first",
			src:  `package p; import "net/http"; func Foo(w http.ResponseWriter, r http.Request) {}`,
			want: false,
		},
		{
			name: "extra_param",
			src:  `package p; import "net/http"; func Foo(w http.ResponseWriter, r *http.Request, x int) {}`,
			want: false,
		},
		{
			name: "no_params",
			src:  `package p; func Foo() {}`,
			want: false,
		},
		{
			name: "returns_handlerfunc",
			src:  `package p; import "net/http"; func Mw(next http.HandlerFunc) http.HandlerFunc { return next }`,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handlers := parseHandlersFromSource(t, tt.src)
			got := len(handlers) > 0
			if got != tt.want {
				t.Errorf("isHandlerSignature = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResultString(t *testing.T) {
	r := Result{
		Name:    "routes",
		Passed:  false,
		Summary: "3 routes, 2 handler methods",
		Findings: []Finding{
			{Severity: SeverityError, File: "routes.go", Line: 10, Message: `route "DELETE /x" references handler "X", but no such method exists`},
			{Severity: SeverityWarning, File: "handler.go", Line: 5, Message: `handler method "Y" on Handler is not registered to any route`},
		},
	}

	got := r.String()

	wantPrefix := "[CHECK:routes] FAIL (3 routes, 2 handler methods)\n"
	if !strings.HasPrefix(got, wantPrefix) {
		t.Errorf("unexpected prefix:\n got: %q\nwant: %q", got[:min(len(got), len(wantPrefix))], wantPrefix)
	}
	wantSubstrings := []string{
		`routes.go:10:`,
		`handler "X"`,
		`handler.go:5:`,
		`handler method "Y"`,
	}
	for _, sub := range wantSubstrings {
		if !strings.Contains(got, sub) {
			t.Errorf("result string missing %q:\n%s", sub, got)
		}
	}
}

func TestRunChecks_UnknownCheck(t *testing.T) {
	results := RunChecks(t.TempDir(), []string{"nonexistent"})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Passed {
		t.Error("expected FAIL for unknown check")
	}
}

func TestRunChecks_AllChecks(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.go", `package main
func main() {}
`)

	results := RunChecks(dir, nil)

	if len(results) == 0 {
		t.Error("expected at least one result when running all checks")
	}
	for _, r := range results {
		if _, ok := Registry[r.Name]; !ok {
			t.Errorf("result for unregistered check %q", r.Name)
		}
	}
}

func TestFinding_String(t *testing.T) {
	tests := []struct {
		name    string
		finding Finding
		want    string
	}{
		{
			name:    "with_file_and_line",
			finding: Finding{File: "foo.go", Line: 42, Message: "something wrong"},
			want:    "  foo.go:42: something wrong",
		},
		{
			name:    "with_file_only",
			finding: Finding{File: "foo.go", Message: "something wrong"},
			want:    "  foo.go: something wrong",
		},
		{
			name:    "message_only",
			finding: Finding{Message: "something wrong"},
			want:    "  something wrong",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if diff := cmp.Diff(tt.want, tt.finding.String()); diff != "" {
				t.Errorf("Finding.String() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestHasErrors(t *testing.T) {
	tests := []struct {
		name    string
		results []Result
		want    bool
	}{
		{"no results", nil, false},
		{"all pass", []Result{{Passed: true}}, false},
		{"one fail", []Result{{Passed: true}, {Passed: false}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasErrors(tt.results); got != tt.want {
				t.Errorf("HasErrors() = %v, want %v", got, tt.want)
			}
		})
	}
}

// --- helpers ---

func parseRoutesFromSource(t *testing.T, src string) []Route {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "src.go", src)
	files, fset, err := parseGoFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	return extractRoutes(fset, files, dir)
}

func parseHandlersFromSource(t *testing.T, src string) []DeclaredHandler {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "src.go", src)
	files, fset, err := parseGoFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	return extractHandlers(fset, files, dir)
}

