package gitio

import (
	"strings"
	"testing"

	"github.com/wlame/regit/internal/model"
)

// redactHistoryRepo builds: c1 adds cfg.yaml carrying the secret line
// "token: sk-OLD"; c2 adds an unrelated other.txt (cfg.yaml rides along
// unmodified in its snapshot); c3 rotates ONLY the secret line to
// "token: sk-NEW" (other.txt untouched); c4 edits other.txt (cfg.yaml rides
// along again, now carrying the rotated line). This is the shape a secret
// rotation leaves in history: the OLD value lives in two snapshots (c1, c2)
// before the rotate commit (c3) replaces it, and the NEW value then lives in
// the tail (c3, c4) — exactly the cascade a redact spec's Targets can select
// across by naming both the old and new line contents for the same path.
func redactHistoryRepo(t *testing.T) (*Repo, []model.Commit) {
	t.Helper()
	r, dir := purgeTestRepoEmpty(t)
	commit := func(msg string, files map[string]string) {
		t.Helper()
		for name, content := range files {
			mustWriteFile(t, dir, name, content)
		}
		gitT(t, dir, "add", ".")
		gitT(t, dir, "commit", "-m", msg)
	}
	commit("c1 adds cfg with old secret", map[string]string{"cfg.yaml": "a\ntoken: sk-OLD\n"})
	commit("c2 adds unrelated file", map[string]string{"other.txt": "unrelated-v1\n"})
	commit("c3 rotates the secret", map[string]string{"cfg.yaml": "a\ntoken: sk-NEW\n"})
	commit("c4 edits unrelated file", map[string]string{"other.txt": "unrelated-v2\n"})
	commits, err := r.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	return r, commits
}

