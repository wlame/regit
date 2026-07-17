package gitio

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/wlame/regit/internal/model"
)

// redactScanRepo builds the 5-commit fixture shared by every scan-primitive
// test: c1 adds cfg.yaml with the secret line plus its 2 neighbor lines, c2
// is an unrelated commit (it also seeds docs/readme.md so the glob-narrowing
// test has a real directory to exclude), c3 rotates the secret to a new
// value, c4 renames the file with NO content change, c5 deletes the secret
// line entirely (leaving its neighbors). Returns the repo and the 5 commits
// root-first (ReadCommits' order, same convention as purgeHistoryRepo).
func redactScanRepo(t *testing.T) (*Repo, []model.Commit) {
	t.Helper()
	r, dir := purgeTestRepoEmpty(t) // reuse purge_test.go's throwaway-repo helper
	commit := func(msg string, files map[string]string) {
		t.Helper()
		for name, content := range files {
			mustWriteFile(t, dir, name, content)
		}
		gitT(t, dir, "add", ".")
		gitT(t, dir, "commit", "-m", msg)
	}
	commit("c1 add secret", map[string]string{
		"cfg.yaml": "before\ntoken: sk-OLD\nafter\n",
	})
	commit("c2 unrelated", map[string]string{
		"docs/readme.md": "unrelated docs text\n",
	})
	commit("c3 rotate secret", map[string]string{
		"cfg.yaml": "before\ntoken: sk-NEW\nafter\n",
	})
	mustMkdir(t, dir, "conf")
	gitT(t, dir, "mv", "cfg.yaml", "conf/cfg.yaml")
	gitT(t, dir, "commit", "-m", "c4 rename cfg.yaml")
	commit("c5 delete secret line", map[string]string{
		"conf/cfg.yaml": "before\nafter\n",
	})
	commits, err := r.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 5 {
		t.Fatalf("setup: want 5 commits, got %d", len(commits))
	}
	return r, commits
}

// multiLineRewriteRepo is the separate 2-commit fixture for the ambiguous-
// rewrite case: c1 adds the tracked line, c2 replaces it with TWO new lines
// in a single contiguous change (so -U0 groups them into one hunk).
func multiLineRewriteRepo(t *testing.T) (*Repo, []model.Commit) {
	t.Helper()
	r, dir := purgeTestRepoEmpty(t)
	mustWriteFile(t, dir, "cfg.yaml", "before\ntoken: sk-OLD\nafter\n")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "c1 add secret")
	mustWriteFile(t, dir, "cfg.yaml", "before\nfirst-line\nsecond-line\nafter\n")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "c2 multi-line rewrite")
	commits, err := r.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 2 {
		t.Fatalf("setup: want 2 commits, got %d", len(commits))
	}
	return r, commits
}

func TestParseHunks(t *testing.T) {
	// A literal patch string (not run through git) so this test pins the
	// parser's shape directly, independent of any end-to-end git invocation.
	// Deliberately includes: a -U2-style hunk with context lines on both
	// sides of a change, a second hunk whose header carries trailing
	// "function context" text (what git's own heuristic appends after the
	// second "@@" — parseHunks must keep that verbatim, not try to parse it),
	// and a trailing "\ No newline at end of file" marker line, which must
	// NOT be captured as hunk content.
	patch := strings.Join([]string{
		"diff --git a/cfg.yaml b/cfg.yaml",
		"index ff84492..33ebf5d 100644",
		"--- a/cfg.yaml",
		"+++ b/cfg.yaml",
		"@@ -1,3 +1,3 @@",
		" before",
		"-token: sk-OLD",
		"+token: sk-NEW",
		" after",
		"@@ -10,0 +11,2 @@ trailing context",
		"+extra1",
		"+extra2",
		"\\ No newline at end of file",
		"",
	}, "\n")

	hunks := parseHunks(patch)
	if len(hunks) != 2 {
		t.Fatalf("got %d hunks, want 2: %+v", len(hunks), hunks)
	}

	h0 := hunks[0]
	if h0.header != "@@ -1,3 +1,3 @@" {
		t.Errorf("hunk0 header = %q", h0.header)
	}
	want0 := []hunkLine{
		{op: ' ', text: "before"},
		{op: '-', text: "token: sk-OLD"},
		{op: '+', text: "token: sk-NEW"},
		{op: ' ', text: "after"},
	}
	if !reflect.DeepEqual(h0.lines, want0) {
		t.Errorf("hunk0 lines = %+v, want %+v", h0.lines, want0)
	}

	h1 := hunks[1]
	if h1.header != "@@ -10,0 +11,2 @@ trailing context" {
		t.Errorf("hunk1 header = %q", h1.header)
	}
	want1 := []hunkLine{
		{op: '+', text: "extra1"},
		{op: '+', text: "extra2"},
	}
	if !reflect.DeepEqual(h1.lines, want1) {
		t.Errorf("hunk1 lines = %+v, want %+v (the \"\\ No newline\" marker must be excluded)",
			h1.lines, want1)
	}
}

