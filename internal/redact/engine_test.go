package redact

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/gittest"
	"github.com/wlame/regit/internal/model"
)

// gitT runs a git command against dir inside the hermetic gittest
// environment, failing the test on any error. Real git, no mocks — this
// mirrors every other engine package's fixture helper (see
// internal/squash's linearDiffRepo), per the project's testing standards.
func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = gittest.Env(dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// mustWriteFile writes content (which may hold raw NUL bytes, for the binary
// fixture) to name under dir, creating parent directories as needed.
func mustWriteFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// openRepo creates a throwaway git repo rooted at a fresh temp dir and opens
// it as a gitio.Repo.
func openRepo(t *testing.T) (*gitio.Repo, string) {
	t.Helper()
	dir := t.TempDir()
	gitT(t, dir, "init", "-q", "-b", "main")
	r, err := gitio.Open(dir)
	if err != nil {
		t.Fatalf("gitio.Open: %v", err)
	}
	return r, dir
}

// renameRotateRepo builds the 3-commit fixture shared by the cascade Scan
// tests and the Plan test: c1 adds cfg.yaml with the secret line plus two
// neighbor lines, c2 renames the file with NO content change, c3 rotates the
// line AT THE RENAMED PATH. The occurrence's single descendant therefore
// lands under a path different from the intro's — exactly what exercises
// Plan's "a descendant contributes at its own (possibly renamed) path"
// contract.
func renameRotateRepo(t *testing.T) (*gitio.Repo, []model.Commit) {
	t.Helper()
	r, dir := openRepo(t)
	mustWriteFile(t, dir, "cfg.yaml", "before\ntoken: sk-OLD\nafter\n")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "c1 add secret")
	if err := os.MkdirAll(filepath.Join(dir, "conf"), 0o755); err != nil {
		t.Fatal(err)
	}
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

// duplicateLineRepo builds a single-commit fixture that adds a NEW file
// containing the same secret-matching line twice. A new file's whole
// addition is one hunk, so this produces two SecretHits sharing an IDENTICAL
// (Commit, Path, Line) triple — the shape Scan's dedup must collapse into one
// occurrence.
func duplicateLineRepo(t *testing.T) (*gitio.Repo, []model.Commit) {
	t.Helper()
	r, dir := openRepo(t)
	mustWriteFile(t, dir, "cfg.yaml", "token: sk-DUP\nother\ntoken: sk-DUP\n")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "c1 add duplicate lines")
	commits, err := r.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 1 {
		t.Fatalf("setup: want 1 commit, got %d", len(commits))
	}
	return r, commits
}

// sameLineTwoPathsRepo builds a single-commit fixture that adds the SAME
// secret-matching line to TWO different new files. The dedup key is
// (Commit, Path, Line), so a shared Commit and Line with different Path
// must NOT collapse — this is the discriminating counterpart to
// duplicateLineRepo, which pins the "identical triple collapses" half of
// the contract.
func sameLineTwoPathsRepo(t *testing.T) (*gitio.Repo, []model.Commit) {
	t.Helper()
	r, dir := openRepo(t)
	mustWriteFile(t, dir, "cfg.yaml", "token: sk-SHARED\n")
	mustWriteFile(t, dir, "other.yaml", "token: sk-SHARED\n")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "c1 add the same line to two files")
	commits, err := r.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 1 {
		t.Fatalf("setup: want 1 commit, got %d", len(commits))
	}
	return r, commits
}

// binarySecretRepo builds a single-commit fixture whose file holds a NUL
// byte AND the secret literal — the shape SecretAdditions' text-hunk parser
// can never see a match inside (git renders it as "Binary files ... differ",
// no hunks), so Scan must surface it via ScanResult.Binary instead of
// silently losing it.
func binarySecretRepo(t *testing.T) *gitio.Repo {
	t.Helper()
	r, dir := openRepo(t)
	mustWriteFile(t, dir, "bin.dat", "before\x00sk-BINARY\x00after")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "add binary secret")
	return r
}

