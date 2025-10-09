package reword

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestBuildScanMessages_includesDiffAndBrowseHint: scan mode must carry the diff
// itself (the agent has no git access anymore) plus the read-only browse hint.
func TestBuildScanMessages_includesDiffAndBrowseHint(t *testing.T) {
	req := Request{
		RepoName:   "myrepo",
		Diff:       "diff --git a/f.txt b/f.txt\n@@ -1 +1 @@\n-old\n+new\n",
		OldSubject: "old subject",
		Variants:   2,
		UserHint:   "focus on the why",
	}
	system, user := BuildScanMessages(req, "abc123")
	if !strings.Contains(user, "diff --git a/f.txt") {
		t.Errorf("scan user prompt must embed the diff; got:\n%s", user)
	}
	if !strings.Contains(user, "abc123") {
		t.Errorf("scan user prompt should name the commit hash; got:\n%s", user)
	}
	if !strings.Contains(system, "Read") || !strings.Contains(system, "read-only") {
		t.Errorf("scan system prompt should describe read-only file browsing; got:\n%s", system)
	}
	if strings.Contains(system, "git show") {
		t.Errorf("scan system prompt must no longer instruct running git; got:\n%s", system)
	}
}

// TestBuildScanMessages_includesRepoAndHint asserts that the scan user prompt
// includes the repo name, user hint, and variant count, and that the system
// prompt still carries the read-only browse permission.
func TestBuildScanMessages_includesRepoAndHint(t *testing.T) {
	req := Request{
		RepoName: "myrepo",
		Diff:     "diff --git a/f.txt b/f.txt\n@@ -1 +1 @@\n-old\n+new\n",
		UserHint: "mention the why",
		Variants: 2,
	}
	system, user := BuildScanMessages(req, "deadbeef")
	for _, want := range []string{"deadbeef", "myrepo", "mention the why", "2"} {
		if !strings.Contains(user, want) {
			t.Fatalf("user prompt missing %q:\n%s", want, user)
		}
	}
	if !strings.Contains(strings.ToLower(system), "read") {
		t.Fatalf("system prompt should mention read-only browsing:\n%s", system)
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

// TestEngine_scanModeEmbedsDiffAndHash verifies that when opts.AgentScan is
// true, the engine still gathers the diff via DiffText (H1: agent-scan no
// longer skips it, since the agent has no git access) and sends both the diff
// and the commit hash to the backend, alongside the read-only browse hint.
func TestEngine_scanModeEmbedsDiffAndHash(t *testing.T) {
	repo, parent, child := rewordRepo(t)
	rb := &recordingBackend{raw: `{"variants":[{"title":"Scan title","body":"why"}]}`}
	eng := NewEngine(repo, rb, NewCache(t.TempDir()))

	opts := Options{
		Variants:     1,
		ContextLines: 3,
		AgentScan:    true,
		Now:          time.Unix(0, 0),
	}

	r, err := eng.Generate(context.Background(), child, parent, "readme text", opts)
	if err != nil {
		t.Fatalf("Generate in scan mode: %v", err)
	}
	if len(r.Variants) != 1 || r.Variants[0].Title != "Scan title" {
		t.Fatalf("variants = %+v", r.Variants)
	}
	if r.CommitHash != child.Hash {
		t.Fatalf("CommitHash = %q, want %q", r.CommitHash, child.Hash)
	}

	// The user prompt must reference both the commit hash and the diff.
	if !strings.Contains(rb.user, child.Hash) {
		t.Fatalf("scan user prompt must contain the commit hash %q;\ngot:\n%s", child.Hash, rb.user)
	}
	if !strings.Contains(rb.user, "diff --git") {
		t.Fatalf("scan user prompt must embed the diff;\ngot:\n%s", rb.user)
	}
	// The system prompt must describe the read-only browse permission and must
	// no longer instruct the agent to run git itself.
	if !strings.Contains(rb.system, "Read") || !strings.Contains(rb.system, "read-only") {
		t.Fatalf("scan system prompt must describe read-only file browsing;\ngot:\n%s", rb.system)
	}
	if strings.Contains(rb.system, "git show") {
		t.Fatalf("scan system prompt must no longer instruct running git;\ngot:\n%s", rb.system)
	}
}

// TestEngine_scanModeCachesLikeNormalMode verifies that scan-mode results are
// cached using the same diff-based fingerprint as normal mode (H1: cache
// keying no longer differs by mode), so a second call with identical inputs
// is a cache hit.
func TestEngine_scanModeCachesLikeNormalMode(t *testing.T) {
	repo, parent, child := rewordRepo(t)
	rb := &recordingBackend{raw: `{"variants":[{"title":"Cached","body":""}]}`}
	cache := NewCache(t.TempDir())
	eng := NewEngine(repo, rb, cache)

	opts := Options{Variants: 1, ContextLines: 3, AgentScan: true, Now: time.Unix(0, 0)}

	if _, err := eng.Generate(context.Background(), child, parent, "readme text", opts); err != nil {
		t.Fatal(err)
	}
	prevSystem := rb.system

	// Second call: must be a cache hit (backend not called again).
	rb.system = "" // clear to detect whether Generate() overwrites it
	if _, err := eng.Generate(context.Background(), child, parent, "readme text", opts); err != nil {
		t.Fatal(err)
	}
	// If cache hit, rb.system stays empty (backend.Generate was not called).
	if rb.system != "" {
		t.Fatalf("expected cache hit on second scan-mode call; backend was called again (system=%q prev=%q)", rb.system, prevSystem)
	}
}