// TestParseHunks_noNewlineMarkerMidHunkDoesNotEndCollection is the direct
// regression test for a CRITICAL finding: git emits "\ No newline at end of
// file" INLINE, immediately after ANY hunk line whose file lacks a trailing
// newline — including a '-' line followed by more '+'/'-' lines in the SAME
// hunk (verified against real git: a same-line rotation in a no-final-newline
// file produces exactly this shape). The original parser treated that marker
// as "end this hunk", silently discarding every line after it — so the '+'
// line carrying the ROTATED value never made it into hunk.lines at all.
func TestParseHunks_noNewlineMarkerMidHunkDoesNotEndCollection(t *testing.T) {
	patch := strings.Join([]string{
		"diff --git a/cfg.yaml b/cfg.yaml",
		"index 935995c..9372754 100644",
		"--- a/cfg.yaml",
		"+++ b/cfg.yaml",
		"@@ -2 +2 @@ before",
		"-token: sk-OLD",
		"\\ No newline at end of file",
		"+token: sk-NEW",
		"\\ No newline at end of file",
		"",
	}, "\n")
	hunks := parseHunks(patch)
	if len(hunks) != 1 {
		t.Fatalf("got %d hunks, want 1: %+v", len(hunks), hunks)
	}
	want := []hunkLine{
		{op: '-', text: "token: sk-OLD"},
		{op: '+', text: "token: sk-NEW"},
	}
	if !reflect.DeepEqual(hunks[0].lines, want) {
		t.Errorf("lines = %+v, want %+v (the '+' line after a MID-hunk \"\\ No newline\" "+
			"marker must still be collected)", hunks[0].lines, want)
	}
}

// TestParseHunks_textBeforeFirstHunkIsIgnored covers the file-header skip
// explicitly: mode/index/---/+++ lines (and anything else before the first
// "@@ " marker, such as diff-tree's leading commit-hash line) must never leak
// into a hunk's lines.
func TestParseHunks_textBeforeFirstHunkIsIgnored(t *testing.T) {
	patch := strings.Join([]string{
		"6ba217bc77e914cdde6f4a935f503dc6860d31ab", // diff-tree's own leading hash line
		"diff --git a/x.txt b/x.txt",
		"new file mode 100644",
		"index 0000000..ff84492",
		"--- /dev/null",
		"+++ b/x.txt",
		"@@ -0,0 +1,1 @@",
		"+hello",
		"",
	}, "\n")
	hunks := parseHunks(patch)
	if len(hunks) != 1 {
		t.Fatalf("got %d hunks, want 1: %+v", len(hunks), hunks)
	}
	want := []hunkLine{{op: '+', text: "hello"}}
	if !reflect.DeepEqual(hunks[0].lines, want) {
		t.Errorf("lines = %+v, want %+v (file header/leading hash must be skipped)", hunks[0].lines, want)
	}
}

