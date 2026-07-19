package docs

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/wlame/regit/internal/capability"
	"github.com/wlame/regit/internal/config"
	"github.com/wlame/regit/internal/gitio"
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

// yamlKeys returns every yaml tag reachable from t, recursing into struct
// fields, so the README config test covers nested llm./split./reword. keys.
func yamlKeys(t reflect.Type) []string {
	var keys []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := strings.Split(f.Tag.Get("yaml"), ",")[0]
		if tag == "" || tag == "-" {
			continue
		}
		keys = append(keys, tag)
		if f.Type.Kind() == reflect.Struct {
			keys = append(keys, yamlKeys(f.Type)...)
		}
	}
	return keys
}

// TestREADME_documentsEveryConfigKey catches the failure mode that let the
// llm:/split: blocks go undocumented: any yaml-tagged Config field must appear
// as a "key:" somewhere in the README. The match is anchored to the start of a
// (possibly indented/list-prefixed) YAML line so a key that is a suffix of
// another key — e.g. "model" inside "default_time_model" — can't false-pass.
func TestREADME_documentsEveryConfigKey(t *testing.T) {
	root := driftRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range yamlKeys(reflect.TypeOf(config.Config{})) {
		pattern := regexp.MustCompile(`(?m)^[ \t-]*` + regexp.QuoteMeta(key) + `:`)
		if !pattern.Match(raw) {
			t.Errorf("README.md does not document config key %q", key)
		}
	}
}

// TestAGENTS_mentionsEveryInternalPackage keeps the AGENTS.md package map
// complete as packages are added. (The README deliberately carries no package
// map — the codebase walkthrough lives in AGENTS.md.)
func TestAGENTS_mentionsEveryInternalPackage(t *testing.T) {
	root := driftRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "internal"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() && !strings.Contains(string(raw), "internal/"+e.Name()) {
			t.Errorf("AGENTS.md does not mention internal/%s", e.Name())
		}
	}
}

// TestREADME_showsBackupRefPattern binds the safety table to the real backup
// ref shape (gitio.BackupRefDocPattern is itself test-locked to BackupRefName).
func TestREADME_showsBackupRefPattern(t *testing.T) {
	root := driftRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), gitio.BackupRefDocPattern) {
		t.Errorf("README.md does not show the backup ref pattern %q", gitio.BackupRefDocPattern)
	}
}

// TestRunFiles_existForEveryVerb keeps the generated runfiles-map line honest:
// every verb must really have its run_<flat>.go implementation file.
func TestRunFiles_existForEveryVerb(t *testing.T) {
	root := driftRoot(t)
	for _, c := range capability.Registry() {
		p := filepath.Join(root, "internal", "capability", "run_"+strings.ReplaceAll(c.Name, "-", "")+".go")
		if _, err := os.Stat(p); err != nil {
			t.Errorf("verb %q has no run file at %s", c.Name, p)
		}
	}
}
