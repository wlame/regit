package safety

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/gittest"
	"github.com/wlame/regit/internal/model"
)

// newRedactRepo builds a temp repo with one commit per entry in commitFiles,
// writing/overwriting the named files with the given content. Modeled
// directly on purge_test.go's newPurgeRepo — same hermetic env, same
// one-commit-per-map shape — so a redact fixture reads the same way a purge
// one does.
func newRedactRepo(t *testing.T, commitFiles []map[string]string) *gitio.Repo {
	t.Helper()
	dir := t.TempDir()
	run := func(env []string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = gittest.Env(dir, env...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run(nil, "init", "-q")
	run(nil, "symbolic-ref", "HEAD", "refs/heads/main")
	run(nil, "config", "user.name", "wlame")
	run(nil, "config", "user.email", "vvlame@gmail.com")
	run(nil, "config", "commit.gpgsign", "false")

	date := []string{
		"GIT_AUTHOR_DATE=2026-07-05T14:00:00+00:00",
		"GIT_COMMITTER_DATE=2026-07-05T14:00:00+00:00",
	}
	for i, files := range commitFiles {
		for name, content := range files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		run(nil, "add", ".")
		run(date, "commit", "-q", "-m", fmt.Sprintf("commit %d", i))
	}
	repo, err := gitio.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return repo
}

// redactRotationRepo builds: c1 adds cfg.yaml carrying "token: sk-OLD"; c2
// adds an unrelated other.txt (cfg.yaml rides along unmodified); c3 rotates
// ONLY the secret line to "token: sk-NEW" (other.txt untouched); c4 edits
// other.txt (the rotated line rides along). Mirrors gitio's own
// redactHistoryRepo fixture exactly — see its doc comment for why this shape
// exercises both the carry-forward base detection and the rotation-only drop.
func redactRotationRepo(t *testing.T) *gitio.Repo {
	t.Helper()
	return newRedactRepo(t, []map[string]string{
		{"cfg.yaml": "a\ntoken: sk-OLD\n"},
		{"other.txt": "unrelated-v1\n"},
		{"cfg.yaml": "a\ntoken: sk-NEW\n"},
		{"other.txt": "unrelated-v2\n"},
	})
}

func TestApplyRedact_endToEndRemoveVerifies(t *testing.T) {
	repo := redactRotationRepo(t)
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	state, err := repo.ReadState()
	if err != nil {
		t.Fatal(err)
	}
	oldHead := state.HeadHash
	spec := model.RedactSpec{
		Match: "sk-OLD", Mode: model.RedactRemove,
		Targets: map[string][]string{"cfg.yaml": {"token: sk-OLD"}},
	}

	mgr := NewManager(repo)
	res, err := mgr.ApplyRedact(commits, spec, true, state,
		ApplyOptions{Sign: gitio.DropSignatures, UnixTimestamp: 1717689600, ExpectedHead: oldHead})
	if err != nil {
		t.Fatalf("ApplyRedact: %v", err)
	}
	if res.NewHead == oldHead {
		t.Error("redact should have produced a new head")
	}
	if !mgr.CanUndo() {
		t.Error("a backup should have been pushed onto the undo stack")
	}
	if res.Verification.Commits == 0 {
		t.Error("verification summary should report the rewritten commits")
	}

	// The secret must be gone: no history containing it, and absent from the
	// new tip's own tree.
	hashes, err := repo.SecretAddedFrom(res.NewHead, "sk-OLD")
	if err != nil {
		t.Fatal(err)
	}
	if len(hashes) != 0 {
		t.Errorf("sk-OLD should no longer be reachable from the new head, still found: %v", hashes)
	}
	found, err := repo.GrepTree(res.NewHead, "sk-OLD")
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Error("sk-OLD should be absent from the new head's tree")
	}

	restored, err := mgr.Undo(false, 1717689601)
	if err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if restored != oldHead {
		t.Errorf("Undo should restore old head %s, got %s", shortHash(oldHead), shortHash(restored))
	}
	head, err := repo.ResolveRef("HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if head != oldHead {
		t.Errorf("HEAD after undo = %s, want original %s", shortHash(head), shortHash(oldHead))
	}
}

// TestApplyRedact_dropsRotationOnlyCommitStructurally is the cascade-selected
// case: the spec targets BOTH the OLD and the rotated NEW line content, so
// c3 — which only ever rotated the secret line — collapses to its rewritten
// parent's tree and must be DROPPED (mirrors gitio's own
// TestApplyRedact_removesAcrossSnapshotsAndDropsEmptied, but exercised here
// through the guarded apply, including verifyChainStructure and the
// verification summary).
func TestApplyRedact_dropsRotationOnlyCommitStructurally(t *testing.T) {
	repo := redactRotationRepo(t)
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	state, err := repo.ReadState()
	if err != nil {
		t.Fatal(err)
	}
	spec := model.RedactSpec{
		Match: "sk-OLD", Mode: model.RedactRemove,
		Targets: map[string][]string{"cfg.yaml": {"token: sk-OLD", "token: sk-NEW"}},
	}

	mgr := NewManager(repo)
	res, err := mgr.ApplyRedact(commits, spec, true, state,
		ApplyOptions{Sign: gitio.DropSignatures, UnixTimestamp: 1, ExpectedHead: state.HeadHash})
	if err != nil {
		t.Fatalf("ApplyRedact: %v", err)
	}

	// c3 (the rotation-only commit) must have vanished: 4 commits in, 3 out.
	chain, err := repo.FirstParents(res.NewHead, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(chain) != 3 {
		t.Fatalf("rewritten chain length = %d, want 3 (c3 dropped)", len(chain))
	}
	subjects, err := gitLogSubjects(t, repo, res.NewHead)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(subjects, "commit 2") {
		t.Errorf("the rotation-only commit's message should not survive: %s", subjects)
	}

	// verifyChainStructure accepted the length-1 drop; the summary must
	// report exactly the 3 surviving (paired) commits.
	if res.Verification.Commits != 3 {
		t.Errorf("Verification.Commits = %d, want 3 (one commit dropped from the original 4)",
			res.Verification.Commits)
	}
	if !strings.Contains(res.Verification.String(), "3 commits") {
		t.Errorf("verification summary should report 3 commits, got %q", res.Verification.String())
	}
}

// gitLogSubjects returns newHead's first-parent commit subjects, newest
// first, for assertions that a dropped commit's message no longer appears
// anywhere in the rewritten history.
func gitLogSubjects(t *testing.T, repo *gitio.Repo, newHead string) (string, error) {
	t.Helper()
	cmd := exec.Command("git", "log", "--format=%s", newHead)
	cmd.Dir = repo.Root()
	cmd.Env = gittest.Env(repo.Root())
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git log: %w\n%s", err, out)
	}
	return string(out), nil
}

// TestApplyRedact_absenceCheckAbortsOnSurvivingSecret is the sabotage test:
// the replace rule's pattern ("remove-me") never occurs within the
// secret-bearing target line ("token: sk-SURVIVOR"), so that line survives
// byte-for-byte — but a SEPARATE target line in another path DOES match the
// pattern and genuinely changes, which is enough to make gitio.ApplyRedact
// rewrite the commit at all. The precise, per-target-path check (tier 1)
// only ever inspects paths a commit's own Expected set names as changed —
// cfg.yaml (whose content never actually changes anywhere) is never in that
// set, so tier 1 passes cleanly. Only the GLOBAL sweep (tier 2, armed by
// allSelected=true) — which searches the literal across the WHOLE new head,
// not just target paths — can catch the survivor. The branch ref must come
// back byte-for-byte unmoved: verifyRedactAbsence runs inside the Rewrite
// closure, strictly before runApply's UpdateRef call.
func TestApplyRedact_absenceCheckAbortsOnSurvivingSecret(t *testing.T) {
	repo := newRedactRepo(t, []map[string]string{
		{
			"cfg.yaml":  "token: sk-SURVIVOR\n",
			"other.txt": "junk: remove-me\n",
		},
	})
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	state, err := repo.ReadState()
	if err != nil {
		t.Fatal(err)
	}
	oldHead := state.HeadHash
	spec := model.RedactSpec{
		Match: "sk-SURVIVOR",
		Mode:  model.RedactReplace,
		Rule:  model.RedactRule{Pattern: "remove-me", Replacement: "GONE"},
		Targets: map[string][]string{
			"cfg.yaml":  {"token: sk-SURVIVOR"},
			"other.txt": {"junk: remove-me"},
		},
	}

	mgr := NewManager(repo)
	_, err = mgr.ApplyRedact(commits, spec, true, state,
		ApplyOptions{Sign: gitio.DropSignatures, UnixTimestamp: 1, ExpectedHead: oldHead})
	if err == nil {
		t.Fatal("a replace rule that leaves the literal in place must abort, not silently succeed")
	}
	if !strings.Contains(err.Error(), "reachable") {
		t.Errorf("error should come from the GLOBAL absence sweep (mentioning reachability), "+
			"got: %v", err)
	}

	newHead, err := repo.ResolveRef("HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if newHead != oldHead {
		t.Errorf("an aborted redact must not move the branch: %s -> %s",
			shortHash(oldHead), shortHash(newHead))
	}
}

func TestApplyRedact_mergeInRangeBlocks(t *testing.T) {
	repo := redactRotationRepo(t)
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	state, err := repo.ReadState()
	if err != nil {
		t.Fatal(err)
	}
	oldHead := state.HeadHash
	spec := model.RedactSpec{
		Match: "sk-OLD", Mode: model.RedactRemove,
		Targets: map[string][]string{"cfg.yaml": {"token: sk-OLD"}},
	}
	// base is commit 0 (cfg.yaml first carries the secret); flag a later
	// commit within the rewritten range as a merge to trigger the guard.
	commits[2].IsMerge = true

	_, err = NewManager(repo).ApplyRedact(commits, spec, true, state,
		ApplyOptions{UnixTimestamp: 1, ExpectedHead: oldHead})
	if err == nil {
		t.Fatal("redacting across a merge commit in the rewritten range must be blocked")
	}
	if !strings.Contains(err.Error(), "merge") {
		t.Errorf("error should mention merge, got: %v", err)
	}
	newHead, err := repo.ResolveRef("HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if newHead != oldHead {
		t.Errorf("a guard-blocked redact must not move the branch: %s -> %s",
			shortHash(oldHead), shortHash(newHead))
	}
}

// TestVerifyRedactAbsence_preciseDetectsLineSurvival unit-tests the PRECISE
// check's sub-check (a) directly, independent of whether a real ApplyRedact
// run could ever actually produce this shape (it cannot: RedactBlobsInTree
// guarantees removal). This is the same "convert a hypothetical rewrite bug
// into a safe abort" role TestVerifyPurgeAbsence_detectsPathStillReachable
// plays for purge: verifyRedactAbsence is handed an expectation claiming
// cfg.yaml was modified, against a tree where the target line was never
// actually touched, and must catch the survivor on its own.
func TestVerifyRedactAbsence_preciseDetectsLineSurvival(t *testing.T) {
	repo := newRedactRepo(t, []map[string]string{
		{"cfg.yaml": "token: sk-OLD\n"},
	})
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	head := commits[0].Hash
	spec := model.RedactSpec{
		Match: "sk-OLD", Mode: model.RedactRemove,
		Targets: map[string][]string{"cfg.yaml": {"token: sk-OLD"}},
	}
	expectations := []model.Expectation{
		{OldCommit: head, NewCommit: head, Expected: model.NewChangeSet(
			[]model.PathChange{{Path: "cfg.yaml", Status: model.Modified}})},
	}

	mgr := NewManager(repo)
	err = mgr.verifyRedactAbsence(head, spec, false, expectations)
	if err == nil {
		t.Fatal("verifyRedactAbsence should catch a target line that never actually left the tree")
	}
	if !strings.Contains(err.Error(), "still present in") {
		t.Errorf("error should name the surviving line, got: %v", err)
	}
}

// TestVerifyRedactAbsence_preciseDetectsIncompleteReplaceLiteral isolates
// sub-check (b): cfg.yaml's line no longer matches its ORIGINAL target text
// verbatim (so sub-check (a) alone passes cleanly), but the literal
// spec.Match is still sitting inside the line — the shape a replace rule
// that edits SOME bytes without actually stripping the secret would leave
// behind. Without this second sub-check, sub-check (a) alone would wrongly
// pass such a rewrite.
func TestVerifyRedactAbsence_preciseDetectsIncompleteReplaceLiteral(t *testing.T) {
	repo := newRedactRepo(t, []map[string]string{
		{"cfg.yaml": "PREFIX sk-OLD suffix\n"},
	})
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	head := commits[0].Hash
	spec := model.RedactSpec{
		Match: "sk-OLD", Mode: model.RedactReplace,
		Rule:    model.RedactRule{Pattern: "prefix", Replacement: "PREFIX"},
		Targets: map[string][]string{"cfg.yaml": {"prefix sk-OLD suffix"}},
	}
	expectations := []model.Expectation{
		{OldCommit: head, NewCommit: head, Expected: model.NewChangeSet(
			[]model.PathChange{{Path: "cfg.yaml", Status: model.Modified}})},
	}

	mgr := NewManager(repo)
	err = mgr.verifyRedactAbsence(head, spec, false, expectations)
	if err == nil {
		t.Fatal("verifyRedactAbsence should catch the literal surviving inside an incompletely " +
			"replaced line")
	}
	if !strings.Contains(err.Error(), "after replace") {
		t.Errorf("error should mention the replace-mode literal check, got: %v", err)
	}
}

func TestApplyRedact_pushedRequiresForce(t *testing.T) {
	repo := redactRotationRepo(t)
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	state, err := repo.ReadState()
	if err != nil {
		t.Fatal(err)
	}
	oldHead := state.HeadHash
	spec := model.RedactSpec{
		Match: "sk-OLD", Mode: model.RedactRemove,
		Targets: map[string][]string{"cfg.yaml": {"token: sk-OLD"}},
	}
	// commits[0] (c1) is the base; flag it pushed.
	commits[0].IsPushed = true

	mgr := NewManager(repo)
	_, err = mgr.ApplyRedact(commits, spec, true, state,
		ApplyOptions{UnixTimestamp: 1, ExpectedHead: oldHead})
	if err == nil {
		t.Fatal("an unconfirmed redact touching a pushed commit must be refused")
	}
	if !strings.Contains(err.Error(), "confirmation") {
		t.Errorf("error should mention confirmation, got: %v", err)
	}
	newHead, _ := repo.ResolveRef("HEAD")
	if newHead != oldHead {
		t.Errorf("a refused redact must not move HEAD: %s -> %s", oldHead, newHead)
	}

	if _, err := mgr.ApplyRedact(commits, spec, true, state,
		ApplyOptions{UnixTimestamp: 2, Confirmed: true, ExpectedHead: oldHead}); err != nil {
		t.Errorf("a confirmed redact touching a pushed commit should proceed: %v", err)
	}
}