func TestSecretAdditions_findsIntroWithContext(t *testing.T) {
	r, commits := redactScanRepo(t)

	hits, err := r.SecretAdditions("sk-OLD", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("got %d hits, want 1: %+v", len(hits), hits)
	}
	hit := hits[0]
	if hit.Commit != commits[0].Hash {
		t.Errorf("hit.Commit = %s, want c1 %s", short(hit.Commit), short(commits[0].Hash))
	}
	if hit.Path != "cfg.yaml" {
		t.Errorf("hit.Path = %q, want cfg.yaml", hit.Path)
	}
	if hit.Line != "token: sk-OLD" {
		t.Errorf("hit.Line = %q, want %q", hit.Line, "token: sk-OLD")
	}
	joined := strings.Join(hit.Context, "\n")
	if !strings.Contains(joined, "before") || !strings.Contains(joined, "after") {
		t.Errorf("hit.Context = %v, want it to contain the neighbor lines before/after", hit.Context)
	}

	// Searching the rotated value finds the rotation commit (c3), not c1.
	hitsNew, err := r.SecretAdditions("sk-NEW", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(hitsNew) != 1 || hitsNew[0].Commit != commits[2].Hash {
		t.Fatalf("SecretAdditions(sk-NEW) = %+v, want exactly one hit on c3 (%s)",
			hitsNew, short(commits[2].Hash))
	}
	if hitsNew[0].Path != "cfg.yaml" {
		t.Errorf("hitsNew[0].Path = %q, want cfg.yaml (c3 predates the c4 rename)", hitsNew[0].Path)
	}
}

func TestSecretAdditions_globNarrows(t *testing.T) {
	r, _ := redactScanRepo(t)
	hits, err := r.SecretAdditions("sk-OLD", []string{"docs/**"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Errorf("hits = %+v, want none (sk-OLD never appears under docs/**)", hits)
	}
}

// TestSecretAdditions_globMatchingPathStillFinds is the positive companion to
// globNarrows: a glob that DOES cover the introducing path must not
// accidentally suppress the real hit too.
func TestSecretAdditions_globMatchingPathStillFinds(t *testing.T) {
	r, commits := redactScanRepo(t)
	hits, err := r.SecretAdditions("sk-OLD", []string{"*.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Commit != commits[0].Hash {
		t.Fatalf("hits = %+v, want exactly one hit on c1 (%s)", hits, short(commits[0].Hash))
	}
}

// noFinalNewlineRotationRepo builds a 2-commit fixture where the secret's
// FILE never has a trailing newline in either commit: c1 adds it, c2 rotates
// the exact same (last) line to a new value. This is the shape that trips
// the mid-hunk "\ No newline at end of file" marker (see
// TestParseHunks_noNewlineMarkerMidHunkDoesNotEndCollection) — and it is a
// realistic one: secrets often live in one-line files or in files whose last
// line is the credential itself, which editors commonly leave without a
// trailing newline.
func noFinalNewlineRotationRepo(t *testing.T) (*Repo, []model.Commit) {
	t.Helper()
	r, dir := purgeTestRepoEmpty(t)
	mustWriteFile(t, dir, "cfg.yaml", "before\ntoken: sk-OLD") // no trailing \n
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "c1 add secret, no trailing newline")
	mustWriteFile(t, dir, "cfg.yaml", "before\ntoken: sk-NEW") // still no trailing \n
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "c2 rotate secret, no trailing newline")
	commits, err := r.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 2 {
		t.Fatalf("setup: want 2 commits, got %d", len(commits))
	}
	return r, commits
}

// TestSecretAdditions_andLineDescendants_noFinalNewlineRotationNotMissed is
// the end-to-end companion to TestParseHunks_noNewlineMarkerMidHunkDoesNotEndCollection:
// both entry points that consume parseHunks must survive a no-final-newline
// rotation. Before the fix: SecretAdditions missed the "sk-NEW" addition
// entirely (its '+' line was dropped), and LineDescendants misclassified the
// rotation as a deletion (0 added lines once the '+' line vanished) — the
// live descendant would have silently escaped the cascade.
func TestSecretAdditions_andLineDescendants_noFinalNewlineRotationNotMissed(t *testing.T) {
	r, commits := noFinalNewlineRotationRepo(t)

	hits, err := r.SecretAdditions("sk-NEW", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Commit != commits[1].Hash {
		t.Fatalf("hits = %+v, want exactly one hit on c2 (%s): the rotated value must not be "+
			"dropped just because the file lacks a trailing newline", hits, short(commits[1].Hash))
	}
	if hits[0].Line != "token: sk-NEW" {
		t.Errorf("hits[0].Line = %q, want %q", hits[0].Line, "token: sk-NEW")
	}

	desc, note, err := r.LineDescendants(commits[0].Hash, "cfg.yaml", "token: sk-OLD")
	if err != nil {
		t.Fatal(err)
	}
	if note != "" {
		t.Errorf("note = %q, want empty (this is a clean rotation, not an ambiguous rewrite)", note)
	}
	if len(desc) != 1 || desc[0].Line != "token: sk-NEW" || desc[0].Commit != commits[1].Hash {
		t.Fatalf("descendants = %+v, want exactly one rotation to sk-NEW on c2 — a misparse would "+
			"see 0 added lines and wrongly report this as a deletion (empty descendants, no note)", desc)
	}
}

// renameThenRotateRepo is the discriminating fixture for the rename-tracking
// claim: c1 adds the secret, c2 renames the file with NO content change, c3
// rotates the line AT THE NEW PATH. A pathspec-limited diff (`git diff ...
// -- <old path>`) cannot see this rotation at all — restricting the c1->c2
// diff to the OLD path makes -M report a plain deletion instead of a rename
// (see LineDescendants' doc comment), so a regression to that approach would
// end the chain at c2 with ZERO descendants. Only a correctly
// rename-following implementation reaches c3's rotation. Unlike
// redactScanRepo (whose rename step is immediately followed by a plain
// DELETION — which a pathspec regression also reports as "0 added", so
// deletion masks the discrepancy), this fixture's post-rename step is a
// ROTATION (1 added line), which a pathspec regression cannot produce at
// all: it never even reaches c3's diff, because it already (wrongly) ended
// the chain at c2.
func renameThenRotateRepo(t *testing.T) (*Repo, []model.Commit) {
	t.Helper()
	r, dir := purgeTestRepoEmpty(t)
	mustWriteFile(t, dir, "cfg.yaml", "before\ntoken: sk-OLD\nafter\n")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "c1 add secret")
	mustMkdir(t, dir, "conf")
	gitT(t, dir, "mv", "cfg.yaml", "conf/cfg.yaml")
	gitT(t, dir, "commit", "-m", "c2 rename cfg.yaml")
	mustWriteFile(t, dir, "conf/cfg.yaml", "before\ntoken: sk-NEW\nafter\n")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "c3 rotate secret at new path")
	commits, err := r.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 3 {
		t.Fatalf("setup: want 3 commits, got %d", len(commits))
	}
	return r, commits
}

