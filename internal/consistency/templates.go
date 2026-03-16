package consistency

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// TemplateURL represents an HTTP URL extracted from an HTML template.
type TemplateURL struct {
	Method string // HTTP method (POST, GET, etc.), empty if unknown (e.g., form action without method)
	RawURL string // URL as written in the template, including Go template expressions
	File   string
	Line   int
}

var (
	hxAttrRe     = regexp.MustCompile(`hx-(post|get|put|delete|patch)\s*=\s*["']([^"']+)["']`)
	formActionRe = regexp.MustCompile(`action\s*=\s*["']([^"']+)["']`)
	formMethodRe = regexp.MustCompile(`(?i)method\s*=\s*["']?(GET|POST|PUT|DELETE|PATCH)["']?`)
	tmplExprRe   = regexp.MustCompile(`\{\{.*?\}\}`)
	routeParamRe = regexp.MustCompile(`\{[^}]+\}`)
)

// CheckTemplateRoutes verifies that every URL in HTML templates (hx-post,
// hx-get, action=, etc.) matches a registered route in the Go source.
func CheckTemplateRoutes(root string) Result {
	goFiles, fset, err := parseGoFiles(root)
	if err != nil {
		return Result{
			Name:   "templates",
			Passed: false,
			Findings: []Finding{{
				Severity: SeverityError,
				Message:  fmt.Sprintf("failed to parse Go files: %v", err),
			}},
		}
	}

	routes := extractRoutes(fset, goFiles, root)
	templateURLs, scanErr := extractTemplateURLs(root)
	if scanErr != nil {
		return Result{
			Name:   "templates",
			Passed: false,
			Findings: []Finding{{
				Severity: SeverityError,
				Message:  fmt.Sprintf("failed to scan templates: %v", scanErr),
			}},
		}
	}

	type normalizedRoute struct {
		Method  string
		Pattern string
	}
	routeSet := make(map[normalizedRoute]bool)
	for _, r := range routes {
		method, pattern := SplitMethodFromPattern(r.Pattern)
		norm := NormalizeRoutePattern(pattern)
		routeSet[normalizedRoute{Method: strings.ToUpper(method), Pattern: norm}] = true
	}

	var findings []Finding
	for _, tu := range templateURLs {
		normURL := NormalizeTemplateURL(tu.RawURL)

		matched := false
		if tu.Method != "" {
			if routeSet[normalizedRoute{Method: strings.ToUpper(tu.Method), Pattern: normURL}] {
				matched = true
			}
		} else {
			for nr := range routeSet {
				if nr.Pattern == normURL {
					matched = true
					break
				}
			}
		}

		if !matched {
			attr := "hx-" + strings.ToLower(tu.Method)
			if tu.Method == "" {
				attr = "action"
			}
			findings = append(findings, Finding{
				Severity: SeverityError,
				File:     tu.File,
				Line:     tu.Line,
				Message:  fmt.Sprintf(`%s="%s" does not match any registered route`, attr, tu.RawURL),
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
		Name:     "templates",
		Passed:   errors == 0,
		Summary:  fmt.Sprintf("%d template URLs, %d routes", len(templateURLs), len(routes)),
		Findings: findings,
	}
}

// extractTemplateURLs walks root looking for HTML template files and extracts
// all hx-* and form action URLs.
func extractTemplateURLs(root string) ([]TemplateURL, error) {
	var urls []TemplateURL

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

		f, err := os.Open(path)
		if err != nil {
			return nil // skip unreadable files; go build will surface real issues
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

			for _, match := range hxAttrRe.FindAllStringSubmatch(line, -1) {
				url := match[2]
				if !strings.HasPrefix(url, "/") {
					continue
				}
				urls = append(urls, TemplateURL{
					Method: strings.ToUpper(match[1]),
					RawURL: url,
					File:   relPath,
					Line:   lineNum,
				})
			}

			for _, match := range formActionRe.FindAllStringSubmatch(line, -1) {
				url := match[1]
				if !strings.HasPrefix(url, "/") {
					continue
				}
				method := ""
				if methodMatch := formMethodRe.FindStringSubmatch(line); len(methodMatch) > 1 {
					method = strings.ToUpper(methodMatch[1])
				}
				urls = append(urls, TemplateURL{
					Method: method,
					RawURL: url,
					File:   relPath,
					Line:   lineNum,
				})
			}
		}

		return scanner.Err()
	})

	return urls, err
}

// SplitMethodFromPattern separates "POST /path" into method and path.
// Returns ("", pattern) if no method prefix is present.
func SplitMethodFromPattern(pattern string) (method, path string) {
	if idx := strings.IndexByte(pattern, ' '); idx >= 0 {
		return pattern[:idx], pattern[idx+1:]
	}
	return "", pattern
}

// NormalizeRoutePattern converts a Go 1.22+ route pattern into a normalized
// form for comparison with template URLs. It strips the trailing {$} (exact
// match) and replaces path parameters like {teamId} with {_}.
func NormalizeRoutePattern(pattern string) string {
	pattern = strings.TrimSuffix(pattern, "{$}")
	if pattern == "" {
		pattern = "/"
	}
	return routeParamRe.ReplaceAllString(pattern, "{_}")
}

// NormalizeTemplateURL replaces Go template expressions (e.g., {{.TeamID}})
// with {_} so the URL can be compared against normalized route patterns.
// Query strings are stripped since Go route patterns are path-only.
func NormalizeTemplateURL(url string) string {
	path, _, _ := strings.Cut(url, "?")
	return tmplExprRe.ReplaceAllString(path, "{_}")
}
