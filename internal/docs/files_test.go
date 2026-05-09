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