// treeOf resolves a commit hash to its tree hash, for assertions that need to
// inspect blob content at an arbitrary rewritten commit rather than HEAD.
func treeOf(t *testing.T, r *Repo, commit string) string {
	t.Helper()
	tree, err := r.git("rev-parse", commit+"^{tree}")
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

func TestRedactBase_findsEarliestContainingSnapshot(t *testing.T) {
	r, commits := redactHistoryRepo(t)
	spec := model.RedactSpec{Match: "sk-OLD", Mode: model.RedactRemove,
		Targets: map[string][]string{"cfg.yaml": {"token: sk-OLD"}}}

	base, err := r.RedactBase(commits, spec)
	if err != nil {
		t.Fatal(err)
	}
	if base != 0 {
		t.Fatalf("RedactBase = %d, want 0 (c1, where the secret line is first introduced)", base)
	}

	// c2 never touches cfg.yaml itself, but the secret still rides forward
	// unmodified in its snapshot. RedactBase must find commit 1 not because
	// it diffed cfg.yaml at each step, but because it tests each commit's
	// OWN tree content directly — this assertion proves c2's tree genuinely
	// still contains the target (so the earliest-match walk isn't vacuously
	// trivial: there is a second commit it must correctly walk past too).
	changedTree, err := r.RedactBlobsInTree(commits[1].TreeHash, spec)
	if err != nil {
		t.Fatal(err)
	}
	if changedTree == commits[1].TreeHash {
		t.Fatalf("c2's snapshot should still contain the OLD secret via carry-forward")
	}

	// By c3 the line has been rotated away: its ORIGINAL tree no longer
	// contains "token: sk-OLD" at all, so applying spec must be a genuine
	// no-op (the returned tree hash equals the input).
	sameTree, err := r.RedactBlobsInTree(commits[2].TreeHash, spec)
	if err != nil {
		t.Fatal(err)
	}
	if sameTree != commits[2].TreeHash {
		t.Fatalf("c3's snapshot should no longer contain the OLD secret after rotation")
	}
}

func TestRedactBase_returnsNegativeOneWhenNoSnapshotContainsTarget(t *testing.T) {
	r, commits := redactHistoryRepo(t)
	spec := model.RedactSpec{Match: "nope", Mode: model.RedactRemove,
		Targets: map[string][]string{"cfg.yaml": {"token: sk-NEVER-PRESENT"}}}
	base, err := r.RedactBase(commits, spec)
	if err != nil {
		t.Fatal(err)
	}
	if base != -1 {
		t.Fatalf("RedactBase = %d, want -1 (target line never appears in any snapshot)", base)
	}
}

// TestApplyRedact_removesAcrossSnapshotsAndDropsEmptied covers the cascade
// selection: the spec targets BOTH the old and rotated line contents for
// cfg.yaml, so c3 — which only ever rotated the secret line — collapses to
// exactly its rewritten parent's tree (cfg.yaml == "a\n" either way, other.txt
// untouched by c3) and must be DROPPED, just like a purge commit that only
// ever touched a purged path.
func TestApplyRedact_removesAcrossSnapshotsAndDropsEmptied(t *testing.T) {
	r, commits := redactHistoryRepo(t)
	spec := model.RedactSpec{Match: "sk-OLD", Mode: model.RedactRemove,
		Targets: map[string][]string{"cfg.yaml": {"token: sk-OLD", "token: sk-NEW"}}}

	newHead, remap, err := r.ApplyRedact(commits, spec, DropSignatures)
	if err != nil {
		t.Fatal(err)
	}
	if len(remap.Dropped) != 1 || remap.Dropped[0] != commits[2].Hash {
		t.Errorf("Dropped = %v, want exactly c3 (%s)", remap.Dropped, short(commits[2].Hash))
	}
	if _, ok := remap.Rewritten[commits[0].Hash]; !ok {
		t.Errorf("c1 should be rewritten (its own secret line is targeted), remap: %+v", remap)
	}
	if _, ok := remap.Rewritten[commits[1].Hash]; !ok {
		t.Errorf("c2 should be rewritten (the OLD secret rides along in its snapshot), remap: %+v", remap)
	}
	if _, ok := remap.Rewritten[commits[3].Hash]; !ok {
		t.Errorf("c4 should be rewritten (the rotated secret rides along in its snapshot), "+
			"remap: %+v", remap)
	}
	if len(remap.Passthrough) != 0 {
		t.Errorf("nothing should pass through untouched in this fixture, remap: %+v", remap)
	}

	// The secret must be gone from cfg.yaml everywhere in the rewritten history.
	out, err := r.git("log", "-p", "--format=%H", newHead, "--", "cfg.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "sk-OLD") || strings.Contains(out, "sk-NEW") {
		t.Errorf("secret still present in rewritten history:\n%s", out)
	}
	if got := blobAt(t, r, treeOf(t, r, newHead), "cfg.yaml"); got != "a\n" {
		t.Errorf("final cfg.yaml = %q, want %q", got, "a\n")
	}

	// other.txt was never a redact target; its final content must be
	// byte-identical to c4's original content, proving the transform touched
	// only the targeted path.
	origOther := blobAt(t, r, commits[3].TreeHash, "other.txt")
	newOther := blobAt(t, r, treeOf(t, r, newHead), "other.txt")
	if newOther != origOther {
		t.Errorf("other.txt changed: got %q, want %q", newOther, origOther)
	}
}

// TestApplyRedact_vetoedDescendantSurvives covers the opposite selection: the
// spec targets ONLY the OLD line content, never the rotated one. c3's rotate
// is therefore never itself redacted — but its rewritten PARENT (c2) no
// longer has "token: sk-OLD" at all, so c3's diff against that parent now
// shows "token: sk-NEW" as a pure ADDITION rather than the original history's
// modification of one line into another. c3 must not be dropped, and the
// rotated value must still be reachable in the final head.
func TestApplyRedact_vetoedDescendantSurvives(t *testing.T) {
	r, commits := redactHistoryRepo(t)
	spec := model.RedactSpec{Match: "sk-OLD", Mode: model.RedactRemove,
		Targets: map[string][]string{"cfg.yaml": {"token: sk-OLD"}}}

	newHead, remap, err := r.ApplyRedact(commits, spec, DropSignatures)
	if err != nil {
		t.Fatal(err)
	}
	if len(remap.Dropped) != 0 {
		t.Errorf("nothing should be dropped when only the OLD value is targeted, remap: %+v", remap)
	}
	if _, ok := remap.Rewritten[commits[0].Hash]; !ok {
		t.Errorf("c1 should be rewritten (its own secret line is targeted), remap: %+v", remap)
	}
	rewrittenC2, ok := remap.Rewritten[commits[1].Hash]
	if !ok {
		t.Fatalf("c2 should be rewritten (the OLD value rides along in its snapshot), "+
			"remap: %+v", remap)
	}
	rewrittenC3, ok := remap.Passthrough[commits[2].Hash]
	if !ok {
		t.Fatalf("c3 should pass through untouched (it never carries the targeted OLD value), "+
			"remap: %+v", remap)
	}
	if _, ok := remap.Passthrough[commits[3].Hash]; !ok {
		t.Errorf("c4 should pass through untouched (still no OLD value present), remap: %+v", remap)
	}

	diff, err := r.git("diff", rewrittenC2, rewrittenC3, "--", "cfg.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff, "+token: sk-NEW") {
		t.Errorf("expected the rotated line to show as an addition, diff:\n%s", diff)
	}
	if strings.Contains(diff, "-token: sk-OLD") {
		t.Errorf("the rewritten parent should no longer have the OLD line to remove, diff:\n%s", diff)
	}

	if got := blobAt(t, r, treeOf(t, r, newHead), "cfg.yaml"); got != "a\ntoken: sk-NEW\n" {
		t.Errorf("final cfg.yaml = %q, want %q", got, "a\ntoken: sk-NEW\n")
	}
}

// TestApplyRedact_alreadyEmptyCommitPreserved mirrors
// TestApplyPurge_preservesPreExistingEmptyCommit: a `git commit --allow-empty`
// marker inside the rewritten range must be re-emitted, never dropped, even
// though redacting the secret line makes its tree equal its (rewritten) new
// parent's tree — the exact condition the drop rule alone would use to decide
// "drop" if it did not also check whether the commit was ALREADY empty before
// this redaction ran.
func TestApplyRedact_alreadyEmptyCommitPreserved(t *testing.T) {
	r, dir := purgeTestRepoEmpty(t)
	mustWriteFile(t, dir, "cfg.yaml", "a\ntoken: sk-OLD\nb\n")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "c1 adds cfg with secret")
	gitT(t, dir, "commit", "--allow-empty", "-m", "c2 marker (pre-existing empty)")
	mustWriteFile(t, dir, "cfg.yaml", "a\ntoken: sk-OLD\nb\nc\n")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "c3 edits cfg, unrelated to the secret line")

	commits, err := r.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	if commits[1].TreeHash != commits[0].TreeHash {
		t.Fatalf("setup: c2 (--allow-empty) must share its parent c1's tree")
	}

	spec := model.RedactSpec{Match: "sk-OLD", Mode: model.RedactRemove,
		Targets: map[string][]string{"cfg.yaml": {"token: sk-OLD"}}}
	newHead, remap, err := r.ApplyRedact(commits, spec, DropSignatures)
	if err != nil {
		t.Fatal(err)
	}
	if len(remap.Dropped) != 0 {
		t.Errorf("the marker must never be dropped as a side effect of redaction, remap: %+v", remap)
	}
	subjects, err := r.git("log", "--format=%s", newHead)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(subjects, "c2 marker") {
		t.Errorf("the pre-existing empty marker commit must be preserved in rewritten "+
			"history:\n%s", subjects)
	}
	count, err := r.git("rev-list", "--count", newHead)
	if err != nil {
		t.Fatal(err)
	}
	if count != "3" {
		t.Errorf("rewritten chain length = %s, want 3 (all three commits survive)", count)
	}
	if got := blobAt(t, r, treeOf(t, r, newHead), "cfg.yaml"); got != "a\nb\nc\n" {
		t.Errorf("final cfg.yaml = %q, want %q", got, "a\nb\nc\n")
	}
}
