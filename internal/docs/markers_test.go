package docs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const spliceDoc = `# Title

<!-- regit:begin alpha -->
stale content
<!-- regit:end alpha -->

prose between

<!-- regit:begin beta -->
<!-- regit:end beta -->
tail`

func TestSpliceBlocks_replacesContentBetweenMarkers(t *testing.T) {
	got, err := SpliceBlocks(spliceDoc, map[string]string{"alpha": "fresh A", "beta": "fresh B\nline two"})
	if err != nil {
		t.Fatalf("SpliceBlocks: %v", err)
	}
	for _, want := range []string{
		"<!-- regit:begin alpha -->\nfresh A\n<!-- regit:end alpha -->",
		"<!-- regit:begin beta -->\nfresh B\nline two\n<!-- regit:end beta -->",
		"# Title", "prose between", "tail",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("result missing %q\nfull result:\n%s", want, got)
		}
	}
	if strings.Contains(got, "stale content") {
		t.Errorf("stale content survived the splice:\n%s", got)
	}
}

func TestSpliceBlocks_isIdempotent(t *testing.T) {
	blocks := map[string]string{"alpha": "A", "beta": "B"}
	once, err := SpliceBlocks(spliceDoc, blocks)
	if err != nil {
		t.Fatalf("first splice: %v", err)
	}
	twice, err := SpliceBlocks(once, blocks)
	if err != nil {
		t.Fatalf("second splice: %v", err)
	}
	if once != twice {
		t.Errorf("splice is not idempotent:\nfirst:\n%s\nsecond:\n%s", once, twice)
	}
}

func TestSpliceBlocks_errorPaths(t *testing.T) {
	cases := []struct {
		name   string
		src    string
		blocks map[string]string
		errHas string
	}{
		{"unknown block", "<!-- regit:begin nope -->\n<!-- regit:end nope -->", map[string]string{}, "unknown block"},
		{"begin without end", "<!-- regit:begin a -->\ntext", map[string]string{"a": "x"}, "without an end"},
		{"end without begin", "text\n<!-- regit:end a -->", map[string]string{"a": "x"}, "without a begin"},
		{"duplicate begin", "<!-- regit:begin a -->\n<!-- regit:end a -->\n<!-- regit:begin a -->\n<!-- regit:end a -->", map[string]string{"a": "x"}, "duplicate"},
		{"nested begin", "<!-- regit:begin a -->\n<!-- regit:begin b -->\n<!-- regit:end a -->", map[string]string{"a": "x", "b": "y"}, "nested"},
		{"mismatched end", "<!-- regit:begin a -->\n<!-- regit:end b -->", map[string]string{"a": "x", "b": "y"}, "closes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := SpliceBlocks(tc.src, tc.blocks)
			if err == nil || !strings.Contains(err.Error(), tc.errHas) {
				t.Fatalf("err = %v, want containing %q", err, tc.errHas)
			}
		})
	}
}

func TestFindRoot_walksUpToGoMod(t *testing.T) {
	// t.TempDir gives an isolated tree; plant a go.mod and search from a
	// nested subdirectory two levels down.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := FindRoot(nested)
	if err != nil {
		t.Fatalf("FindRoot: %v", err)
	}
	// Resolve symlinks before comparing: on macOS TempDir returns /var/…
	// while walking can yield /private/var/….
	wantR, _ := filepath.EvalSymlinks(root)
	gotR, _ := filepath.EvalSymlinks(got)
	if gotR != wantR {
		t.Errorf("FindRoot = %q, want %q", gotR, wantR)
	}
}

func TestFindRoot_errorsWhenNoGoModAbove(t *testing.T) {
	tmp := t.TempDir()
	got, err := FindRoot(tmp)
	if err == nil {
		// Some environments carry a go.mod above the temp tree; the error
		// path is genuinely unreachable there — skip rather than false-fail.
		t.Skipf("go.mod found above %s (at %s); error path untestable in this environment", tmp, got)
	}
	if !strings.Contains(err.Error(), "no go.mod found above") {
		t.Errorf("error = %q, want it to name the missing go.mod", err)
	}
}

// TestParseMarker_emptyNameDoesNotPanic pins the bounds guard. The prefix and
// the suffix OVERLAP when a marker carries no name: "<!-- regit:end -->" is 18
// bytes, but len(endPrefix)+len(markerSuffix) is 19, so slicing between them
// inverts the indices. Both marker kinds are affected. Found by FuzzSpliceBlocks.
func TestParseMarker_emptyNameDoesNotPanic(t *testing.T) {
	for _, tc := range []struct{ line, prefix string }{
		{"<!-- regit:end -->", endPrefix},
		{"<!-- regit:begin -->", beginPrefix},
	} {
		name, ok := parseMarker(tc.line, tc.prefix)
		if ok {
			t.Errorf("parseMarker(%q) = (%q, true), want ok=false — a nameless marker is not a marker",
				tc.line, name)
		}
	}
}