// TestLineDescendants_rotationAfterRenameTracksNewPath is the discriminating
// test TestLineDescendants_followsRotationRenameAndDeletion is NOT: that
// fixture's rename is immediately followed by a deletion, which a
// pathspec-limited-diff regression also (wrongly, but coincidentally)
// reports as "0 added lines" — so it cannot tell a correct rename-follow
// from a broken one. This test can: only a correct implementation reaches
// c3's rotation at the RENAMED path.
func TestLineDescendants_rotationAfterRenameTracksNewPath(t *testing.T) {
	r, commits := renameThenRotateRepo(t)

	desc, note, err := r.LineDescendants(commits[0].Hash, "cfg.yaml", "token: sk-OLD")
	if err != nil {
		t.Fatal(err)
	}
	if note != "" {
		t.Errorf("note = %q, want empty", note)
	}
	if len(desc) != 1 {
		t.Fatalf("descendants = %+v, want exactly one (the rotation at the renamed path) — "+
			"a pathspec-limited-diff regression would see the rename as a deletion and report none", desc)
	}
	got := desc[0]
	if got.Commit != commits[2].Hash {
		t.Errorf("descendant.Commit = %s, want c3 %s", short(got.Commit), short(commits[2].Hash))
	}
	if got.Path != "conf/cfg.yaml" {
		t.Errorf("descendant.Path = %q, want conf/cfg.yaml (the renamed path)", got.Path)
	}
	if got.Line != "token: sk-NEW" {
		t.Errorf("descendant.Line = %q, want %q", got.Line, "token: sk-NEW")
	}
}

