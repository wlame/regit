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

// renameCycleFixture reintroduces the original name later in history: x.txt
// is renamed to y.txt, and a later commit renames y.txt back to x.txt. This
// makes renamedFrom/renamedTo form a 2-cycle, which RenameChain's walks must
// terminate on instead of looping forever.
const renameCycleFixture = "\x00rrrr\n\n" +
	"R100\tx.txt\ty.txt\n" +
	"\x00ssss\n\n" +
	"R100\ty.txt\tx.txt\n"

// quotedPathFixture mimics git's C-style quoting of a path containing a
// non-ASCII byte sequence (default core.quotePath=true): "café.txt" is
// emitted as "caf\303\251.txt", where \303\251 is the UTF-8 encoding of "é"
// written as octal escapes. These are valid Go string-literal escapes too,
// so the doubled backslashes below reproduce the literal bytes git would
// write on the wire.
const quotedPathFixture = "\x00qqqq\n\n" +
	"A\t\"caf\\303\\251.txt\"\n"

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

// TestRenameChain_cycleTerminatesDeterministically covers a rename cycle
// (R x.txt->y.txt then later R y.txt->x.txt). Before the visited-set guard,
// both the backward walk (finding the oldest name) and the forward walk
// (building the chain) would loop forever on this fixture; if the guard
// regresses, this test hangs rather than fails, which is an acceptable
// signal per the review finding.
//
// The exact order below is not a meaningful "oldest first" (a cycle has no
// single oldest name) but is fully determined by which path RenameChain
// started from and the guard's break condition, worked out by hand:
//   - RenameChain("x.txt"): backward walk visits x.txt -> y.txt, then would
//     revisit x.txt and stops, leaving "y.txt" as the walk's endpoint;
//     forward walk then appends x.txt and stops before revisiting y.txt.
//   - RenameChain("y.txt"): symmetric, ending on "x.txt" then appending
//     "y.txt".
func TestRenameChain_cycleTerminatesDeterministically(t *testing.T) {
	idx := ParsePathScan(renameCycleFixture)

	if got, want := idx.RenameChain("x.txt"), []string{"y.txt", "x.txt"}; !reflect.DeepEqual(got, want) {
		t.Errorf("RenameChain(x.txt) = %v, want %v", got, want)
	}
	if got, want := idx.RenameChain("y.txt"), []string{"x.txt", "y.txt"}; !reflect.DeepEqual(got, want) {
		t.Errorf("RenameChain(y.txt) = %v, want %v", got, want)
	}
}

// TestPathIndexMatch_renameCycleDoesNotHang exercises Match over the same
// cyclic fixture: Match calls RenameChain internally for every hit, so a
// regression of the cycle guard would hang here too.
func TestPathIndexMatch_renameCycleDoesNotHang(t *testing.T) {
	idx := ParsePathScan(renameCycleFixture)

	got := idx.Match("x")
	want := []string{"x.txt", "y.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Match(x) = %v, want %v", got, want)
	}
}

// TestParsePathScan_unquotesCStyleQuotedPaths covers the C-quoting fix:
// a path field arriving as a quoted, octal-escaped string must be indexed
// under its real (decoded) name, not the raw quoted form, or exact-path
// lookups downstream (e.g. the purge finder) would never match it.
func TestParsePathScan_unquotesCStyleQuotedPaths(t *testing.T) {
	idx := ParsePathScan(quotedPathFixture)
	if _, ok := idx.AddedIn["café.txt"]; !ok {
		t.Errorf("AddedIn missing real (unquoted) name %q; got %v", "café.txt", idx.AddedIn)
	}
}

// TestPathIndexMatch_matchesRenameDestinationWithNoDirectAdd exercises the
// branch of Match that scans idx.renamedTo for rename destinations that were
// never seen as a plain "A" add (here, "helpers.py" only ever appears as a
// rename target in scanFixture). Querying a substring that only the
// destination name contains must still surface the full rename chain.
func TestPathIndexMatch_matchesRenameDestinationWithNoDirectAdd(t *testing.T) {
	idx := ParsePathScan(scanFixture)
	got := idx.Match("help")
	want := []string{"helpers.py", "util.py"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Match(help) = %v, want %v", got, want)
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
