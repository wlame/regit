package docs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// malformedReadme carries a well-formed marker pair for every README block
// except "keys", whose begin marker has trailing text after "-->". Per spec,
// a malformed/orphan marker must FAIL LOUDLY rather than silently skip the
// block (as a bare strings.Contains presence check would allow).
const malformedReadme = `# Title

<!-- regit:begin keys --> trailing
stale
<!-- regit:end keys -->

<!-- regit:begin commands -->
<!-- regit:end commands -->

<!-- regit:begin strategies -->
<!-- regit:end strategies -->

<!-- regit:begin time-models -->
<!-- regit:end time-models -->

<!-- regit:begin env-vars -->
<!-- regit:end env-vars -->
`

// wellFormedAgents carries empty, well-formed marker pairs for every AGENTS.md
// block; it exists only so the fixture is honest (Check would still have to
// process it if Files() order ever changed), not because its content matters.
const wellFormedAgents = `# Agents

<!-- regit:begin verbs-overview -->
<!-- regit:end verbs-overview -->

<!-- regit:begin verbs-detail -->
<!-- regit:end verbs-detail -->

<!-- regit:begin runfiles-map -->
<!-- regit:end runfiles-map -->
`

func TestCheck_failsLoudlyOnMalformedMarker(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte(malformedReadme), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(wellFormedAgents), 0o644); err != nil {
		t.Fatal(err)
	}

	// Check may legitimately also want to report drift for the well-formed
	// files (their content doesn't match the live registry); we only assert
	// that the malformed "keys" marker in README.md fails loudly rather than
	// being silently skipped.
	_, err := Check(root)
	if err == nil {
		t.Fatal("Check returned no error for a malformed begin marker, want a loud failure")
	}
	for _, want := range []string{"README.md", `missing markers for block "keys"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to contain %q", err, want)
		}
	}
}

// TestSync_writesBlocksAndIsIdempotent exercises Sync's write path — the one
// cmd/docgen actually runs — against a synthetic root: seed both doc files
// with empty, well-formed marker pairs for every declared block, run Sync,
// and assert the rendered content lands on disk. A second Sync must be a
// no-op, proving the write path (not just SpliceBlocks) is idempotent.
func TestSync_writesBlocksAndIsIdempotent(t *testing.T) {
	root := t.TempDir()
	for _, f := range Files() {
		var b strings.Builder
		b.WriteString("# synthetic\n\n")
		for _, n := range f.Blocks {
			b.WriteString(BeginMarker(n) + "\n" + EndMarker(n) + "\n\n")
		}
		if err := os.WriteFile(filepath.Join(root, f.Path), []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	changed, err := Sync(root)
	if err != nil {
		t.Fatalf("first Sync: %v", err)
	}
	if len(changed) != len(Files()) {
		t.Errorf("first Sync changed %v, want both doc files", changed)
	}

	all := Blocks()
	for _, f := range Files() {
		raw, err := os.ReadFile(filepath.Join(root, f.Path))
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range f.Blocks {
			if !strings.Contains(string(raw), all[n]) {
				t.Errorf("%s: block %q content not written by Sync", f.Path, n)
			}
		}
	}

	changed, err = Sync(root)
	if err != nil {
		t.Fatalf("second Sync: %v", err)
	}
	if len(changed) != 0 {
		t.Errorf("second Sync changed %v, want none", changed)
	}
}

// TestSync_reportsReadErrorForMissingFile covers regenerate's read-error
// branch: Sync must surface os.ReadFile's error rather than swallowing it,
// and must not report any file as changed once it has failed.
func TestSync_reportsReadErrorForMissingFile(t *testing.T) {
	root := t.TempDir()
	// Deliberately do not create README.md or AGENTS.md.
	changed, err := Sync(root)
	if err == nil {
		t.Fatal("Sync returned no error for a missing doc file, want a loud failure")
	}
	if !strings.Contains(err.Error(), "README.md") {
		t.Errorf("err = %q, want it to mention README.md", err)
	}
	if len(changed) != 0 {
		t.Errorf("Sync reported changed = %v on failure, want none", changed)
	}
}
