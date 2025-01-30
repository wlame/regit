package reword

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestBuildScanMessages_hashOnlyNoDiff asserts that the scan prompt references
// the commit hash, does NOT embed a diff, and that the system prompt instructs
// read-only inspection.
func TestBuildScanMessages_hashOnlyNoDiff(t *testing.T) {
	system, user := BuildScanMessages("abc123", "regit", "focus on API", 3)
	if !strings.Contains(user, "abc123") {
		t.Fatalf("scan prompt must reference the commit hash:\n%s", user)
	}
	if strings.Contains(user, "@@") || strings.Contains(user, "diff --git") {
		t.Fatalf("scan prompt must NOT embed a diff (the agent fetches it):\n%s", user)
	}
	if !strings.Contains(strings.ToLower(system), "read") { // read-only instruction
		t.Fatalf("system prompt should instruct read-only inspection:\n%s", system)
	}
}

// TestBuildScanMessages_includesRepoAndHint asserts that the scan user prompt
// includes the repo name, user hint, and variant count.
func TestBuildScanMessages_includesRepoAndHint(t *testing.T) {
	system, user := BuildScanMessages("deadbeef", "myrepo", "mention the why", 2)
	_ = system
	for _, want := range []string{"deadbeef", "myrepo", "mention the why", "2"} {
		if !strings.Contains(user, want) {
			t.Fatalf("user prompt missing %q:\n%s", want, user)
		}
	}
}

// recordingBackend records the system and user prompts it receives, for use in
// engine scan-mode tests.
type recordingBackend struct {
	system string
	user   string
	raw    string
}

func (r *recordingBackend) Name() string { return "recording" }
func (r *recordingBackend) Generate(_ context.Context, system, user string, _ []byte, validate func(string) error) (string, error) {
	r.system = system
	r.user = user
	if err := validate(r.raw); err != nil {
		return "", err
	}
	return r.raw, nil
}

// TestEngine_scanModeUsesHashNotDiff verifies that when opts.AgentScan is true,
// the engine sends the commit hash (not an embedded diff) to the backend, and
// does NOT call DiffText (i.e. the parentTree arg can be empty without failure).
func TestEngine_scanModeUsesHashNotDiff(t *testing.T) {
	repo, _, child := rewordRepo(t)
	rb := &recordingBackend{raw: `{"variants":[{"title":"Scan title","body":"why"}]}`}
	eng := NewEngine(repo, rb, NewCache(t.TempDir()))

	opts := Options{
		Variants:     1,
		ContextLines: 3,
		AgentScan:    true,
		Now:          time.Unix(0, 0),
	}

	// In scan mode, parentTree is not used (diff is not gathered), so we can
	// pass an empty string. The engine must not return an error from DiffText.
	r, err := eng.Generate(context.Background(), child, "" /* parentTree unused */, "", opts)
	if err != nil {
		t.Fatalf("Generate in scan mode: %v", err)
	}
	if len(r.Variants) != 1 || r.Variants[0].Title != "Scan title" {
		t.Fatalf("variants = %+v", r.Variants)
	}
	if r.CommitHash != child.Hash {
		t.Fatalf("CommitHash = %q, want %q", r.CommitHash, child.Hash)
	}

	// The user prompt must reference the commit hash, not a diff.
	if !strings.Contains(rb.user, child.Hash) {
		t.Fatalf("scan user prompt must contain the commit hash %q;\ngot:\n%s", child.Hash, rb.user)
	}
	if strings.Contains(rb.user, "@@") || strings.Contains(rb.user, "diff --git") {
		t.Fatalf("scan user prompt must NOT embed a diff;\ngot:\n%s", rb.user)
	}
}

// TestEngine_scanModeCachesOnHash verifies that scan-mode results are cached by
// hash (not by diff), so a second call returns a cache hit.
func TestEngine_scanModeCachesOnHash(t *testing.T) {
	repo, _, child := rewordRepo(t)
	rb := &recordingBackend{raw: `{"variants":[{"title":"Cached","body":""}]}`}
	cache := NewCache(t.TempDir())
	eng := NewEngine(repo, rb, cache)

	opts := Options{Variants: 1, AgentScan: true, Now: time.Unix(0, 0)}

	if _, err := eng.Generate(context.Background(), child, "", "", opts); err != nil {
		t.Fatal(err)
	}
	prevSystem := rb.system

	// Second call: must be a cache hit (backend not called again).
	rb.system = "" // clear to detect whether Generate() overwrites it
	if _, err := eng.Generate(context.Background(), child, "", "", opts); err != nil {
		t.Fatal(err)
	}
	// If cache hit, rb.system stays empty (backend.Generate was not called).
	if rb.system != "" {
		t.Fatalf("expected cache hit on second scan-mode call; backend was called again (system=%q prev=%q)", rb.system, prevSystem)
	}
}
