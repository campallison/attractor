// check-consistency performs static analysis on a generated Go web application,
// verifying internal agreement among routes, handlers, store interfaces,
// templates, and database schema. Designed to run as a pipeline build gate.
//
// Usage:
//
//	check-consistency --root=/workspace [--checks=routes,templates,store]
//
// Exit code 0 if all checks pass, 1 if any check has errors.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/campallison/attractor/internal/consistency"
)

func main() {
	root := flag.String("root", ".", "root directory of the Go project to check")
	checks := flag.String("checks", "", "comma-separated list of checks to run (default: all)")
	flag.Parse()

	var names []string
	if *checks != "" {
		names = strings.Split(*checks, ",")
		for i := range names {
			names[i] = strings.TrimSpace(names[i])
		}
	}

	results := consistency.RunChecks(*root, names)

	for _, r := range results {
		fmt.Print(r.String())
	}

	if consistency.HasErrors(results) {
		os.Exit(1)
	}
}
