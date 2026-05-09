package gitio

import (
	"strings"
	"testing"
)

// commitHash returns the current HEAD commit hash. snapTree returns the TREE
// hash (its callers elsewhere feed it to Hunks(parent, child)), but these
// impact tests need commit-ish values to tag/branch — so capture HEAD right
// after each snapTree call, before the next commit moves it.
func commitHash(t *testing.T, dir string) string {
	t.Helper()
	return gitT(t, dir, "rev-parse", "HEAD")
}

func TestRefsPointingAt_reportsTagsAndBranches(t *testing.T) {
	dir, r := initRepo(t)
	gitT(t, dir, "symbolic-ref", "HEAD", "refs/heads/main")
	snapTree(t, dir, "one")
	c1 := commitHash(t, dir)
	snapTree(t, dir, "two")
	c2 := commitHash(t, dir)
	gitT(t, dir, "tag", "v1", c1)
	gitT(t, dir, "tag", "-a", "-m", "annotated", "v2", c1) // annotated tag → resolve via *objectname
	gitT(t, dir, "branch", "side", c1)

	set := map[string]bool{c1: true} // c2 is HEAD; only c1 has refs on it
	impacts, err := r.RefsPointingAt(set)
	if err != nil {
		t.Fatalf("RefsPointingAt: %v", err)
	}
	names := map[string]string{} // name → commit
	for _, im := range impacts {
		names[im.Name] = im.Commit
	}
	for _, want := range []string{"refs/tags/v1", "refs/tags/v2", "refs/heads/side"} {
		if names[want] != c1 {
			t.Errorf("expected %s → %s, got impacts=%v", want, c1, impacts)
		}
	}
	if _, ok := names["refs/heads/main"]; ok {
		// main points at c2, which is not in the set — must not be reported.
		t.Errorf("main should not be reported (points at c2, not in set): %v", impacts)
	}
	_ = c2
}

func TestRefsPointingAt_emptySetReturnsNothing(t *testing.T) {
	_, r := initRepo(t)
	impacts, err := r.RefsPointingAt(nil)
	if err != nil {
		t.Fatalf("RefsPointingAt(nil): %v", err)
	}
	if len(impacts) != 0 {
		t.Errorf("empty set should yield no impacts, got %v", impacts)
	}
}

func TestIsShallow_trueForDepthOneClone(t *testing.T) {
	origin, _ := initRepo(t)
	snapTree(t, origin, "one")
	snapTree(t, origin, "two")

	shallowDir := t.TempDir()
	gitT(t, shallowDir, "clone", "--depth=1", "file://"+origin, ".")
	sr, err := Open(shallowDir)
	if err != nil {
		t.Fatal(err)
	}
	shallow, err := sr.IsShallow()
	if err != nil {
		t.Fatalf("IsShallow: %v", err)
	}
	if !shallow {
		t.Error("depth-1 clone should be shallow")
	}
	if full, _ := r0IsShallow(t, origin); full {
		t.Error("origin should not be shallow")
	}
}

func r0IsShallow(t *testing.T, dir string) (bool, error) {
	t.Helper()
	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return r.IsShallow()
}

func TestReadCommits_carriesEncoding(t *testing.T) {
	dir, r := initRepo(t)
	// Commit with a non-UTF-8 encoding header.
	gitT(t, dir, "config", "i18n.commitEncoding", "ISO-8859-1")
	snapTreeMsg(t, dir, "iso message")
	commits, err := r.ReadCommits()
	if err != nil {
		t.Fatalf("ReadCommits: %v", err)
	}
	last := commits[len(commits)-1]
	if !strings.EqualFold(last.Encoding, "ISO-8859-1") {
		t.Errorf("Encoding = %q, want ISO-8859-1", last.Encoding)
	}
}
