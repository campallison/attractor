package consistency

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestParseFormsFromHTML_SingleForm(t *testing.T) {
	src := `<html><body>
<form method="POST" action="/teams">
  <input type="text" name="team-name" placeholder="Team name">
  <input type="password" name="password">
  <input type="text" name="display-name">
  <button type="submit">Create</button>
</form>
</body></html>`

	got := parseFormsFromHTML(src, "test.html")

	want := []HTMLForm{{
		Method: "POST",
		Action: "/teams",
		Fields: []FormField{
			{Name: "team-name", Type: "text"},
			{Name: "password", Type: "password"},
			{Name: "display-name", Type: "text"},
		},
		File: "test.html",
	}}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("parseFormsFromHTML mismatch (-want +got):\n%s", diff)
	}
}

func TestParseFormsFromHTML_MultipleForms(t *testing.T) {
	src := `<html><body>
<form method="POST" action="/create">
  <input type="text" name="title">
</form>
<form method="POST" action="/login">
  <input type="email" name="email">
  <input type="password" name="pass">
</form>
</body></html>`

	got := parseFormsFromHTML(src, "multi.html")

	if len(got) != 2 {
		t.Fatalf("got %d forms, want 2", len(got))
	}

	if got[0].Action != "/create" || len(got[0].Fields) != 1 {
		t.Errorf("form 0: got action=%q fields=%d, want /create with 1 field",
			got[0].Action, len(got[0].Fields))
	}
	if got[0].Fields[0].Name != "title" {
		t.Errorf("form 0 field 0: got name=%q, want title", got[0].Fields[0].Name)
	}

	if got[1].Action != "/login" || len(got[1].Fields) != 2 {
		t.Errorf("form 1: got action=%q fields=%d, want /login with 2 fields",
			got[1].Action, len(got[1].Fields))
	}
	if got[1].Fields[0].Name != "email" || got[1].Fields[0].Type != "email" {
		t.Errorf("form 1 field 0: got %+v, want email/email", got[1].Fields[0])
	}
}

func TestParseFormsFromHTML_GoTemplateExpressions(t *testing.T) {
	src := `{{define "page"}}
<html><body>
{{if .Error}}<div class="error">{{.Error}}</div>{{end}}
<form method="POST" action="/teams/{{.Team.ID}}/items">
  <input type="text" name="content">
  {{if eq .Type "action"}}
  <input type="text" name="assignee">
  {{end}}
</form>
</body></html>
{{end}}`

	got := parseFormsFromHTML(src, "tmpl.html")

	if len(got) != 1 {
		t.Fatalf("got %d forms, want 1", len(got))
	}

	if got[0].Action != "/teams/{_}/items" {
		t.Errorf("action = %q, want /teams/{_}/items (template exprs replaced)", got[0].Action)
	}

	wantFields := []FormField{
		{Name: "content", Type: "text"},
		{Name: "assignee", Type: "text"},
	}
	if diff := cmp.Diff(wantFields, got[0].Fields); diff != "" {
		t.Errorf("fields mismatch (-want +got):\n%s", diff)
	}
}

func TestParseFormsFromHTML_NoForms(t *testing.T) {
	src := `<html><body><h1>Hello</h1><p>No forms here.</p></body></html>`

	got := parseFormsFromHTML(src, "empty.html")

	if len(got) != 0 {
		t.Errorf("got %d forms, want 0", len(got))
	}
}

func TestParseFormsFromHTML_SelectAndTextarea(t *testing.T) {
	src := `<form method="POST" action="/survey">
  <select name="rating"><option value="1">1</option></select>
  <textarea name="comments"></textarea>
  <input type="hidden" name="csrf" value="abc123">
</form>`

	got := parseFormsFromHTML(src, "survey.html")

	if len(got) != 1 {
		t.Fatalf("got %d forms, want 1", len(got))
	}

	wantFields := []FormField{
		{Name: "rating", Type: "text"},
		{Name: "comments", Type: "text"},
		{Name: "csrf", Type: "hidden"},
	}
	if diff := cmp.Diff(wantFields, got[0].Fields); diff != "" {
		t.Errorf("fields mismatch (-want +got):\n%s", diff)
	}
}

func TestParseFormsFromHTML_DefaultMethodAndAction(t *testing.T) {
	src := `<form>
  <input name="q" type="search">
</form>`

	got := parseFormsFromHTML(src, "search.html")

	if len(got) != 1 {
		t.Fatalf("got %d forms, want 1", len(got))
	}
	if got[0].Method != "GET" {
		t.Errorf("method = %q, want GET (default)", got[0].Method)
	}
	if got[0].Action != "" {
		t.Errorf("action = %q, want empty (default)", got[0].Action)
	}
}

