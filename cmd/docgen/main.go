// Command docgen regenerates the generated documentation blocks in README.md
// and AGENTS.md from the code — see internal/docs for what is generated and
// why. It is a development tool, never shipped to users (only cmd/regit is).
//
// Usage:
//
//	go run ./cmd/docgen             # rewrite drifted files in place
//	go run ./cmd/docgen --check     # exit 1 if anything would change; write nothing
//	go run ./cmd/docgen --repo=DIR  # explicit repo root (default: nearest go.mod)
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/wlame/regit/internal/docs"
)

func main() {
	check := flag.Bool("check", false, "report drift without writing; exit 1 when found")
	repo := flag.String("repo", "", "repo root (default: nearest go.mod above the working directory)")
	flag.Parse()

	root := *repo
	if root == "" {
		wd, err := os.Getwd()
		if err != nil {
			fatal(err)
		}
		if root, err = docs.FindRoot(wd); err != nil {
			fatal(err)
		}
	}

	if *check {
		drifted, err := docs.Check(root)
		if err != nil {
			fatal(err)
		}
		if len(drifted) > 0 {
			fmt.Fprintf(os.Stderr, "docs drifted from code: %v — run 'just docs-sync'\n", drifted)
			os.Exit(1)
		}
		fmt.Println("docs in sync")
		return
	}

	changed, err := docs.Sync(root)
	if err != nil {
		fatal(err)
	}
	if len(changed) == 0 {
		fmt.Println("docs already in sync")
		return
	}
	fmt.Printf("regenerated: %v\n", changed)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "docgen:", err)
	os.Exit(1)
}