// mergedSideBranchRepo builds a fixture where a commit is merged into main
// via an ORDINARY (non-first-parent) merge: sideHash is a genuine ancestor
// of HEAD (reachable) but is NOT on HEAD's first-parent chain — the exact
// shape LineDescendants' forward-walk contract requires fromCommit to avoid.
func mergedSideBranchRepo(t *testing.T) (r *Repo, sideHash string) {
	t.Helper()
	var dir string
	r, dir = purgeTestRepoEmpty(t)
	mustWriteFile(t, dir, "keep.txt", "v1")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "c0 keep only")
	gitT(t, dir, "checkout", "-q", "-b", "side")
	mustWriteFile(t, dir, "cfg.yaml", "before\ntoken: sk-OLD\nafter\n")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "side adds secret")
	sideHash = gitT(t, dir, "rev-parse", "HEAD")
	gitT(t, dir, "checkout", "-q", "main")
	gitT(t, dir, "merge", "--no-ff", "-q", "-m", "merge side", "side")
	mustWriteFile(t, dir, "keep.txt", "v2")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "main continues")
	return r, sideHash
}

// TestLineDescendants_errorsWhenFromCommitOffFirstParentChain is the seam
// guard test: LineDescendants' forward walk (`rev-list --first-parent
// --reverse fromCommit..HEAD`) silently computes a nonsensical chain when
// fromCommit is not actually ON HEAD's first-parent lineage (a side-branch
// commit merged in via an ordinary merge IS reachable from HEAD, just not
// via first-parent at every step) — SecretAdditions walks ALL parents (no
// --first-parent), so a SecretHit.Commit can legitimately be such a commit.
// LineDescendants must reject it with a clear error instead of silently
// producing garbage for the caller (Task 5's engine) to trust.
func TestLineDescendants_errorsWhenFromCommitOffFirstParentChain(t *testing.T) {
	r, sideHash := mergedSideBranchRepo(t)
	_, _, err := r.LineDescendants(sideHash, "cfg.yaml", "token: sk-OLD")
	if err == nil {
		t.Fatal("LineDescendants should error when fromCommit is not on HEAD's first-parent chain")
	}
	// The error must be recognizable via the sentinel, not just non-nil: Task
	// 5's Scan distinguishes "this one occurrence cannot be cascaded" from
	// every other failure mode by checking errors.Is against exactly this.
	if !errors.Is(err, ErrLineOffFirstParentChain) {
		t.Errorf("err = %v, want it to wrap ErrLineOffFirstParentChain", err)
	}
}

// binarySecretRepo builds a fixture whose single commit adds a binary file
// (a NUL byte inside it) whose content also contains the literal match, plus
// an unrelated text file so glob-narrowing has something real to exclude.
func binarySecretRepo(t *testing.T) (*Repo, []model.Commit) {
	t.Helper()
	r, dir := purgeTestRepoEmpty(t)
	mustWriteFile(t, dir, "bin.dat", "before\x00sk-BINARY\x00after")
	mustWriteFile(t, dir, "docs/readme.md", "unrelated docs text\n")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "add binary secret")
	commits, err := r.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 1 {
		t.Fatalf("setup: want 1 commit, got %d", len(commits))
	}
	return r, commits
}

