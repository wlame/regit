package capability

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/wlame/regit/internal/config"
	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/model"
)

// TestRunSplitSingleGroupNoPanic covers the guard that was dropped in an earlier
// refactor: when the LLM returns should_split=true but only 1 group (a
// "cohesive change" verdict), runSplit must print "Not splitting" and return nil
// rather than reaching BuildSubCommits with 0 trees for 1 group (panic).
//
// The test injects a stub backend that returns a valid 1-group proposal whose
// hunk_ids cover all hunks in the commit (so ValidateCoverage passes and the
// engine's own <2-group short-circuit fires, returning nil trees). The guard
// added back to runSplit catches this before printProposal.
func TestRunSplitSingleGroupNoPanic(t *testing.T) {
	_, repo := newSplittableCapabilityRepo(t)

	var out bytes.Buffer

	// MinSplitLines=1 so the small test commit (4 changed lines total) is not
	// skipped by ShouldSkipSplit before even reaching the backend.
	cfg := config.Default()
	cfg.Split.MinSplitLines = 1

	// Resolve the parent tree and fetch actual hunk IDs.
	// The commit produced by newSplittableCapabilityRepo changes two files
	// (a.txt, b.txt), so the diff always has ≥2 hunks.
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	// ReadCommits returns root-first; commits[1] is the child (HEAD).
	child := commits[1]
	parentTree, err := ParentTreeOf(repo, child)
	if err != nil {
		t.Fatal(err)
	}
	hunks, err := repo.Hunks(parentTree, child.TreeHash)
	if err != nil {
		t.Fatal(err)
	}
	if len(hunks) < 2 {
		t.Fatalf("need ≥2 hunks for the split path, got %d", len(hunks))
	}

	// Build the stub payload: should_split=true but only 1 group covering ALL
	// hunk IDs. ParseAndValidate (called by ProposeSplit) runs ValidateCoverage,
	// so the IDs must exactly match the real hunks.
	allIDs := make([]string, len(hunks))
	for i, h := range hunks {
		allIDs[i] = h.ID
	}
	type groupWire struct {
		Order   int      `json:"order"`
		HunkIDs []string `json:"hunk_ids"`
		Subject string   `json:"subject"`
	}
	type wire struct {
		ShouldSplit bool        `json:"should_split"`
		Reason      string      `json:"reason"`
		Groups      []groupWire `json:"groups"`
	}
	raw, _ := json.Marshal(wire{
		ShouldSplit: true,
		Reason:      "cohesive change",
		Groups:      []groupWire{{Order: 1, HunkIDs: allIDs, Subject: "All changes"}},
	})

	ctx := &RunContext{
		Repo:    repo,
		Cfg:     cfg,
		Stdout:  &out,
		Stderr:  &out,
		Now:     time.Unix(1000, 0),
		Backend: stubBackend{payload: string(raw)},
	}

	// --execute is set so that if the guard were absent the code would reach
	// BuildSubCommits (0 trees, 1 group → panic). With the guard restored it
	// must return nil before getting there.
	a := newParsedArgs(map[string]any{"execute": true, "no-cache": true}, []string{"HEAD"})

	before, _ := repo.ReadState()
	if err := runSplit(ctx, a); err != nil {
		t.Fatalf("runSplit: unexpected error: %v", err)
	}
	after, _ := repo.ReadState()

	if !bytes.Contains(out.Bytes(), []byte("Not splitting")) {
		t.Errorf("expected 'Not splitting' in output, got:\n%s", out.String())
	}
	if before.HeadHash != after.HeadHash {
		t.Error("runSplit with single-group proposal moved HEAD")
	}
}

// tSplitGroup / tSplitWire mirror the split LLM JSON wire shape for test stubs.
type tSplitGroup struct {
	Order   int      `json:"order"`
	HunkIDs []string `json:"hunk_ids"`
	Subject string   `json:"subject"`
}
type tSplitWire struct {
	ShouldSplit bool          `json:"should_split"`
	Reason      string        `json:"reason"`
	Groups      []tSplitGroup `json:"groups"`
}