func TestParseFormsFromHTML_SelfClosingInputs(t *testing.T) {
	src := `<form method="POST" action="/submit">
  <input type="text" name="field1" />
  <input type="email" name="field2" />
</form>`

	got := parseFormsFromHTML(src, "self-closing.html")

	if len(got) != 1 {
		t.Fatalf("got %d forms, want 1", len(got))
	}
	if len(got[0].Fields) != 2 {
		t.Fatalf("got %d fields, want 2", len(got[0].Fields))
	}
	if got[0].Fields[0].Name != "field1" {
		t.Errorf("field 0 name = %q, want field1", got[0].Fields[0].Name)
	}
	if got[0].Fields[1].Name != "field2" || got[0].Fields[1].Type != "email" {
		t.Errorf("field 1 = %+v, want {field2 email}", got[0].Fields[1])
	}
}

func TestParseFormsFromHTML_InputsOutsideFormIgnored(t *testing.T) {
	src := `<html><body>
<input type="text" name="stray-field">
<form method="POST" action="/real">
  <input type="text" name="real-field">
</form>
<input type="text" name="another-stray">
</body></html>`

	got := parseFormsFromHTML(src, "stray.html")

	if len(got) != 1 {
		t.Fatalf("got %d forms, want 1", len(got))
	}
	if len(got[0].Fields) != 1 {
		t.Fatalf("got %d fields, want 1 (only real-field)", len(got[0].Fields))
	}
	if got[0].Fields[0].Name != "real-field" {
		t.Errorf("field name = %q, want real-field", got[0].Fields[0].Name)
	}
}

func TestExtractForms_WalksDirectory(t *testing.T) {
	dir := t.TempDir()

	tmplDir := filepath.Join(dir, "templates")
	if err := os.MkdirAll(tmplDir, 0o755); err != nil {
		t.Fatal(err)
	}

	html1 := `<form method="POST" action="/a"><input name="x"></form>`
	if err := os.WriteFile(filepath.Join(tmplDir, "page.html"), []byte(html1), 0o644); err != nil {
		t.Fatal(err)
	}

	html2 := `<form method="POST" action="/b"><input name="y"></form>`
	if err := os.WriteFile(filepath.Join(tmplDir, "other.gohtml"), []byte(html2), 0o644); err != nil {
		t.Fatal(err)
	}

	// Non-HTML file should be skipped.
	if err := os.WriteFile(filepath.Join(tmplDir, "style.css"), []byte("body{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	forms, err := ExtractForms(dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(forms) != 2 {
		t.Fatalf("got %d forms, want 2", len(forms))
	}

	actions := map[string]bool{}
	for _, f := range forms {
		actions[f.Action] = true
	}
	if !actions["/a"] || !actions["/b"] {
		t.Errorf("actions = %v, want /a and /b", actions)
	}
}

func TestExtractForms_SkipsHiddenAndVendorDirs(t *testing.T) {
	dir := t.TempDir()

	for _, sub := range []string{".git", "vendor", "node_modules"} {
		subDir := filepath.Join(dir, sub)
		if err := os.MkdirAll(subDir, 0o755); err != nil {
			t.Fatal(err)
		}
		html := `<form method="POST" action="/hidden"><input name="x"></form>`
		if err := os.WriteFile(filepath.Join(subDir, "page.html"), []byte(html), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// One visible form.
	html := `<form method="POST" action="/visible"><input name="y"></form>`
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(html), 0o644); err != nil {
		t.Fatal(err)
	}

	forms, err := ExtractForms(dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(forms) != 1 {
		t.Fatalf("got %d forms, want 1", len(forms))
	}
	if forms[0].Action != "/visible" {
		t.Errorf("action = %q, want /visible", forms[0].Action)
	}
}

func TestParseFormsFromHTML_UnclosedFormDropped(t *testing.T) {
	src := `<form method="POST" action="/never-closed">
  <input type="text" name="field1">
`

	got := parseFormsFromHTML(src, "unclosed.html")

	if len(got) != 0 {
		t.Errorf("got %d forms, want 0 (unclosed form should be dropped)", len(got))
	}
}

func TestParseFormsFromHTML_ButtonsWithoutNameIgnored(t *testing.T) {
	src := `<form method="POST" action="/submit">
  <input type="text" name="title">
  <button type="submit">Go</button>
</form>`

	got := parseFormsFromHTML(src, "btn.html")

	if len(got) != 1 {
		t.Fatalf("got %d forms, want 1", len(got))
	}
	if len(got[0].Fields) != 1 {
		t.Errorf("got %d fields, want 1 (button has no name attr)", len(got[0].Fields))
	}
}