// mergedSideBranchRepo builds a fixture where the commit introducing the
// secret is merged into main via an ORDINARY (non-first-parent) merge: it is
// reachable from HEAD (SecretAdditions walks ALL parents, so it will be
// found) but is NOT on HEAD's first-parent chain — the exact shape
// gitio.LineDescendants rejects via ErrLineOffFirstParentChain, and the seam
// this package's Scan must absorb instead of aborting the whole scan over.
func mergedSideBranchRepo(t *testing.T) *gitio.Repo {
	t.Helper()
	r, dir := openRepo(t)
	mustWriteFile(t, dir, "keep.txt", "v1")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "c0 keep only")
	gitT(t, dir, "checkout", "-q", "-b", "side")
	mustWriteFile(t, dir, "cfg.yaml", "before\ntoken: sk-SIDE\nafter\n")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "side adds secret")
	gitT(t, dir, "checkout", "-q", "main")
	gitT(t, dir, "merge", "--no-ff", "-q", "-m", "merge side", "side")
	mustWriteFile(t, dir, "keep.txt", "v2")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "main continues")
	return r
}

// corruptedObjectRepo builds a 2-commit fixture where c1 adds the secret to
// cfg.yaml and c2 BOTH rotates that same line (a genuine edit LineDescendants
// would otherwise report as a real descendant) AND adds an unrelated new
// file, extra.txt — then removes the loose object backing extra.txt from the
// repository's own object store. This reproduces a real (if rare) git
// failure mode — a partial clone or a corrupted object store — where commit
// metadata stays fully readable but a blob-content operation fails outright:
// `rev-list`, which both of LineDescendants' first-parent chain checks rely
// on, never reads blob content, so it keeps working; `git log -S` SCOPED to
// cfg.yaml (via the test's Globs) never touches extra.txt either, so
// SecretAdditions succeeds and hands cascade a real hit. LineDescendants'
// own commit-pair diff, though, is deliberately NOT pathspec-restricted (see
// its doc comment — full-tree diffing is what makes rename-following work),
// so it still needs extra.txt's content to render the c1..c2 diff and fails
// on the missing blob. This is the only way found to drive LineDescendants
// to a NON-sentinel error through Scan's real git plumbing — any commit that
// is simply absent from, or off, the first-parent chain always surfaces as
// ErrLineOffFirstParentChain instead (see mergedSideBranchRepo), so that
// path cannot exercise this one.
func corruptedObjectRepo(t *testing.T) *gitio.Repo {
	t.Helper()
	r, dir := openRepo(t)
	mustWriteFile(t, dir, "cfg.yaml", "before\ntoken: sk-CORRUPT\nafter\n")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "c1 add secret")
	mustWriteFile(t, dir, "cfg.yaml", "before\ntoken: sk-CORRUPT-ROTATED\nafter\n")
	mustWriteFile(t, dir, "extra.txt", "irrelevant\n")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "c2 rotate secret and add unrelated file")

	blob := gitT(t, dir, "rev-parse", "HEAD:extra.txt")
	objPath := filepath.Join(dir, ".git", "objects", blob[:2], blob[2:])
	if err := os.Remove(objPath); err != nil {
		t.Fatalf("remove loose object %s (fixture assumes it is unpacked): %v", objPath, err)
	}
	return r
}

