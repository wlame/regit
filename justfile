# regit task runner — run `just` (or `just --list`) to see all recipes.
# Requires: just (https://github.com/casey/just) and the Go toolchain.

# Binary name and output location.
binary := "regit"
bin_dir := "dist"
coverage_file := "coverage.out"

# Show the list of available recipes when `just` is run with no arguments.
default:
    @just --list

# Compile the binary into ./{{bin_dir}}/{{binary}}.
build:
    go build -o {{bin_dir}}/{{binary}} ./cmd/regit

# Install the binary into $GOBIN (or $GOPATH/bin) so `regit` is on your PATH.
install:
    go install ./cmd/regit

# Run regit against a repository (defaults to the current directory).
# Usage: `just run` or `just run /path/to/repo`.
run repo=".":
    go run ./cmd/regit --repo={{repo}}

# Run the full test suite.
test:
    go test ./...

# Run tests with the race detector and write a coverage profile.
test-race:
    go test -race -coverprofile={{coverage_file}} ./...

# Show per-function coverage from the last profile (runs tests if needed).
cover: test-race
    go tool cover -func={{coverage_file}}

# Open an HTML coverage report in the browser.
cover-html: test-race
    go tool cover -html={{coverage_file}}

# Format all Go sources in place.
fmt:
    gofmt -w .

# Fail if any Go source is not gofmt-formatted (used by CI).
fmt-check:
    #!/usr/bin/env bash
    set -euo pipefail
    unformatted="$(gofmt -l .)"
    if [ -n "$unformatted" ]; then
        echo "These files are not gofmt-formatted:"
        echo "$unformatted"
        exit 1
    fi
    echo "all formatted"

# Run go vet across the module.
vet:
    go vet ./...

# Static checks: formatting + vet.
lint: fmt-check vet

# Sync go.mod / go.sum.
tidy:
    go mod tidy

# Download module dependencies.
deps:
    go mod download

# Full local CI pipeline, mirroring .github/workflows/ci.yml.
ci: fmt-check vet build test-race
    go tool cover -func={{coverage_file}} | tail -n 1

# Run the LLM-backed commit splitter against a commit (defaults to HEAD).
# Usage: just split <commit-ish> -- --llm=ollama/qwen2.5-coder --dry-run
split commit="HEAD" *args:
    go run ./cmd/regit split {{commit}} {{args}}

# Reword commit messages with the LLM (defaults to HEAD).
# Usage: just reword <commit-ish> -- --llm=ollama/qwen2.5-coder --dry-run
reword commit="HEAD" *args:
    go run ./cmd/regit reword {{commit}} {{args}}

# Remove build and coverage artifacts.
clean:
    rm -rf {{bin_dir}} {{coverage_file}}
