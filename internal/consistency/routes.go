package consistency

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Route represents a registered HTTP route extracted from a HandleFunc/Handle call.
type Route struct {
	Pattern string // e.g., "GET /teams/{teamId}/items"
	Handler string // resolved handler name, e.g., "CreateItem"
	File    string
	Line    int
}

// DeclaredHandler represents an exported method with an HTTP handler signature.
type DeclaredHandler struct {
	Name     string
	Receiver string // receiver type name, empty for standalone functions
	File     string
	Line     int
}

// CheckRouteHandler verifies that every registered route references a handler
// method that exists, and warns about handler methods that are not registered
// to any route.
func CheckRouteHandler(root string) Result {
	files, fset, err := parseGoFiles(root)
	if err != nil {
		return Result{
			Name:   "routes",
			Passed: false,
			Findings: []Finding{{
				Severity: SeverityError,
				Message:  fmt.Sprintf("failed to parse Go files: %v", err),
			}},
		}
	}

	routes := extractRoutes(fset, files, root)
	handlers := extractHandlers(fset, files, root)

	handlerSet := make(map[string]DeclaredHandler, len(handlers))
	for _, h := range handlers {
		handlerSet[h.Name] = h
	}

	routedNames := make(map[string]bool)
	var findings []Finding

	for _, r := range routes {
		routedNames[r.Handler] = true
		if _, ok := handlerSet[r.Handler]; !ok {
			findings = append(findings, Finding{
				Severity: SeverityError,
				File:     r.File,
				Line:     r.Line,
				Message:  fmt.Sprintf("route %q references handler %q, but no such method exists", r.Pattern, r.Handler),
			})
		}
	}

	for _, h := range handlers {
		if !routedNames[h.Name] {
			recv := h.Receiver
			if recv == "" {
				recv = "(function)"
			}
			findings = append(findings, Finding{
				Severity: SeverityWarning,
				File:     h.File,
				Line:     h.Line,
				Message:  fmt.Sprintf("handler method %q on %s is not registered to any route", h.Name, recv),
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
		Name:     "routes",
		Passed:   errors == 0,
		Summary:  fmt.Sprintf("%d routes, %d handler methods", len(routes), len(handlers)),
		Findings: findings,
	}
}

// parseGoFiles walks root recursively, parsing all non-test Go source files.
// Directories named vendor, node_modules, testdata, or starting with "." are skipped.
func parseGoFiles(root string) ([]*ast.File, *token.FileSet, error) {
	fset := token.NewFileSet()
	var files []*ast.File

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
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
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return nil // skip files with syntax errors; go build will catch those
		}
		files = append(files, f)
		return nil
	})

	return files, fset, err
}

// extractRoutes finds all HandleFunc/Handle calls and returns the registered routes.
func extractRoutes(fset *token.FileSet, files []*ast.File, root string) []Route {
	var routes []Route

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
			if sel.Sel.Name != "HandleFunc" && sel.Sel.Name != "Handle" {
				return true
			}
			if len(call.Args) < 2 {
				return true
			}

			patternLit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || patternLit.Kind != token.STRING {
				return true
			}
			pattern, err := strconv.Unquote(patternLit.Value)
			if err != nil {
				return true
			}

			handlerName := extractHandlerName(call.Args[1])
			if handlerName == "" {
				return true
			}

			pos := fset.Position(call.Pos())
			relPath, relErr := filepath.Rel(root, pos.Filename)
			if relErr != nil {
				relPath = pos.Filename
			}

			routes = append(routes, Route{
				Pattern: pattern,
				Handler: handlerName,
				File:    relPath,
				Line:    pos.Line,
			})
			return true
		})
	}

	return routes
}

// extractHandlerName resolves the handler function name from the expression
// passed to HandleFunc/Handle. It unwraps middleware wrappers by recursively
// searching call expression arguments for the innermost selector or identifier.
//
//	h.Landing               → "Landing"
//	h.requireSession(h.Board) → "Board"
//	someFunc                → "someFunc"
func extractHandlerName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.SelectorExpr:
		return e.Sel.Name
	case *ast.Ident:
		return e.Name
	case *ast.CallExpr:
		for _, arg := range e.Args {
			if name := extractHandlerName(arg); name != "" {
				return name
			}
		}
	}
	return ""
}

// extractHandlers finds all exported functions and methods whose signature
// matches func(http.ResponseWriter, *http.Request).
func extractHandlers(fset *token.FileSet, files []*ast.File, root string) []DeclaredHandler {
	var handlers []DeclaredHandler

	for _, f := range files {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || !fn.Name.IsExported() {
				continue
			}
			if !isHandlerSignature(fn.Type) {
				continue
			}

			receiver := ""
			if fn.Recv != nil && len(fn.Recv.List) > 0 {
				receiver = receiverTypeName(fn.Recv.List[0].Type)
			}

			pos := fset.Position(fn.Pos())
			relPath, relErr := filepath.Rel(root, pos.Filename)
			if relErr != nil {
				relPath = pos.Filename
			}

			handlers = append(handlers, DeclaredHandler{
				Name:     fn.Name.Name,
				Receiver: receiver,
				File:     relPath,
				Line:     pos.Line,
			})
		}
	}

	return handlers
}

// isHandlerSignature returns true if ft matches func(*.ResponseWriter, *.Request).
// It checks selector names rather than full package paths, so it works regardless
// of how net/http is imported (aliased or not).
func isHandlerSignature(ft *ast.FuncType) bool {
	if ft.Params == nil {
		return false
	}

	types := flattenParamTypes(ft.Params)
	if len(types) != 2 {
		return false
	}

	sel, ok := types[0].(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "ResponseWriter" {
		return false
	}

	star, ok := types[1].(*ast.StarExpr)
	if !ok {
		return false
	}
	sel2, ok := star.X.(*ast.SelectorExpr)
	if !ok || sel2.Sel.Name != "Request" {
		return false
	}

	return true
}

// flattenParamTypes expands a FieldList into individual parameter types,
// duplicating the type for fields with multiple names (e.g., "a, b int").
func flattenParamTypes(fl *ast.FieldList) []ast.Expr {
	var types []ast.Expr
	for _, field := range fl.List {
		count := len(field.Names)
		if count == 0 {
			count = 1 // unnamed parameter
		}
		for range count {
			types = append(types, field.Type)
		}
	}
	return types
}

// receiverTypeName extracts the type name from a receiver expression,
// stripping pointer indirection.
func receiverTypeName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.StarExpr:
		return receiverTypeName(e.X)
	case *ast.Ident:
		return e.Name
	case *ast.IndexExpr:
		return receiverTypeName(e.X)
	}
	return ""
}
