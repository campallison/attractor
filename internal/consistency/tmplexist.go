package consistency

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// TemplateDef represents a {{define "name"}} block in an HTML template.
type TemplateDef struct {
	Name string
	File string
	Line int
}

// TemplateRef represents a reference to a template name, either from Go code
// (via Render/ExecuteTemplate) or from an HTML template (via {{template "name"}}).
type TemplateRef struct {
	Name   string
	File   string
	Line   int
	Source string // "go" or "html"
}

var (
	defineRe      = regexp.MustCompile(`\{\{-?\s*define\s+"([^"]+)"`)
	blockRe       = regexp.MustCompile(`\{\{-?\s*block\s+"([^"]+)"`)
	tmplIncludeRe = regexp.MustCompile(`\{\{-?\s*template\s+"([^"]+)"`)
)

// CheckTemplateExistence verifies that every template name referenced in Go
// source and HTML templates has a corresponding {{define "name"}} block, and
// warns about defined templates that are never referenced.
func CheckTemplateExistence(root string) Result {
	goFiles, fset, err := parseGoFiles(root)
	if err != nil {
		return Result{
			Name:   "tmpl-names",
			Passed: false,
			Findings: []Finding{{
				Severity: SeverityError,
				Message:  fmt.Sprintf("failed to parse Go files: %v", err),
			}},
		}
	}

	defs, htmlRefs, scanErr := scanTemplateDefs(root)
	if scanErr != nil {
		return Result{
			Name:   "tmpl-names",
			Passed: false,
			Findings: []Finding{{
				Severity: SeverityError,
				Message:  fmt.Sprintf("failed to scan templates: %v", scanErr),
			}},
		}
	}

	goRefs := extractTemplateRefs(fset, goFiles, root)

	defSet := make(map[string]TemplateDef, len(defs))
	for _, d := range defs {
		defSet[d.Name] = d
	}

	allRefs := append(goRefs, htmlRefs...)
	referencedNames := make(map[string]bool)
	var findings []Finding

	for _, ref := range allRefs {
		referencedNames[ref.Name] = true
		if _, ok := defSet[ref.Name]; !ok {
			findings = append(findings, Finding{
				Severity: SeverityError,
				File:     ref.File,
				Line:     ref.Line,
				Message:  fmt.Sprintf("references template %q, but no {{define %q}} block exists", ref.Name, ref.Name),
			})
		}
	}

	for _, d := range defs {
		if !referencedNames[d.Name] {
			findings = append(findings, Finding{
				Severity: SeverityWarning,
				File:     d.File,
				Line:     d.Line,
				Message:  fmt.Sprintf("template %q is defined but never referenced", d.Name),
			})
		}
	}

	errors := 0
	for _, f := range findings {
		if f.Severity == SeverityError {
			errors++
		}
	}

	return Result{
		Name:     "tmpl-names",
		Passed:   errors == 0,
		Summary:  fmt.Sprintf("%d template defs, %d references", len(defs), len(allRefs)),
		Findings: findings,
	}
}

// scanTemplateDefs walks root looking for HTML template files, extracting
// {{define "name"}} definitions and {{template "name"}} references.
func scanTemplateDefs(root string) (defs []TemplateDef, refs []TemplateRef, err error) {
	err = filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			base := filepath.Base(path)
			switch base {
			case "vendor", "node_modules", "testdata":
				return filepath.SkipDir
			}
			if strings.HasPrefix(base, ".") && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		ext := filepath.Ext(path)
		if ext != ".html" && ext != ".tmpl" && ext != ".gohtml" {
			return nil
		}

		f, openErr := os.Open(path)
		if openErr != nil {
			return nil // skip unreadable files
		}
		defer f.Close()

		relPath, relErr := filepath.Rel(root, path)
		if relErr != nil {
			relPath = path
		}

		scanner := bufio.NewScanner(f)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			line := scanner.Text()

			for _, re := range []*regexp.Regexp{defineRe, blockRe} {
				for _, match := range re.FindAllStringSubmatch(line, -1) {
					defs = append(defs, TemplateDef{
						Name: match[1],
						File: relPath,
						Line: lineNum,
					})
				}
			}

			for _, match := range tmplIncludeRe.FindAllStringSubmatch(line, -1) {
				refs = append(refs, TemplateRef{
					Name:   match[1],
					File:   relPath,
					Line:   lineNum,
					Source: "html",
				})
			}
		}

		return scanner.Err()
	})

	return defs, refs, err
}

// extractTemplateRefs finds Go calls to Render, ExecuteTemplate, or similar
// functions where the template name is passed as a string literal.
//
// Matched patterns:
//
//	x.Render(w, "name", data)          — 2nd arg is template name
//	x.ExecuteTemplate(w, "name", data) — 2nd arg is template name
func extractTemplateRefs(fset *token.FileSet, files []*ast.File, root string) []TemplateRef {
	var refs []TemplateRef

	renderMethods := map[string]int{
		"Render":          1, // name is arg index 1
		"ExecuteTemplate": 1,
	}

	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}

			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}

			nameIdx, ok := renderMethods[sel.Sel.Name]
			if !ok {
				return true
			}
			if len(call.Args) <= nameIdx {
				return true
			}

			lit, ok := call.Args[nameIdx].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}

			name, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}

			pos := fset.Position(call.Pos())
			relPath, relErr := filepath.Rel(root, pos.Filename)
			if relErr != nil {
				relPath = pos.Filename
			}

			refs = append(refs, TemplateRef{
				Name:   name,
				File:   relPath,
				Line:   pos.Line,
				Source: "go",
			})
			return true
		})
	}

	return refs
}