// twoGroupPayload builds a valid 2-group split proposal that covers every hunk of
// child's diff exactly once, so ParseAndValidate's coverage check passes and the
// engine yields >=2 groups (reaching runSplit's apply guards).
func twoGroupPayload(t *testing.T, repo *gitio.Repo, child model.Commit) string {
	t.Helper()
	parentTree, err := ParentTreeOf(repo, child)
	if err != nil {
		t.Fatal(err)
	}
	hunks, err := repo.Hunks(parentTree, child.TreeHash)
	if err != nil {
		t.Fatal(err)
	}
	if len(hunks) < 2 {
		t.Fatalf("need >=2 hunks for a 2-group proposal, got %d", len(hunks))
	}
	ids := make([]string, len(hunks))
	for i, h := range hunks {
		ids[i] = h.ID
	}
	mid := len(ids) / 2
	if mid == 0 {
		mid = 1
	}
	raw, _ := json.Marshal(tSplitWire{
		ShouldSplit: true,
		Reason:      "two cohesive groups",
		Groups: []tSplitGroup{
			{Order: 1, HunkIDs: ids[:mid], Subject: "group one"},
			{Order: 2, HunkIDs: ids[mid:], Subject: "group two"},
		},
	})
	return string(raw)
}

// newSplittableRepoWithPushedParent is newSplittableCapabilityRepo plus a bare
// origin to which only the PARENT commit is pushed: HEAD (the child, our split
// target) is unpushed. Post-M4-Task-3, this is the proof that a pushed ancestor
// the split never touches must NOT force --force — only the split's own
// rewrite set (target + tail) matters (model.AnyPushed).
func newSplittableRepoWithPushedParent(t *testing.T) (string, *gitio.Repo) {
	t.Helper()
	dir := t.TempDir()
	origin := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if out, err := exec.Command("git", "init", "-q", "--bare", origin).CombinedOutput(); err != nil {
		t.Fatalf("init bare: %v\n%s", err, out)
	}
	run("init", "-q", "-b", "main")
	run("config", "commit.gpgsign", "false")
	run("remote", "add", "origin", origin)

	// Parent commit (two files) — pushed.
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("foo\nbar\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-q", "-m", "parent")
	run("push", "-q", "-u", "origin", "main")

	// Child commit (modifies both files) — unpushed, becomes HEAD / split target.
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("HELLO\nworld\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("foo\nBAR\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-q", "-m", "child: update a and b")

	repo, err := gitio.Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return dir, repo
}

// newSplittableRepoWithPushedTarget is newSplittableRepoWithPushedParent except
// BOTH commits are pushed before the test runs, so the split target (HEAD, the
// child) is itself pushed — the case that must still require --force.
func newSplittableRepoWithPushedTarget(t *testing.T) (string, *gitio.Repo) {
	t.Helper()
	dir := t.TempDir()
	origin := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if out, err := exec.Command("git", "init", "-q", "--bare", origin).CombinedOutput(); err != nil {
		t.Fatalf("init bare: %v\n%s", err, out)
	}
	run("init", "-q", "-b", "main")
	run("config", "commit.gpgsign", "false")
	run("remote", "add", "origin", origin)

	// Parent commit (two files).
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("foo\nbar\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-q", "-m", "parent")

	// Child commit (modifies both files) — becomes HEAD / split target.
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("HELLO\nworld\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("foo\nBAR\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-q", "-m", "child: update a and b")
	run("push", "-q", "-u", "origin", "main") // push BOTH commits, including the split target

	repo, err := gitio.Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return dir, repo
}

// TestRunSplitDirtyTreeIsGuardBlocked: a dirty working tree must block the split
// with exit code 2 (guard), not exit 3 surfacing from ApplySplit.
func TestRunSplitDirtyTreeIsGuardBlocked(t *testing.T) {
	dir, repo := newSplittableCapabilityRepo(t)
	commits, _ := repo.ReadCommits()
	child := commits[1] // root-first: [parent, child]
	payload := twoGroupPayload(t, repo, child)

	// Dirty the working tree (committed trees, hence the proposal, are unaffected).
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("dirty edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Split.MinSplitLines = 1
	var out bytes.Buffer
	ctx := &RunContext{Repo: repo, Cfg: cfg, Stdout: &out, Stderr: &out, Now: time.Unix(1000, 0), Backend: stubBackend{payload: payload}}
	a := newParsedArgs(map[string]any{"execute": true, "no-cache": true}, []string{"HEAD"})

	before, _ := repo.ReadState()
	err := runSplit(ctx, a)
	if got := ExitCode(err); got != 2 {
		t.Fatalf("dirty split exit = %d, want 2 (err=%v)", got, err)
	}
	after, _ := repo.ReadState()
	if before.HeadHash != after.HeadHash {
		t.Error("dirty split moved HEAD despite the guard")
	}
}

// TestRunSplitPushedHistoryDoesNotRequireForce: a branch with already-pushed
// history further BACK than the split target (the parent, here) must NOT
// require --force when the split's own rewrite set — target + tail — never
// touches a pushed commit.
//
// This is the M4 Task 3 correction, made visible at the headless surface: split
// used to over-prompt via the coarse RepoState.HasPushedCommits(), which was
// true whenever ANY upstream existed on the branch at all, regardless of
// whether the split actually rewrote anything published. It now only demands
// --force when model.AnyPushed(target, tail...) is true. Renamed and flipped
// from TestRunSplitPushedHistoryIsGuardBlocked, which asserted the old,
// over-broad "block anyway" behavior — that assertion is now wrong.
func TestRunSplitPushedHistoryDoesNotRequireForce(t *testing.T) {
	_, repo := newSplittableRepoWithPushedParent(t)
	commits, _ := repo.ReadCommits()
	child := commits[1]
	if child.IsPushed {
		t.Fatal("setup: the split target should be unpushed")
	}
	payload := twoGroupPayload(t, repo, child)

	cfg := config.Default()
	cfg.Split.MinSplitLines = 1
	var out bytes.Buffer
	ctx := &RunContext{Repo: repo, Cfg: cfg, Stdout: &out, Stderr: &out, Now: time.Unix(1000, 0), Backend: stubBackend{payload: payload}}
	a := newParsedArgs(map[string]any{"execute": true, "no-cache": true}, []string{"HEAD"})

	before, _ := repo.ReadState()
	if !before.HasUpstream {
		t.Fatal("setup: branch should have an upstream (the pushed parent)")
	}
	err := runSplit(ctx, a)
	if err != nil {
		t.Fatalf("splitting an unpushed target with no pushed tail should succeed without --force: %v", err)
	}
	after, _ := repo.ReadState()
	if before.HeadHash == after.HeadHash {
		t.Error("a successful split should move HEAD")
	}
}

// TestRunSplitPushedTargetRequiresForce: when the split TARGET itself is
// pushed, the split must still be guard-blocked with exit code 2 without
// --force. Companion to TestRunSplitPushedHistoryDoesNotRequireForce, proving
// the corrected check still catches the case that actually matters.
func TestRunSplitPushedTargetRequiresForce(t *testing.T) {
	_, repo := newSplittableRepoWithPushedTarget(t)
	commits, _ := repo.ReadCommits()
	child := commits[1]
	if !child.IsPushed {
		t.Fatal("setup: the split target should be pushed")
	}
	payload := twoGroupPayload(t, repo, child)

	cfg := config.Default()
	cfg.Split.MinSplitLines = 1
	var out bytes.Buffer
	ctx := &RunContext{Repo: repo, Cfg: cfg, Stdout: &out, Stderr: &out, Now: time.Unix(1000, 0), Backend: stubBackend{payload: payload}}
	a := newParsedArgs(map[string]any{"execute": true, "no-cache": true}, []string{"HEAD"})

	before, _ := repo.ReadState()
	err := runSplit(ctx, a)
	if got := ExitCode(err); got != 2 {
		t.Fatalf("pushed-target split without --force exit = %d, want 2 (err=%v)", got, err)
	}
	after, _ := repo.ReadState()
	if before.HeadHash != after.HeadHash {
		t.Error("pushed-target split moved HEAD despite the guard")
	}
}

// TestBuildSplitOptionsContextKeyFromConfig: the cache-fingerprint ContextKey must
// reflect the configured split context, not a hardcoded "readme", so changing the
// context invalidates the cache.
func TestBuildSplitOptionsContextKeyFromConfig(t *testing.T) {
	cfg := config.Default()
	cfg.Split.Context = []string{"readme", "filestates"}
	ctx := &RunContext{Cfg: cfg, Now: time.Unix(0, 0)}
	if got := buildSplitOptions(ctx, newParsedArgs(nil, nil)).ContextKey; got != "readme,filestates" {
		t.Errorf("ContextKey = %q, want %q", got, "readme,filestates")
	}

	def := &RunContext{Cfg: config.Default(), Now: time.Unix(0, 0)}
	if got := buildSplitOptions(def, newParsedArgs(nil, nil)).ContextKey; got != "readme" {
		t.Errorf("default ContextKey = %q, want %q", got, "readme")
	}
}
