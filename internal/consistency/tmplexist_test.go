package consistency

import (
	"strings"
	"testing"
)

func TestCheckTemplateExistence_AllMatched(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "handler.go", `package handler

import "net/http"

type Renderer struct{}

func (r *Renderer) Render(w http.ResponseWriter, name string, data any) {}

type Handler struct{ Tmpl *Renderer }

func (h *Handler) Home(w http.ResponseWriter, r *http.Request) {
	h.Tmpl.Render(w, "home", nil)
}
func (h *Handler) About(w http.ResponseWriter, r *http.Request) {
	h.Tmpl.Render(w, "about", nil)
}
`)
	writeFile(t, dir, "templates/home.html", `{{define "home"}}<h1>Home</h1>{{template "nav" .}}{{end}}`)
	writeFile(t, dir, "templates/about.html", `{{define "about"}}<h1>About</h1>{{template "nav" .}}{{end}}`)
	writeFile(t, dir, "templates/partials.html", `{{define "nav"}}<nav>Nav</nav>{{end}}`)

	result := CheckTemplateExistence(dir)

	if !result.Passed {
		t.Errorf("expected PASS, got FAIL:\n%s", result)
	}
	if result.Summary != "3 template defs, 4 references" {
		t.Errorf("unexpected summary: %s", result.Summary)
	}
}

func TestCheckTemplateExistence_MissingDefinition(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "handler.go", `package handler

import "net/http"

type Renderer struct{}

func (r *Renderer) Render(w http.ResponseWriter, name string, data any) {}

type Handler struct{ Tmpl *Renderer }

func (h *Handler) Home(w http.ResponseWriter, r *http.Request) {
	h.Tmpl.Render(w, "home", nil)
	h.Tmpl.Render(w, "missing_page", nil)
}
`)
	writeFile(t, dir, "templates/home.html", `{{define "home"}}<h1>Home</h1>{{end}}`)

	result := CheckTemplateExistence(dir)

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
	if !strings.Contains(errors[0].Message, "missing_page") {
		t.Errorf("expected error about missing_page, got: %s", errors[0].Message)
	}
}

func TestCheckTemplateExistence_UnreferencedDefinition(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "handler.go", `package handler

import "net/http"

type Renderer struct{}

func (r *Renderer) Render(w http.ResponseWriter, name string, data any) {}

type Handler struct{ Tmpl *Renderer }

func (h *Handler) Home(w http.ResponseWriter, r *http.Request) {
	h.Tmpl.Render(w, "home", nil)
}
`)
	writeFile(t, dir, "templates/home.html", `{{define "home"}}<h1>Home</h1>{{end}}`)
	writeFile(t, dir, "templates/orphan.html", `{{define "orphan"}}<h1>Orphan</h1>{{end}}`)

	result := CheckTemplateExistence(dir)

	if !result.Passed {
		t.Error("expected PASS (warnings don't fail)")
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
	if !strings.Contains(warnings[0].Message, "orphan") {
		t.Errorf("expected warning about orphan, got: %s", warnings[0].Message)
	}
}

func TestCheckTemplateExistence_HTMLTemplateInclusion(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "handler.go", `package handler

import "net/http"

type Renderer struct{}

func (r *Renderer) Render(w http.ResponseWriter, name string, data any) {}

type Handler struct{ Tmpl *Renderer }

func (h *Handler) Home(w http.ResponseWriter, r *http.Request) {
	h.Tmpl.Render(w, "home", nil)
}
`)
	writeFile(t, dir, "templates/home.html", `{{define "home"}}<h1>Home</h1>{{template "missing_partial" .}}{{end}}`)

	result := CheckTemplateExistence(dir)

	if result.Passed {
		t.Error("expected FAIL (missing_partial not defined), got PASS")
	}

	var errors []Finding
	for _, f := range result.Findings {
		if f.Severity == SeverityError {
			errors = append(errors, f)
		}
	}
	if len(errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errors))
	}
	if !strings.Contains(errors[0].Message, "missing_partial") {
		t.Errorf("unexpected error: %s", errors[0].Message)
	}
}

func TestCheckTemplateExistence_ExecuteTemplate(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "handler.go", `package handler

import (
	"html/template"
	"net/http"
)

func Serve(w http.ResponseWriter, r *http.Request, t *template.Template) {
	t.ExecuteTemplate(w, "page", nil)
}
`)
	writeFile(t, dir, "templates/page.html", `{{define "page"}}<p>Hello</p>{{end}}`)

	result := CheckTemplateExistence(dir)

	if !result.Passed {
		t.Errorf("expected PASS (ExecuteTemplate matched), got FAIL:\n%s", result)
	}
}

func TestCheckTemplateExistence_NoTemplates(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "main.go", `package main
func main() {}
`)

	result := CheckTemplateExistence(dir)

	if !result.Passed {
		t.Errorf("expected PASS with no templates, got FAIL")
	}
}

func TestCheckTemplateExistence_WhitespaceTrimSyntax(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "handler.go", `package handler

import "net/http"

type Renderer struct{}

func (r *Renderer) Render(w http.ResponseWriter, name string, data any) {}

type Handler struct{ Tmpl *Renderer }

func (h *Handler) Home(w http.ResponseWriter, r *http.Request) {
	h.Tmpl.Render(w, "trimmed", nil)
}
`)
	// Go template whitespace trim syntax: {{- define "name" -}}
	writeFile(t, dir, "templates/trimmed.html", `{{- define "trimmed" -}}<h1>Trimmed</h1>{{- end -}}`)

	result := CheckTemplateExistence(dir)

	if !result.Passed {
		t.Errorf("expected PASS (whitespace trim syntax), got FAIL:\n%s", result)
	}
}