// TestSecretAdditionsBinary_findsBinaryBlobHit is the direct regression test
// for the gap SecretAdditionsBinary closes: a pickaxe hit whose diff is
// entirely a binary-file change produces ZERO SecretHits from
// SecretAdditions (git's diff-tree renders it as "Binary files ... differ",
// no hunks to parse) — without this primitive the secret would vanish from
// the scan with no trace at all.
func TestSecretAdditionsBinary_findsBinaryBlobHit(t *testing.T) {
	r, commits := binarySecretRepo(t)

	textHits, err := r.SecretAdditions("sk-BINARY", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(textHits) != 0 {
		t.Errorf("SecretAdditions found %+v, want none (the only occurrence is inside a binary blob)",
			textHits)
	}

	binHits, err := r.SecretAdditionsBinary("sk-BINARY", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(binHits) != 1 {
		t.Fatalf("got %d binary hits, want 1: %+v", len(binHits), binHits)
	}
	if binHits[0].Commit != commits[0].Hash || binHits[0].Path != "bin.dat" {
		t.Errorf("binHits[0] = %+v, want Commit=%s Path=bin.dat", binHits[0], short(commits[0].Hash))
	}
}

// TestSecretAdditionsBinary_globNarrows is the binary-hit companion to
// TestSecretAdditions_globNarrows: a glob that never covers the binary file's
// path must suppress the hit.
func TestSecretAdditionsBinary_globNarrows(t *testing.T) {
	r, _ := binarySecretRepo(t)
	hits, err := r.SecretAdditionsBinary("sk-BINARY", []string{"docs/**"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Errorf("hits = %+v, want none (sk-BINARY never appears under docs/**)", hits)
	}
}

// duplicateLineAcrossHunksRepo builds a fixture where the SAME line content
// appears twice in one file, far enough apart that a -U0 diff always puts
// each occurrence's change in its OWN hunk (verified against real git: two
// non-adjacent changed lines never merge into one hunk under -U0, regardless
// of distance). c2 then rotates each occurrence to a DIFFERENT new value.
func duplicateLineAcrossHunksRepo(t *testing.T) (*Repo, []model.Commit) {
	t.Helper()
	r, dir := purgeTestRepoEmpty(t)
	mustWriteFile(t, dir, "cfg.yaml", "token: sk-DUP\nother\ntoken: sk-DUP\n")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "c1 add duplicate lines")
	mustWriteFile(t, dir, "cfg.yaml", "token: ROTATED1\nother\ntoken: ROTATED2\n")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "c2 rotate both occurrences differently")
	commits, err := r.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 2 {
		t.Fatalf("setup: want 2 commits, got %d", len(commits))
	}
	return r, commits
}

// TestLineDescendants_duplicateLineAcrossHunksEndsChainWithNote covers a
// missed-secret variant: when the tracked content is removed by MORE THAN
// ONE hunk in the same commit pair (two identical lines with divergent
// fates), silently picking the first hunk's fate would report only ONE of
// the two rotations and silently drop the other. The conservative fix ends
// the chain with an ambiguity note instead, so a human reviews the commit.
func TestLineDescendants_duplicateLineAcrossHunksEndsChainWithNote(t *testing.T) {
	r, commits := duplicateLineAcrossHunksRepo(t)

	desc, note, err := r.LineDescendants(commits[0].Hash, "cfg.yaml", "token: sk-DUP")
	if err != nil {
		t.Fatal(err)
	}
	if len(desc) != 0 {
		t.Errorf("descendants = %+v, want none (which rotation applies to OUR occurrence is ambiguous)",
			desc)
	}
	if note == "" {
		t.Fatal("note should explain the chain ended due to ambiguity, naming the commit")
	}
	if !strings.Contains(note, short(commits[1].Hash)) {
		t.Errorf("note = %q, want it to name the short hash of c2 (%s)", note, short(commits[1].Hash))
	}
}

func TestLineDescendants_followsRotationRenameAndDeletion(t *testing.T) {
	r, commits := redactScanRepo(t)

	desc, note, err := r.LineDescendants(commits[0].Hash, "cfg.yaml", "token: sk-OLD")
	if err != nil {
		t.Fatal(err)
	}
	if note != "" {
		t.Errorf("note = %q, want empty (the chain runs cleanly to a deletion)", note)
	}
	if len(desc) != 1 {
		t.Fatalf("descendants = %+v, want exactly one (the rotation to sk-NEW)", desc)
	}
	got := desc[0]
	if got.Commit != commits[2].Hash {
		t.Errorf("descendant.Commit = %s, want c3 %s", short(got.Commit), short(commits[2].Hash))
	}
	if got.Path != "cfg.yaml" {
		t.Errorf("descendant.Path = %q, want cfg.yaml (rotation predates the rename)", got.Path)
	}
	if got.Line != "token: sk-NEW" {
		t.Errorf("descendant.Line = %q, want %q", got.Line, "token: sk-NEW")
	}
}

func TestLineDescendants_multiLineRewriteEndsChainWithNote(t *testing.T) {
	r, commits := multiLineRewriteRepo(t)

	desc, note, err := r.LineDescendants(commits[0].Hash, "cfg.yaml", "token: sk-OLD")
	if err != nil {
		t.Fatal(err)
	}
	if len(desc) != 0 {
		t.Errorf("descendants = %+v, want none (the rewrite is ambiguous)", desc)
	}
	wantNote := fmt.Sprintf("line rewritten as a multi-line change in %s; review that commit manually",
		short(commits[1].Hash))
	if note != wantNote {
		t.Errorf("note = %q, want %q", note, wantNote)
	}
}

// TestLineDescendants_untouchedLineReachesHeadWithNoDescendants is the
// negative companion: tracking a line that is NEVER edited again must walk
// the whole forward chain and come back empty-handed, not error or stop early.
func TestLineDescendants_untouchedLineReachesHeadWithNoDescendants(t *testing.T) {
	r, commits := redactScanRepo(t)
	desc, note, err := r.LineDescendants(commits[0].Hash, "cfg.yaml", "before")
	if err != nil {
		t.Fatal(err)
	}
	if len(desc) != 0 || note != "" {
		t.Errorf("desc=%+v note=%q, want no descendants and no note ('before' never touched)",
			desc, note)
	}
}

func TestGrepTree_reportsPresenceAbsence(t *testing.T) {
	r, commits := redactScanRepo(t)

	found, err := r.GrepTree(commits[0].Hash, "sk-OLD")
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Error("sk-OLD should be present in c1's tree")
	}

	head := commits[len(commits)-1].Hash
	found, err = r.GrepTree(head, "sk-OLD")
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Error("sk-OLD should be absent at HEAD (deleted by c5, replaced by c3 before that)")
	}

	// A tree/rev that cannot be resolved at all is a real error, not "absent".
	_, err = r.GrepTree("deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", "sk-OLD")
	if err == nil {
		t.Error("GrepTree on an unresolvable tree should return an error, not (false, nil)")
	}
}

