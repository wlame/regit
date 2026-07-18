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
// rewrite the commit at all. Both cfg.yaml and other.txt are in spec.Targets,
// so the PRECISE tier (tier 1) — which inspects every path in spec.Targets
// for every surviving commit, not just the paths a commit's own change-set
// happens to name — catches the survivor on its own; allSelected=true here
// additionally confirms the GLOBAL sweep (tier 2) remains available too, had
// tier 1 somehow missed it. The branch ref must come back byte-for-byte
// unmoved: verifyRedactAbsence runs inside the Rewrite closure, strictly
// before runApply's UpdateRef call.
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
	if !strings.Contains(err.Error(), "still present in") {
		t.Errorf("error should come from the PRECISE absence sweep (mentioning the surviving "+
			"line), got: %v", err)
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

// TestApplyRedact_partialSelectionPreciseTierAbortsOnSurvivor is the
// regression test for the CRITICAL missed-secret finding: with
// allSelected=false the GLOBAL sweep never runs at all, so the PRECISE tier
// is the ONLY safety net standing between a broken replace rule and a
// successful apply. Same sabotage shape as the sibling test above (a rule
// pattern that never matches the secret-bearing target line, but a SEPARATE
// target line in another path genuinely changes so the commit is rewritten
// at all) — but this time nothing arms the global sweep, so the fix must
// come from the precise tier alone: it must inspect EVERY path in
// spec.Targets for every surviving (non-dropped) commit, not just the paths
// a given commit's own change-set happens to name.
func TestApplyRedact_partialSelectionPreciseTierAbortsOnSurvivor(t *testing.T) {
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
	_, err = mgr.ApplyRedact(commits, spec, false, state,
		ApplyOptions{Sign: gitio.DropSignatures, UnixTimestamp: 1, ExpectedHead: oldHead})
	if err == nil {
		t.Fatal("a selected target line the replace rule fails to strip must abort the apply " +
			"even with allSelected=false (the global sweep never runs in that case — the " +
			"precise tier is the only net)")
	}
	if !strings.Contains(err.Error(), "still present in") {
		t.Errorf("error should come from the PRECISE tier (mentioning the surviving line), got: %v", err)
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

// TestApplyRedact_globalTierAbortsOnSurvivorOutsideTargets isolates tier 2
// (GLOBAL) with a construction where tier 1 (PRECISE) genuinely PASSES
// first: the literal "sk-SHARED" lives in TWO files, but spec.Targets names
// only cfg.yaml (simulating a scan/selection that covered only one
// location). The redact genuinely, successfully strips the targeted line
// from cfg.yaml — RedactBlobsInTree only ever touches paths present in
// spec.Targets (see its doc comment), so other.txt is never even looked at
// and rides forward byte-for-byte, literal intact. By construction:
//   - Tier 1 loops spec.Targets = {cfg.yaml}; cfg.yaml's target line is
//     genuinely gone from the new tree, so redactLineSurvives returns false
//     and tier 1 returns nil cleanly — a REAL pass, not a vacuous one (the
//     check runs and the redaction it inspects actually worked).
//   - Tier 2 (armed by allSelected=true) re-scans the ENTIRE rewritten
//     head — not spec.Targets — via SecretAddedFrom, which still finds the
//     literal reachable through other.txt's untouched content, and aborts.
//
// This is the ONLY tier that can catch a survivor entirely outside
// spec.Targets — the highest-value guarantee the absence check provides,
// and the one a future regression in tier 1's scoping could otherwise leave
// completely unpinned by the test suite.
func TestApplyRedact_globalTierAbortsOnSurvivorOutsideTargets(t *testing.T) {
	repo := newRedactRepo(t, []map[string]string{
		{
			"cfg.yaml":  "token: sk-SHARED\n",
			"other.txt": "backup: sk-SHARED\n",
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
		Match: "sk-SHARED", Mode: model.RedactRemove,
		Targets: map[string][]string{"cfg.yaml": {"token: sk-SHARED"}},
	}

	mgr := NewManager(repo)
	_, err = mgr.ApplyRedact(commits, spec, true, state,
		ApplyOptions{Sign: gitio.DropSignatures, UnixTimestamp: 1, ExpectedHead: oldHead})
	if err == nil {
		t.Fatal("a survivor outside spec.Targets must abort via the GLOBAL tier, not silently succeed")
	}
	const wantSubstr = `"sk-SHARED" still reachable from the rewritten head's history`
	if !strings.Contains(err.Error(), wantSubstr) {
		t.Errorf("error should be the exact GLOBAL-tier wording %q, got: %v", wantSubstr, err)
	}

	newHead, herr := repo.ResolveRef("HEAD")
	if herr != nil {
		t.Fatal(herr)
	}
	if newHead != oldHead {
		t.Errorf("a GLOBAL-tier abort must not move the branch: %s -> %s",
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
// sub-check (b): cfg.yaml's actual tree content no longer matches the
// ORIGINAL target text verbatim (so sub-check (a) alone passes cleanly), but
// applying the spec's OWN rule to that original target text still leaves the
// literal spec.Match embedded — the shape a rule that edits SOME bytes
// without actually stripping the secret produces. Sub-check (b) is a pure,
// commit-independent recomputation (see redactReplacedLineStillHasMatch), so
// this also proves it runs even when the tree happens to already look
// "changed" for an unrelated reason.
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
	if !strings.Contains(err.Error(), "after replacing") {
		t.Errorf("error should mention the replace-mode literal check, got: %v", err)
	}
}

// TestVerifyRedactAbsence_replaceLiteralCheckIgnoresUnselectedLineOnSamePath
// is the direct regression test for the false-abort finding: Targets selects
// ONLY "junk: remove-me" on cfg.yaml, and the tree already reflects that
// line's correct replacement ("junk: GONE") — but a SEPARATE, unselected
// line on that SAME path ("token: sk-OLD", never in Targets) still legally
// carries the literal (the operator vetoed that occurrence). A whole-blob
// grep for spec.Match would find it there and abort every time, making this
// kind of same-path veto permanently un-appliable; scoping the check to the
// RECOMPUTED replaced text of only the selected line must pass cleanly.
func TestVerifyRedactAbsence_replaceLiteralCheckIgnoresUnselectedLineOnSamePath(t *testing.T) {
	repo := newRedactRepo(t, []map[string]string{
		{"cfg.yaml": "token: sk-OLD\njunk: GONE\n"},
	})
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	head := commits[0].Hash
	spec := model.RedactSpec{
		Match: "sk-OLD", Mode: model.RedactReplace,
		Rule:    model.RedactRule{Pattern: "remove-me", Replacement: "GONE"},
		Targets: map[string][]string{"cfg.yaml": {"junk: remove-me"}},
	}
	expectations := []model.Expectation{
		{OldCommit: head, NewCommit: head, Expected: model.NewChangeSet(
			[]model.PathChange{{Path: "cfg.yaml", Status: model.Modified}})},
	}

	mgr := NewManager(repo)
	if err := mgr.verifyRedactAbsence(head, spec, false, expectations); err != nil {
		t.Errorf("a literal on an unselected line of the same path must not trip the replace-mode "+
			"check, got: %v", err)
	}
}

// TestApplyRedact_verifyStageAbortsOnWrongExpectationRefUnmoved pins the
// keystone property directly rather than by composition: it does not rely on
// redactExpectations/redactModifiedPaths ever being correct (they are, by
// construction — see redact.go's doc comments — so a real ApplyRedact call
// can never actually produce a wrong expectation to exercise this with). It
// drives runApply directly with a Rewrite closure that performs a REAL,
// correct gitio.ApplyRedact rewrite but then hands back a DELIBERATELY WRONG
// expectation — claiming other.txt changed in c1's pair, though c1 (the
// first commit in redactRotationRepo) never contained other.txt at all. If
// the generic verify() stage ever stopped catching a wrong expectation for
// ANY reason (a future refactor bug, say), this is what would catch it: the
// apply must error, and the branch ref must be byte-for-byte unmoved.
func TestApplyRedact_verifyStageAbortsOnWrongExpectationRefUnmoved(t *testing.T) {
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
	_, err = mgr.runApply(applyOp{
		Guards: func() error { return nil },
		Rewrite: func(string) (rewriteResult, error) {
			newHead, remap, err := repo.ApplyRedact(commits, spec, gitio.DropSignatures)
			if err != nil {
				return rewriteResult{}, err
			}
			c1New, ok := remap.Rewritten[commits[0].Hash]
			if !ok {
				t.Fatalf("setup: c1 should be rewritten, remap: %+v", remap)
			}
			wrongExpectations := []model.Expectation{
				{
					OldCommit: commits[0].Hash, NewCommit: c1New,
					Expected: model.NewChangeSet(
						[]model.PathChange{{Path: "other.txt", Status: model.Modified}}),
				},
			}
			return rewriteResult{
				NewHead:      newHead,
				Expectations: wrongExpectations,
				OldLen:       len(commits),
				NewLen:       len(commits) - len(remap.Dropped),
			}, nil
		},
	}, ApplyOptions{UnixTimestamp: 1, ExpectedHead: oldHead})

	if err == nil {
		t.Fatal("a wrong expectation must abort at the generic verify stage, not silently pass")
	}
	newHead, herr := repo.ResolveRef("HEAD")
	if herr != nil {
		t.Fatal(herr)
	}
	if newHead != oldHead {
		t.Errorf("a verify-stage abort must not move the branch: %s -> %s",
			shortHash(oldHead), shortHash(newHead))
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
