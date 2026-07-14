package gitio

import (
	"reflect"
	"testing"
)

// scanFixture mimics `git log --format=%x00%H --name-status --diff-filter=AR
// -M --reverse HEAD` output: a NUL+hash record header, then one status line
// per path ("A\tpath" or "R<score>\told\tnew"), blank lines between records.
const scanFixture = "\x00aaaa\n\n" +
	"A\tutil.py\n" +
	"A\tdocs/readme.md\n" +
	"\x00bbbb\n\n" +
	"R100\tutil.py\thelpers.py\n" +
	"\x00cccc\n\n" +
	"A\tdocs/img/logo.png\n"

func TestParsePathScan_addedPathsAndFirstAddWins(t *testing.T) {
	idx := ParsePathScan(scanFixture)
	if idx.AddedIn["util.py"] != "aaaa" {
		t.Errorf("util.py added in %q, want aaaa", idx.AddedIn["util.py"])
	}
	if idx.AddedIn["docs/img/logo.png"] != "cccc" {
		t.Errorf("logo.png added in %q, want cccc", idx.AddedIn["docs/img/logo.png"])
	}
}

func TestParsePathScan_renameChain(t *testing.T) {
	idx := ParsePathScan(scanFixture)
	want := []string{"util.py", "helpers.py"}
	if got := idx.RenameChain("helpers.py"); !reflect.DeepEqual(got, want) {
		t.Errorf("chain(helpers.py) = %v, want %v", got, want)
	}
	if got := idx.RenameChain("util.py"); !reflect.DeepEqual(got, want) {
		t.Errorf("chain(util.py) = %v, want %v", got, want)
	}
	if got := idx.RenameChain("docs/readme.md"); !reflect.DeepEqual(got, []string{"docs/readme.md"}) {
		t.Errorf("chain of unrenamed path = %v", got)
	}
}

func TestParsePathScan_derivesDirectories(t *testing.T) {
	idx := ParsePathScan(scanFixture)
	// Every ancestor directory of every added path, no duplicates, sorted.
	want := []string{"docs", "docs/img"}
	if !reflect.DeepEqual(idx.Dirs, want) {
		t.Errorf("Dirs = %v, want %v", idx.Dirs, want)
	}
}

func TestPathIndexMatch_substringOnBasenameAndPath(t *testing.T) {
	idx := ParsePathScan(scanFixture)
	if got := idx.Match("UTIL"); !reflect.DeepEqual(got, []string{"helpers.py", "util.py"}) {
		t.Errorf("Match(UTIL) = %v (case-insensitive substring expected)", got)
	}
	// A query matching a directory name returns the directory row.
	got := idx.Match("img")
	if !reflect.DeepEqual(got, []string{"docs/img/logo.png", "docs/img"}) {
		t.Errorf("Match(img) = %v, want file match then dir row", got)
	}
}

func TestScanHistoryPaths_realRepoFindsAddAndRename(t *testing.T) {
	r, dir := testRepo(t) // 3 commits: alpha.txt, beta.txt, gamma.txt
	_ = dir
	idx, err := r.ScanHistoryPaths()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := idx.AddedIn["alpha.txt"]; !ok {
		t.Errorf("alpha.txt should be indexed as added; got %v", idx.AddedIn)
	}
}
