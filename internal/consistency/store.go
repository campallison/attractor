package consistency

import (
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"
)

// InterfaceMethod represents a method declared in a Go interface.
type InterfaceMethod struct {
	Interface string
	Method    string
	File      string
	Line      int
}

// StoreCall represents a method call on a struct field (e.g., h.Store.GetTeam).
type StoreCall struct {
	Field  string // struct field name, e.g., "Store"
	Method string // method name, e.g., "GetTeam"
	File   string
	Line   int
}

// CheckStoreInterface verifies that every method called on a struct field
// whose type matches an interface exists in that interface, and warns about
// interface methods that are never called.
//
// This check operates at the AST level without type resolution. It uses
// name-based matching: a struct field's type name (or the selector portion
// of a qualified type) is matched against interface names. This is a
// heuristic that works well for the common Go web application pattern of
// injecting interfaces via struct fields.
//
// Known limitations:
//   - No distinction between interfaces in different packages with the same name.
//   - Embedded interfaces are not expanded; only explicitly listed methods are checked.
//   - Struct field → interface mapping is global: if two structs have a field with
//     the same name but different types, the last one wins. In practice this is rare
//     because the dominant pattern is a single Handler struct with named dependencies.
func CheckStoreInterface(root string) Result {
	files, fset, err := parseGoFiles(root)
	if err != nil {
		return Result{
			Name:   "store",
			Passed: false,
			Findings: []Finding{{
				Severity: SeverityError,
				Message:  fmt.Sprintf("failed to parse Go files: %v", err),
			}},
		}
	}

	interfaces := extractInterfaces(fset, files, root)
	fieldTypes := extractStructFieldTypes(files)
	calls := extractFieldMethodCalls(fset, files, root)

	// Build interface method sets indexed by interface name.
	ifaceMethods := make(map[string]map[string]InterfaceMethod)
	for _, im := range interfaces {
		if ifaceMethods[im.Interface] == nil {
			ifaceMethods[im.Interface] = make(map[string]InterfaceMethod)
		}
		ifaceMethods[im.Interface][im.Method] = im
	}

	// Map struct field names to the interface names they might implement.
	// A field like Store store.Store → field "Store", type name "Store".
	fieldToIface := make(map[string]string)
	for fieldName, typeName := range fieldTypes {
		if _, ok := ifaceMethods[typeName]; ok {
			fieldToIface[fieldName] = typeName
		}
	}

	if len(fieldToIface) == 0 {
		return Result{
			Name:    "store",
			Passed:  true,
			Summary: fmt.Sprintf("%d interfaces, 0 matched struct fields", len(ifaceMethods)),
		}
	}

	var findings []Finding
	calledMethods := make(map[string]map[string]bool) // iface → method → called

	for _, c := range calls {
		ifaceName, ok := fieldToIface[c.Field]
		if !ok {
			continue
		}
		methods := ifaceMethods[ifaceName]
		if calledMethods[ifaceName] == nil {
			calledMethods[ifaceName] = make(map[string]bool)
		}
		calledMethods[ifaceName][c.Method] = true

		if _, exists := methods[c.Method]; !exists {
			findings = append(findings, Finding{
				Severity: SeverityError,
				File:     c.File,
				Line:     c.Line,
				Message:  fmt.Sprintf("calls %s.%s, but %q is not a method on the %s interface", c.Field, c.Method, c.Method, ifaceName),
			})
		}
	}

	for ifaceName, methods := range ifaceMethods {
		for methodName, im := range methods {
			if calledMethods[ifaceName] != nil && calledMethods[ifaceName][methodName] {
				continue
			}
			findings = append(findings, Finding{
				Severity: SeverityWarning,
				File:     im.File,
				Line:     im.Line,
				Message:  fmt.Sprintf("interface method %s.%s is declared but never called", ifaceName, methodName),
			})
		}
	}

	errors := 0
	for _, f := range findings {
		if f.Severity == SeverityError {
			errors++
		}
	}

	totalMethods := 0
	for _, methods := range ifaceMethods {
		totalMethods += len(methods)
	}

	return Result{
		Name:     "store",
		Passed:   errors == 0,
		Summary:  fmt.Sprintf("%d interface methods, %d field calls", totalMethods, len(calls)),
		Findings: findings,
	}
}

// extractInterfaces finds all interface type declarations and their methods.
func extractInterfaces(fset *token.FileSet, files []*ast.File, root string) []InterfaceMethod {
	var methods []InterfaceMethod

	for _, f := range files {
		for _, decl := range f.Decls {
			genDecl, ok := decl.(*ast.GenDecl)
			if !ok || genDecl.Tok != token.TYPE {
				continue
			}
			for _, spec := range genDecl.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				iface, ok := typeSpec.Type.(*ast.InterfaceType)
				if !ok || iface.Methods == nil {
					continue
				}
				ifaceName := typeSpec.Name.Name
				for _, method := range iface.Methods.List {
					if len(method.Names) == 0 {
						continue // embedded interface
					}
					for _, name := range method.Names {
						pos := fset.Position(name.Pos())
						relPath, relErr := filepath.Rel(root, pos.Filename)
						if relErr != nil {
							relPath = pos.Filename
						}
						methods = append(methods, InterfaceMethod{
							Interface: ifaceName,
							Method:    name.Name,
							File:      relPath,
							Line:      pos.Line,
						})
					}
				}
			}
		}
	}

	return methods
}

// extractStructFieldTypes builds a map from struct field names to their type
// names (the final component of the type, stripping package qualifiers and
// pointer indirection).
func extractStructFieldTypes(files []*ast.File) map[string]string {
	fields := make(map[string]string)

	for _, f := range files {
		for _, decl := range f.Decls {
			genDecl, ok := decl.(*ast.GenDecl)
			if !ok || genDecl.Tok != token.TYPE {
				continue
			}
			for _, spec := range genDecl.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				structType, ok := typeSpec.Type.(*ast.StructType)
				if !ok || structType.Fields == nil {
					continue
				}
				for _, field := range structType.Fields.List {
					typeName := typeExprName(field.Type)
					if typeName == "" {
						continue
					}
					for _, name := range field.Names {
						fields[name.Name] = typeName
					}
				}
			}
		}
	}

	return fields
}

// typeExprName extracts the final type name from an expression, stripping
// pointers and package qualifiers. Returns "" for complex types.
func typeExprName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return e.Sel.Name
	case *ast.StarExpr:
		return typeExprName(e.X)
	}
	return ""
}

// extractFieldMethodCalls finds all call expressions of the form
// receiver.field.method(args...) — a two-level selector call.
func extractFieldMethodCalls(fset *token.FileSet, files []*ast.File, root string) []StoreCall {
	var calls []StoreCall

	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}

			// Must be a selector: receiver.field.method(...)
			outerSel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			methodName := outerSel.Sel.Name

			// The X of the outer selector must itself be a selector: receiver.field
			innerSel, ok := outerSel.X.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			fieldName := innerSel.Sel.Name

			pos := fset.Position(call.Pos())
			relPath, relErr := filepath.Rel(root, pos.Filename)
			if relErr != nil {
				relPath = pos.Filename
			}

			calls = append(calls, StoreCall{
				Field:  fieldName,
				Method: methodName,
				File:   relPath,
				Line:   pos.Line,
			})
			return true
		})
	}

	return calls
}
