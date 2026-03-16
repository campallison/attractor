package consistency

import (
	"testing"
)

func TestCheckTemplateRoutes_AllMatched(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "routes.go", `package handler

import "net/http"

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", h.Home)
	mux.HandleFunc("POST /items", h.CreateItem)
	mux.HandleFunc("POST /items/{itemId}/vote", h.Vote)
	mux.HandleFunc("DELETE /items/{itemId}", h.Delete)
}
`)
	writeFile(t, dir, "handler.go", `package handler

import "net/http"

type Handler struct{}

func (h *Handler) Home(w http.ResponseWriter, r *http.Request) {}
func (h *Handler) CreateItem(w http.ResponseWriter, r *http.Request) {}
func (h *Handler) Vote(w http.ResponseWriter, r *http.Request) {}
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {}
`)
	writeFile(t, dir, "templates/index.html", `<html>
<body>
<form action="/" method="GET"><button>Home</button></form>
<form action="/items" method="POST"><input name="content"><button>Add</button></form>
<button hx-post="/items/{{.Item.ID}}/vote">Vote</button>
<button hx-delete="/items/{{.Item.ID}}">Delete</button>
</body>
</html>
`)

	result := CheckTemplateRoutes(dir)

	if !result.Passed {
		t.Errorf("expected PASS, got FAIL:\n%s", result)
	}
	if result.Summary != "4 template URLs, 4 routes" {
		t.Errorf("unexpected summary: %s", result.Summary)
	}
}

func TestCheckTemplateRoutes_MissingRoute(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "routes.go", `package handler

import "net/http"

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", h.Home)
	mux.HandleFunc("POST /items", h.CreateItem)
}
`)
	writeFile(t, dir, "handler.go", `package handler

import "net/http"

type Handler struct{}

func (h *Handler) Home(w http.ResponseWriter, r *http.Request) {}
func (h *Handler) CreateItem(w http.ResponseWriter, r *http.Request) {}
`)
	writeFile(t, dir, "templates/page.html", `<html>
<button hx-post="/items/{{.ID}}/vote">Vote</button>
<button hx-delete="/items/{{.ID}}">Delete</button>
</html>
`)

	result := CheckTemplateRoutes(dir)

	if result.Passed {
		t.Error("expected FAIL, got PASS")
	}

	var errors []Finding
	for _, f := range result.Findings {
		if f.Severity == SeverityError {
			errors = append(errors, f)
		}
	}
	if len(errors) != 2 {
		t.Fatalf("expected 2 errors, got %d: %v", len(errors), errors)
	}
}

func TestCheckTemplateRoutes_FormActionMatchesAnyMethod(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "routes.go", `package handler

import "net/http"

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /login", h.Login)
}
`)
	writeFile(t, dir, "handler.go", `package handler

import "net/http"

type Handler struct{}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {}
`)
	// Form action without method attribute — should match any method
	writeFile(t, dir, "templates/login.html", `<html>
<form action="/login"><button>Login</button></form>
</html>
`)

	result := CheckTemplateRoutes(dir)

	if !result.Passed {
		t.Errorf("expected PASS (form action matches any method), got FAIL:\n%s", result)
	}
}

func TestCheckTemplateRoutes_FormActionWithMethod(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "routes.go", `package handler

import "net/http"

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /search", h.Search)
}
`)
	writeFile(t, dir, "handler.go", `package handler

import "net/http"

type Handler struct{}

func (h *Handler) Search(w http.ResponseWriter, r *http.Request) {}
`)
	// Form with method="POST" but route is GET
	writeFile(t, dir, "templates/search.html", `<html>
<form action="/search" method="POST"><button>Search</button></form>
</html>
`)

	result := CheckTemplateRoutes(dir)

	if result.Passed {
		t.Error("expected FAIL (POST form action vs GET route), got PASS")
	}
}

func TestCheckTemplateRoutes_TemplateExpressionNormalization(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "routes.go", `package handler

import "net/http"

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("PUT /teams/{teamId}/columns/{columnId}/title", h.UpdateTitle)
}
`)
	writeFile(t, dir, "handler.go", `package handler

import "net/http"

type Handler struct{}

func (h *Handler) UpdateTitle(w http.ResponseWriter, r *http.Request) {}
`)
	writeFile(t, dir, "templates/col.html", `<html>
<form hx-put="/teams/{{.TeamID}}/columns/{{.Column.ID}}/title">
<input name="title"><button>Save</button>
</form>
</html>
`)

	result := CheckTemplateRoutes(dir)

	if !result.Passed {
		t.Errorf("expected PASS (template expressions match route params), got FAIL:\n%s", result)
	}
}

