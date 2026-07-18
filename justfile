# regit task runner — run `just` (or `just --list`) to see all recipes.
# Requires: just (https://github.com/casey/just) and the Go toolchain.

# Binary name and output location.
binary := "regit"
bin_dir := "dist"
coverage_file := "coverage.out"

# Minimum total statement coverage; `just cover` and `just ci` fail below it.
cover_min := "80"

# Version stamped into builds: latest tag + dirty/commit marker, or "dev"
# outside any tag (e.g. a fresh clone with no tags).
version := `git describe --tags --dirty --always 2>/dev/null || echo dev`

# The OS/arch matrix build-all cross-compiles for.
platforms := "linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64"

# Show the list of available recipes when `just` is run with no arguments.
default:
    @just --list --unsorted

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
version:
    @echo {{version}}

# A bare `go install` would leave `regit --version` reporting "dev"; this stamps
# the same version a `just build` would.
# Install the version-stamped binary into $GOBIN (or $GOPATH/bin).
install:
    go install -ldflags "-X main.version={{version}}" ./cmd/regit

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

# Show per-function coverage and FAIL if the total is below cover_min.
cover: test-race
    #!/usr/bin/env bash
    set -euo pipefail
    go tool cover -func={{coverage_file}}
    total="$(go tool cover -func={{coverage_file}} | tail -n 1 | awk '{print $3}' | tr -d '%')"
    echo "total coverage: ${total}% (floor {{cover_min}}%)"
    awk -v t="$total" -v m={{cover_min}} 'BEGIN { if (t+0 < m+0) { print "coverage below floor"; exit 1 } }'

# Open an HTML coverage report in the browser.
cover-html: test-race
    go tool cover -html={{coverage_file}}

# Format all tracked Go sources in place (gitignored trees, e.g. agent
# worktrees under .claude/, are deliberately excluded).
fmt:
    git ls-files '*.go' | xargs gofmt -w

# Fail if any tracked Go source is not gofmt-formatted (used by CI).
fmt-check:
    #!/usr/bin/env bash
    set -euo pipefail
    unformatted="$(git ls-files '*.go' | xargs gofmt -l)"
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

# GitHub CI calls this exact recipe, so the two cannot disagree; the coverage
# gate lives in `cover` (which reruns test-race).
# Full local CI pipeline: fmt-check + vet + build + race tests + coverage gate.
ci: fmt-check vet build cover

# Covers exit codes, preview vs --execute, undo, and --repo — the main/dispatch
# wiring unit tests cannot reach.
# Run the end-to-end tests against the built binary.
e2e:
    go test ./cmd/regit -run 'TestE2E' -count=1 -v

# Usage: just fuzz  ·  just fuzz 2m
# Run every fuzz target for the given time each (default a short burst).
fuzz time="20s":
    #!/usr/bin/env bash
    set -euo pipefail
    go test ./internal/capability -fuzz='^FuzzRenderParseRoundTrip$' -fuzztime={{time}}
    go test ./internal/gitio      -fuzz='^FuzzParseDiff$'            -fuzztime={{time}}
    go test ./internal/gitio      -fuzz='^FuzzBuildMessage$'         -fuzztime={{time}}
    go test ./internal/gitio      -fuzz='^FuzzRedactLines$'          -fuzztime={{time}}
    go test ./internal/docs       -fuzz='^FuzzSpliceBlocks$'         -fuzztime={{time}}

# Full pre-push battery: CI gates + end-to-end + docs drift + a fuzz burst.
check: ci e2e docs-check (fuzz "10s")

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

# Regenerate the generated doc blocks in README.md / AGENTS.md from the code.
docs-sync:
    go run ./cmd/docgen

# Verify the generated doc blocks match the code (the drift test runs this same check).
docs-check:
    go run ./cmd/docgen --check

# --- Versioning & release ----------------------------------------------------

# Validates X.Y.Z, requires a clean tree on main, runs the full CI gates,
# creates the annotated tag, then PRINTS the publish commands — never pushes.
# Tag release vX.Y.Z after the CI gates pass (usage: just release 1.2.3).
release ver:
    #!/usr/bin/env bash
    set -euo pipefail
    ver="{{ver}}"
    if ! [[ "$ver" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
        echo "release: version must be X.Y.Z (got '$ver')" >&2; exit 1
    fi
    if [ -n "$(git status --porcelain)" ]; then
        echo "release: working tree is not clean" >&2; exit 1
    fi
    branch="$(git rev-parse --abbrev-ref HEAD)"
    if [ "$branch" != "main" ]; then
        echo "release: must be on main (on '$branch')" >&2; exit 1
    fi
    just ci
    git tag -a "v$ver" -m "regit v$ver"
    echo
    echo "tagged v$ver — publish with:"
    echo "  git push origin v$ver"
    if git remote get-url origin 2>/dev/null | grep -q github.com && command -v gh >/dev/null; then
        echo "  gh release create v$ver --generate-notes"
    fi

# Derives the next version from the latest vX.Y.Z tag and runs the same gated
# release flow.
# Bump the version by one semver level (usage: just bump patch|minor|major).
bump level:
    #!/usr/bin/env bash
    set -euo pipefail
    level="{{level}}"
    case "$level" in patch|minor|major) ;; *)
        echo "bump: level must be patch, minor, or major (got '$level')" >&2; exit 1 ;;
    esac
    latest="$(git describe --tags --abbrev=0 --match 'v[0-9]*' 2>/dev/null || echo v0.0.0)"
    ver="${latest#v}"
    IFS=. read -r major minor patch <<<"$ver"
    case "$level" in
        major) major=$((major+1)); minor=0; patch=0 ;;
        minor) minor=$((minor+1)); patch=0 ;;
        patch) patch=$((patch+1)) ;;
    esac
    next="$major.$minor.$patch"
    echo "bumping $latest -> v$next"
    just release "$next"

# Remove build and coverage artifacts.
clean:
    rm -rf {{bin_dir}} {{coverage_file}}