func TestScan_buildsOccurrenceWithCascade(t *testing.T) {
	r, commits := renameRotateRepo(t)
	eng := Engine{Repo: r}

	res, err := eng.Scan(ScanOptions{Match: "sk-OLD", Cascade: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Occurrences) != 1 {
		t.Fatalf("got %d occurrences, want 1: %+v", len(res.Occurrences), res.Occurrences)
	}
	occ := res.Occurrences[0]
	wantIntro := occ.Intro.Commit == commits[0].Hash && occ.Intro.Path == "cfg.yaml" &&
		occ.Intro.Line == "token: sk-OLD"
	if !wantIntro {
		t.Errorf("Intro = %+v, want commit=c1 path=cfg.yaml line=%q", occ.Intro, "token: sk-OLD")
	}
	joined := strings.Join(occ.Context, "\n")
	if !strings.Contains(joined, "before") || !strings.Contains(joined, "after") {
		t.Errorf("Context = %v, want it to contain the neighbor lines before/after", occ.Context)
	}
	if occ.ChainNote != "" {
		t.Errorf("ChainNote = %q, want empty (a clean rotation)", occ.ChainNote)
	}
	if len(occ.Descendants) != 1 {
		t.Fatalf("got %d descendants, want 1: %+v", len(occ.Descendants), occ.Descendants)
	}
	d := occ.Descendants[0]
	if d.Commit != commits[2].Hash || d.Path != "conf/cfg.yaml" || d.Line != "token: sk-NEW" {
		t.Errorf("Descendant = %+v, want commit=c3 path=conf/cfg.yaml line=%q", d, "token: sk-NEW")
	}
}

func TestScan_noCascadeSkipsDescendants(t *testing.T) {
	r, _ := renameRotateRepo(t)
	eng := Engine{Repo: r}

	res, err := eng.Scan(ScanOptions{Match: "sk-OLD", Cascade: false})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Occurrences) != 1 {
		t.Fatalf("got %d occurrences, want 1", len(res.Occurrences))
	}
	occ := res.Occurrences[0]
	if len(occ.Descendants) != 0 {
		t.Errorf("Descendants = %+v, want none: --no-cascade must skip LineDescendants entirely",
			occ.Descendants)
	}
	if occ.ChainNote != "" {
		t.Errorf("ChainNote = %q, want empty (cascade was never attempted)", occ.ChainNote)
	}
}

func TestScan_dedupesIdenticalIntroTriple(t *testing.T) {
	r, commits := duplicateLineRepo(t)
	eng := Engine{Repo: r}

	res, err := eng.Scan(ScanOptions{Match: "sk-DUP", Cascade: false})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Occurrences) != 1 {
		t.Fatalf("got %d occurrences, want 1 (the identical Commit/Path/Line triple must collapse): %+v",
			len(res.Occurrences), res.Occurrences)
	}
	if res.Occurrences[0].Intro.Commit != commits[0].Hash {
		t.Errorf("Intro.Commit = %s, want c1 %s", res.Occurrences[0].Intro.Commit, commits[0].Hash)
	}
}

// TestScan_sameLineDifferentPathsStayDistinct is the discriminating
// counterpart to TestScan_dedupesIdenticalIntroTriple: the SAME commit
// adding the SAME line to two DIFFERENT paths must keep both occurrences,
// since Path is part of the dedup key. A dedup keyed on (Commit, Line) alone
// would wrongly collapse this to one and silently drop a real occurrence.
func TestScan_sameLineDifferentPathsStayDistinct(t *testing.T) {
	r, commits := sameLineTwoPathsRepo(t)
	eng := Engine{Repo: r}

	res, err := eng.Scan(ScanOptions{Match: "sk-SHARED", Cascade: false})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Occurrences) != 2 {
		t.Fatalf("got %d occurrences, want 2 (same commit/line, different path, must NOT collapse): %+v",
			len(res.Occurrences), res.Occurrences)
	}
	gotPaths := map[string]bool{}
	for _, occ := range res.Occurrences {
		if occ.Intro.Commit != commits[0].Hash {
			t.Errorf("Intro.Commit = %s, want c1 %s", occ.Intro.Commit, commits[0].Hash)
		}
		if occ.Intro.Line != "token: sk-SHARED" {
			t.Errorf("Intro.Line = %q, want %q", occ.Intro.Line, "token: sk-SHARED")
		}
		gotPaths[occ.Intro.Path] = true
	}
	wantPaths := map[string]bool{"cfg.yaml": true, "other.yaml": true}
	if !reflect.DeepEqual(gotPaths, wantPaths) {
		t.Errorf("paths = %v, want %v", gotPaths, wantPaths)
	}
}