// TestGrepTree_dashPrefixedLiteralDoesNotBreakParsing is a regression test
// for a real finding: `git grep -F --quiet <literal> <tree>` WITHOUT `-e`
// misparses a literal starting with '-' as an unknown flag (verified against
// real git: exit 129 usage error) instead of treating it as the search
// pattern. Secrets commonly start with '-' (base64/token alphabets), so
// GrepTree must pass the literal via -e.
func TestGrepTree_dashPrefixedLiteralDoesNotBreakParsing(t *testing.T) {
	r, dir := purgeTestRepoEmpty(t)
	mustWriteFile(t, dir, "cfg.yaml", "x=-secretdash\n")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "add dash-prefixed secret")
	commits, err := r.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	found, err := r.GrepTree(commits[0].Hash, "-secretdash")
	if err != nil {
		t.Fatalf("GrepTree with a dash-prefixed literal must not error: %v", err)
	}
	if !found {
		t.Error("GrepTree should find the dash-prefixed literal, not silently miss it")
	}
}

func TestSecretAddedFrom_emptyAfterFullRedaction(t *testing.T) {
	r, commits := redactScanRepo(t)

	hashes, err := r.SecretAddedFrom(commits[len(commits)-1].Hash, "sk-OLD")
	if err != nil {
		t.Fatal(err)
	}
	if len(hashes) == 0 {
		t.Error("SecretAddedFrom(HEAD, sk-OLD) should be non-empty: the fixture's history did add it")
	}
	found := false
	for _, h := range hashes {
		if h == commits[0].Hash {
			found = true
		}
	}
	if !found {
		t.Errorf("hashes = %v, want it to include the introducing commit c1 (%s)",
			hashes, short(commits[0].Hash))
	}

	// A fresh repo that never contained the literal at all reports empty.
	r2, dir2 := purgeTestRepoEmpty(t)
	mustWriteFile(t, dir2, "unrelated.txt", "nothing to see here\n")
	gitT(t, dir2, "add", ".")
	gitT(t, dir2, "commit", "-m", "unrelated")
	empty, err := r2.SecretAddedFrom("HEAD", "sk-OLD")
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Errorf("hashes = %v, want none (sk-OLD was never added in this repo)", empty)
	}
}

// mustMkdir creates dir/name (and parents) for fixtures that need a directory
// to exist BEFORE a `git mv` into it — verified against real git that `git
// mv src dst/` does NOT create a missing destination directory itself
// (unlike mustWriteFile, which MkdirAll's the parent of a file it writes).
func mustMkdir(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
		t.Fatal(err)
	}
}
