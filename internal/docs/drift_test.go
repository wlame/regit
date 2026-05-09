package docs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// driftRoot locates the repo root for doc-drift assertions, skipping when the
// docs are absent (e.g. tests running from an extracted module zip) — the
// tests must never false-pass, but they may honestly not apply.
func driftRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	root, err := FindRoot(wd)
	if err != nil {
		t.Skipf("no repo root found: %v", err)
	}
	for _, f := range Files() {
		if _, err := os.Stat(filepath.Join(root, f.Path)); err != nil {
			t.Skipf("%s not present at %s: %v", f.Path, root, err)
		}
	}
	return root
}

// TestDocs_generatedBlocksMatchCode is the drift gate: any difference between
// the docs' generated blocks and the code's rendering fails the suite.
func TestDocs_generatedBlocksMatchCode(t *testing.T) {
	root := driftRoot(t)
	drifted, err := Check(root)
	if err != nil {
		t.Fatalf("docs check: %v", err)
	}
	if len(drifted) > 0 {
		t.Fatalf("docs drifted from code in %v — run 'just docs-sync'", drifted)
	}
}

// TestDocs_noOrphanMarkers ensures every marker in the files is declared in
// Files(): a typo'd or leftover marker must fail loudly, not be skipped.
func TestDocs_noOrphanMarkers(t *testing.T) {
	root := driftRoot(t)
	for _, f := range Files() {
		declared := map[string]bool{}
		for _, n := range f.Blocks {
			declared[n] = true
		}
		raw, err := os.ReadFile(filepath.Join(root, f.Path))
		if err != nil {
			t.Fatalf("read %s: %v", f.Path, err)
		}
		for _, line := range strings.Split(string(raw), "\n") {
			if name, ok := parseMarker(strings.TrimSpace(line), beginPrefix); ok && !declared[name] {
				t.Errorf("%s: marker %q is not declared in Files()", f.Path, name)
			}
		}
	}
}

// TestDocs_onDiskSpliceIsIdempotent guards the whole pipeline end to end: the
// real files, spliced with the real blocks, must be a fixed point.
func TestDocs_onDiskSpliceIsIdempotent(t *testing.T) {
	root := driftRoot(t)
	all := Blocks()
	for _, f := range Files() {
		raw, err := os.ReadFile(filepath.Join(root, f.Path))
		if err != nil {
			t.Fatalf("read %s: %v", f.Path, err)
		}
		once, err := SpliceBlocks(string(raw), fileBlocks(f, all))
		if err != nil {
			t.Fatalf("splice %s: %v", f.Path, err)
		}
		twice, err := SpliceBlocks(once, fileBlocks(f, all))
		if err != nil {
			t.Fatalf("re-splice %s: %v", f.Path, err)
		}
		if once != twice {
			t.Errorf("%s: splice is not a fixed point", f.Path)
		}
	}
}