func TestCheckTemplateRoutes_NoTemplates(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "main.go", `package main

import "net/http"

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", Index)
}

func Index(w http.ResponseWriter, r *http.Request) {}
`)

	result := CheckTemplateRoutes(dir)

	if !result.Passed {
		t.Errorf("expected PASS with no templates, got FAIL:\n%s", result)
	}
	if result.Summary != "0 template URLs, 1 routes" {
		t.Errorf("unexpected summary: %s", result.Summary)
	}
}

func TestCheckTemplateRoutes_SkipsNonPathURLs(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "routes.go", `package handler

import "net/http"

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", h.Home)
}
`)
	writeFile(t, dir, "handler.go", `package handler

import "net/http"

type Handler struct{}

func (h *Handler) Home(w http.ResponseWriter, r *http.Request) {}
`)
	// URLs not starting with / should be ignored
	writeFile(t, dir, "templates/page.html", `<html>
<a hx-get="https://example.com/api">External</a>
<button hx-post="#modal">Modal</button>
</html>
`)

	result := CheckTemplateRoutes(dir)

	if !result.Passed {
		t.Errorf("expected PASS (non-path URLs ignored), got FAIL:\n%s", result)
	}
	if result.Summary != "0 template URLs, 1 routes" {
		t.Errorf("unexpected summary: %s", result.Summary)
	}
}

func TestCheckTemplateRoutes_GoHTMLAndTmplExtensions(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "routes.go", `package handler

import "net/http"

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /submit", h.Submit)
	mux.HandleFunc("GET /page", h.Page)
}
`)
	writeFile(t, dir, "handler.go", `package handler

import "net/http"

type Handler struct{}

func (h *Handler) Submit(w http.ResponseWriter, r *http.Request) {}
func (h *Handler) Page(w http.ResponseWriter, r *http.Request) {}
`)
	writeFile(t, dir, "templates/form.gohtml", `<form action="/submit" method="POST"><button>Go</button></form>`)
	writeFile(t, dir, "templates/page.tmpl", `<a hx-get="/page">Page</a>`)

	result := CheckTemplateRoutes(dir)

	if !result.Passed {
		t.Errorf("expected PASS (.gohtml and .tmpl extensions), got FAIL:\n%s", result)
	}
	if result.Summary != "2 template URLs, 2 routes" {
		t.Errorf("unexpected summary: %s", result.Summary)
	}
}

func TestCheckTemplateRoutes_RootPattern(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "routes.go", `package handler

import "net/http"

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", h.Home)
}
`)
	writeFile(t, dir, "handler.go", `package handler

import "net/http"

type Handler struct{}

func (h *Handler) Home(w http.ResponseWriter, r *http.Request) {}
`)
	writeFile(t, dir, "templates/nav.html", `<a hx-get="/">Home</a>`)

	result := CheckTemplateRoutes(dir)

	if !result.Passed {
		t.Errorf("expected PASS (/ matches /{$}), got FAIL:\n%s", result)
	}
}

func TestNormalizeRoutePattern(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/teams/{teamId}/items", "/teams/{_}/items"},
		{"/teams/{teamId}/items/{itemId}/vote", "/teams/{_}/items/{_}/vote"},
		{"/{$}", "/"},
		{"/", "/"},
		{"/static", "/static"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := NormalizeRoutePattern(tt.input)
			if got != tt.want {
				t.Errorf("NormalizeRoutePattern(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestNormalizeTemplateURL(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/teams/{{.TeamID}}/items", "/teams/{_}/items"},
		{"/teams/{{.TeamID}}/items/{{.Item.ID}}/vote", "/teams/{_}/items/{_}/vote"},
		{"/", "/"},
		{"/static", "/static"},
		{"/teams/{{.Team.ID}}/columns/{{.Column.ID}}/title", "/teams/{_}/columns/{_}/title"},
		{"/search?q={{.Query}}", "/search"},
		{"/items?page=1&sort=date", "/items"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := NormalizeTemplateURL(tt.input)
			if got != tt.want {
				t.Errorf("NormalizeTemplateURL(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSplitMethodFromPattern(t *testing.T) {
	tests := []struct {
		input      string
		wantMethod string
		wantPath   string
	}{
		{"GET /foo", "GET", "/foo"},
		{"POST /teams/{teamId}", "POST", "/teams/{teamId}"},
		{"/foo", "", "/foo"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			method, path := SplitMethodFromPattern(tt.input)
			if method != tt.wantMethod || path != tt.wantPath {
				t.Errorf("SplitMethodFromPattern(%q) = (%q, %q), want (%q, %q)",
					tt.input, method, path, tt.wantMethod, tt.wantPath)
			}
		})
	}
}
