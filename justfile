# regit task runner — run `just` (or `just --list`) to see all recipes.
# Requires: just (https://github.com/casey/just) and the Go toolchain.

# Binary name and output location.
binary := "regit"
bin_dir := "dist"
coverage_file := "coverage.out"

# Version stamped into builds: latest tag + dirty/commit marker, or "dev"
# outside any tag (e.g. a fresh clone with no tags).
version := `git describe --tags --dirty --always 2>/dev/null || echo dev`

# The OS/arch matrix build-all cross-compiles for.
platforms := "linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64"

# Show the list of available recipes when `just` is run with no arguments.
default:
    @just --list

# Compile the binary into ./{{bin_dir}}/{{binary}}, stamping the version.
build:
    go build -ldflags "-X main.version={{version}}" -o {{bin_dir}}/{{binary}} ./cmd/regit

# Cross-compile release binaries for every platform into ./{{bin_dir}}.
# Output: {{bin_dir}}/{{binary}}-<os>-<arch> ({{binary}}-windows-*.exe on Windows).
# Pure-Go build (no cgo), so every target cross-compiles without a C toolchain.
build-all:
    #!/usr/bin/env bash
    set -euo pipefail
    for p in {{platforms}}; do
        os="${p%/*}"; arch="${p#*/}"
        ext=""; [ "$os" = "windows" ] && ext=".exe"
        out="{{bin_dir}}/{{binary}}-${os}-${arch}${ext}"
        echo "building ${out}"
        GOOS="$os" GOARCH="$arch" go build -ldflags "-X main.version={{version}}" -o "$out" ./cmd/regit
    done
    echo "done — binaries in {{bin_dir}}/"

# Print the version a build would stamp.
print-version:
    @echo {{version}}

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

# Preview an even date spread on the current repo (dry-run).
redate *args:
    go run ./cmd/regit redate --strategy=Even-Spread --time=Working-hours {{args}}

# Preview a regex retitle (dry-run); pass --execute to apply.
retitle match replace *args:
    go run ./cmd/regit retitle --match={{match}} --replace={{replace}} {{args}}

# Print the agent-facing documentation.
llm-help:
    go run ./cmd/regit --llm-help

# List every registered command (uses the terse help projection).
commands:
    go run ./cmd/regit --help

# Remove build and coverage artifacts.
clean:
    rm -rf {{bin_dir}} {{coverage_file}}
