package gitio

import (
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