func TestScan_reportsBinaryHitsSeparately(t *testing.T) {
	r := binarySecretRepo(t)
	eng := Engine{Repo: r}

	res, err := eng.Scan(ScanOptions{Match: "sk-BINARY", Cascade: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Occurrences) != 0 {
		t.Errorf("Occurrences = %+v, want none (the only hit lives inside a binary blob)",
			res.Occurrences)
	}
	if len(res.Binary) != 1 {
		t.Fatalf("got %d binary reports, want 1: %+v", len(res.Binary), res.Binary)
	}
	if !strings.HasPrefix(res.Binary[0], "bin.dat @ ") {
		t.Errorf("Binary[0] = %q, want it to start with %q", res.Binary[0], "bin.dat @ ")
	}
}

// TestScan_offFirstParentChainHitSkipsCascadeWithNote covers the seam where
// LineDescendants errors (via gitio.ErrLineOffFirstParentChain) when asked
// to cascade a hit whose commit is off HEAD's first-parent chain. Scan must
// absorb that as a per-occurrence note, not let it abort the entire scan.
func TestScan_offFirstParentChainHitSkipsCascadeWithNote(t *testing.T) {
	r := mergedSideBranchRepo(t)
	eng := Engine{Repo: r}

	res, err := eng.Scan(ScanOptions{Match: "sk-SIDE", Cascade: true})
	if err != nil {
		t.Fatalf("Scan must not abort over one off-first-parent-chain hit: %v", err)
	}
	if len(res.Occurrences) != 1 {
		t.Fatalf("got %d occurrences, want 1: %+v", len(res.Occurrences), res.Occurrences)
	}
	occ := res.Occurrences[0]
	if occ.Intro.Path != "cfg.yaml" || occ.Intro.Line != "token: sk-SIDE" {
		t.Errorf("Intro = %+v, want path=cfg.yaml line=%q", occ.Intro, "token: sk-SIDE")
	}
	if len(occ.Descendants) != 0 {
		t.Errorf("Descendants = %+v, want none: a side-branch commit cannot be cascaded", occ.Descendants)
	}
	if occ.ChainNote == "" {
		t.Fatal("ChainNote should explain the occurrence could not be cascaded")
	}
}

// TestScan_nonSentinelCascadeErrorAborts is the abort-side counterpart to
// TestScan_offFirstParentChainHitSkipsCascadeWithNote: a LineDescendants
// failure that is NOT the off-chain sentinel (here, a genuine git failure
// from a missing object) must still abort Scan — the errors.Is branch in
// cascade must not swallow it the way it swallows the sentinel case.
//
// Globs scopes the pickaxe search to cfg.yaml so SecretAdditions itself
// succeeds without ever touching extra.txt's corrupted blob (verified
// against real git: an UNSCOPED `git log -S` must read every blob any hit
// commit changed, including extra.txt, and fails there instead) — the
// failure under test must come from LineDescendants' own unrestricted
// commit-pair diff, not from the pickaxe search that runs before cascade is
// ever reached. The message-prefix check below pins that distinction so a
// future regression that moved the failure back to SecretAdditions can't
// silently pass this test for the wrong reason.
func TestScan_nonSentinelCascadeErrorAborts(t *testing.T) {
	r := corruptedObjectRepo(t)
	eng := Engine{Repo: r}

	_, err := eng.Scan(ScanOptions{
		Match:   "sk-CORRUPT",
		Globs:   []string{"cfg.yaml"},
		Cascade: true,
	})
	if err == nil {
		t.Fatal("Scan should abort when LineDescendants fails for a reason " +
			"other than the off-chain sentinel")
	}
	if errors.Is(err, gitio.ErrLineOffFirstParentChain) {
		t.Errorf("err = %v, want a genuine git failure, not the off-chain sentinel", err)
	}
	if !strings.HasPrefix(err.Error(), "diff ") {
		t.Errorf("err = %q, want it to carry LineDescendants' own \"diff <a>..<b>: ...\" "+
			"wrapper — a \"pickaxe search\" prefix here would mean the failure came from "+
			"SecretAdditions and cascade/LineDescendants was never even reached", err)
	}
	if strings.Contains(err.Error(), "pickaxe search") {
		t.Errorf("err = %q, want no \"pickaxe search\" prefix: that would mean "+
			"SecretAdditions failed before cascade ran, not the branch this test targets", err)
	}
}

func TestPlan_selectionFlattensToTargets(t *testing.T) {
	r, _ := renameRotateRepo(t)
	eng := Engine{Repo: r}
	res, err := eng.Scan(ScanOptions{Match: "sk-OLD", Cascade: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Occurrences) != 1 || len(res.Occurrences[0].Descendants) != 1 {
		t.Fatalf("setup: want 1 occurrence with 1 descendant, got %+v", res.Occurrences)
	}

	// Veto the descendant: Targets holds only the intro content.
	vetoSel := []Selection{{Occurrence: true, Descendants: []bool{false}}}
	spec, err := eng.Plan(res, vetoSel, model.RedactRemove, model.RedactRule{}, "sk-OLD")
	if err != nil {
		t.Fatal(err)
	}
	wantVeto := map[string][]string{"cfg.yaml": {"token: sk-OLD"}}
	if !reflect.DeepEqual(spec.Targets, wantVeto) {
		t.Errorf("vetoed Targets = %+v, want %+v", spec.Targets, wantVeto)
	}

	// Check the descendant: both contents present, descendant under its
	// renamed path.
	checkSel := []Selection{{Occurrence: true, Descendants: []bool{true}}}
	spec, err = eng.Plan(res, checkSel, model.RedactRemove, model.RedactRule{}, "sk-OLD")
	if err != nil {
		t.Fatal(err)
	}
	wantBoth := map[string][]string{
		"cfg.yaml":      {"token: sk-OLD"},
		"conf/cfg.yaml": {"token: sk-NEW"},
	}
	if !reflect.DeepEqual(spec.Targets, wantBoth) {
		t.Errorf("checked Targets = %+v, want %+v", spec.Targets, wantBoth)
	}
	if spec.Match != "sk-OLD" {
		t.Errorf("spec.Match = %q, want sk-OLD", spec.Match)
	}
	if spec.Mode != model.RedactRemove {
		t.Errorf("spec.Mode = %v, want RedactRemove", spec.Mode)
	}
}

func TestPlan_nothingSelectedErrors(t *testing.T) {
	r, _ := renameRotateRepo(t)
	eng := Engine{Repo: r}
	res, err := eng.Scan(ScanOptions{Match: "sk-OLD", Cascade: true})
	if err != nil {
		t.Fatal(err)
	}

	sel := []Selection{{Occurrence: false, Descendants: []bool{false}}}
	if _, err := eng.Plan(res, sel, model.RedactRemove, model.RedactRule{}, "sk-OLD"); err == nil {
		t.Fatal("Plan should error when nothing is selected")
	}
}

// TestPlan_selectionLengthMismatchErrors covers the parallel-slice contract:
// sel must have one entry per occurrence, or Plan must fail fast rather than
// index out of range or silently ignore the extra/missing occurrences.
func TestPlan_selectionLengthMismatchErrors(t *testing.T) {
	r, _ := renameRotateRepo(t)
	eng := Engine{Repo: r}
	res, err := eng.Scan(ScanOptions{Match: "sk-OLD", Cascade: true})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := eng.Plan(res, nil, model.RedactRemove, model.RedactRule{}, "sk-OLD"); err == nil {
		t.Fatal("Plan should error when sel has fewer entries than Occurrences")
	}
}
