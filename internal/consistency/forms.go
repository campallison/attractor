package consistency

import (
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// FormField represents a single named input element inside an HTML form.
type FormField struct {
	Name string // from name="..." attribute
	Type string // from type="..." attribute; defaults to "text"
}

// HTMLForm represents an HTML <form> extracted from a template file,
// including the input fields scoped to that form.
type HTMLForm struct {
	Method string // HTTP method (POST, GET, etc.); defaults to "GET"
	Action string // URL path with Go template expressions replaced with {_}
	Fields []FormField
	File   string
}

// tmplExprRe is defined in templates.go and reused here.

// ExtractForms walks the file tree rooted at root, parses HTML template files,
// and returns all <form> elements with their child input fields.
func ExtractForms(root string) ([]HTMLForm, error) {
	var forms []HTMLForm

	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
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

		raw, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		relPath, relErr := filepath.Rel(root, path)
		if relErr != nil {
			relPath = path
		}

		fileForms := parseFormsFromHTML(string(raw), relPath)
		forms = append(forms, fileForms...)
		return nil
	})

	return forms, err
}

// parseFormsFromHTML strips Go template expressions from src, then uses the
// html tokenizer to extract forms and their input fields.
func parseFormsFromHTML(src, file string) []HTMLForm {
	cleaned := tmplExprRe.ReplaceAllString(src, "{_}")

	var forms []HTMLForm
	var current *HTMLForm
	depth := 0

	z := html.NewTokenizer(strings.NewReader(cleaned))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			if z.Err() == io.EOF {
				break
			}
			break
		}

		switch tt {
		case html.StartTagToken:
			tn, hasAttr := z.TagName()
			tag := atom.Lookup(tn)

			if tag == atom.Form {
				method, action := "GET", ""
				if hasAttr {
					method, action = readFormAttrs(z)
				}
				current = &HTMLForm{
					Method: method,
					Action: action,
					File:   file,
				}
				depth = 1
				continue
			}

			if current != nil && depth > 0 {
				if isInputElement(tag) && hasAttr {
					if f, ok := readInputAttrs(z); ok {
						current.Fields = append(current.Fields, f)
					}
				}
			}

		case html.EndTagToken:
			tn, _ := z.TagName()
			tag := atom.Lookup(tn)
			if tag == atom.Form && current != nil {
				depth--
				if depth <= 0 {
					forms = append(forms, *current)
					current = nil
					depth = 0
				}
			}

		case html.SelfClosingTagToken:
			if current != nil && depth > 0 {
				tn, hasAttr := z.TagName()
				tag := atom.Lookup(tn)
				if isInputElement(tag) && hasAttr {
					if f, ok := readInputAttrs(z); ok {
						current.Fields = append(current.Fields, f)
					}
				}
			}
		}
	}

	return forms
}

func isInputElement(tag atom.Atom) bool {
	return tag == atom.Input || tag == atom.Select || tag == atom.Textarea
}

// readFormAttrs reads method and action attributes from the current token.
func readFormAttrs(z *html.Tokenizer) (method, action string) {
	method = "GET"
	for {
		key, val, more := z.TagAttr()
		k := strings.ToLower(string(key))
		v := string(val)
		switch k {
		case "method":
			method = strings.ToUpper(strings.TrimSpace(v))
		case "action":
			action = strings.TrimSpace(v)
		}
		if !more {
			break
		}
	}
	return method, action
}

// readInputAttrs reads name and type attributes from an input/select/textarea.
// Returns the FormField and true if a name attribute was found.
func readInputAttrs(z *html.Tokenizer) (FormField, bool) {
	f := FormField{Type: "text"}
	hasName := false
	for {
		key, val, more := z.TagAttr()
		k := strings.ToLower(string(key))
		v := string(val)
		switch k {
		case "name":
			f.Name = v
			hasName = true
		case "type":
			f.Type = strings.ToLower(strings.TrimSpace(v))
		}
		if !more {
			break
		}
	}
	return f, hasName
}
