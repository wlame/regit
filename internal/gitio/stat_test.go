package gitio

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wlame/regit/internal/gittest"
)

// statRepo builds a throwaway repo whose HEAD commit adds one file, modifies
// another, deletes a third, and writes a binary blob — exercising every status
// CommitStat reports.
func statRepo(t *testing.T) *Repo {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = gittest.Env(dir,
			"GIT_AUTHOR_DATE=2026-06-05T14:00:00+00:00",
			"GIT_COMMITTER_DATE=2026-06-05T14:00:00+00:00")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-q")
	run("symbolic-ref", "HEAD", "refs/heads/main")
	run("config", "user.name", "wlame")
	run("config", "user.email", "vvlame@gmail.com")
	run("config", "commit.gpgsign", "false")

	// First commit: keep.txt (modified later) and gone.txt (deleted later).
	write("keep.txt", "one\ntwo\n")
	write("gone.txt", "remove me\n")
	run("add", ".")
	run("commit", "-q", "-m", "seed")

	// HEAD commit: add new.txt, modify keep.txt, delete gone.txt, add a binary.
	write("new.txt", "a\nb\nc\n")
	write("keep.txt", "one\ntwo\nthree\n")
	if err := os.Remove(filepath.Join(dir, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	write("logo.bin", "\x00\x01\x02\x00\xff")
	run("add", "-A")
	run("commit", "-q", "-m", "change a bunch")

	repo, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return repo
}

func TestCommitStat_reportsPerFileChanges(t *testing.T) {
	repo := statRepo(t)
	head, err := repo.ResolveRef("HEAD")
	if err != nil {
		t.Fatal(err)
	}
	st, err := repo.CommitStat(head)
	if err != nil {
		t.Fatalf("CommitStat: %v", err)
	}

	byPath := map[string]FileStat{}
	for _, f := range st.Files {
		byPath[f.Path] = f
	}

	if got := byPath["new.txt"]; got.Status != "A" || got.Add != 3 || got.Del != 0 {
		t.Errorf("new.txt = %+v, want A +3 -0", got)
	}
	if got := byPath["keep.txt"]; got.Status != "M" || got.Add != 1 || got.Del != 0 {
		t.Errorf("keep.txt = %+v, want M +1 -0", got)
	}
	if got := byPath["gone.txt"]; got.Status != "D" || got.Add != 0 || got.Del != 1 {
		t.Errorf("gone.txt = %+v, want D +0 -1", got)
	}
	if got := byPath["logo.bin"]; !got.Binary || got.Status != "A" {
		t.Errorf("logo.bin = %+v, want Binary status A", got)
	}

	// Totals exclude the binary file (counts unknown for binaries).
	if st.Additions != 4 || st.Deletions != 1 {
		t.Errorf("totals = +%d -%d, want +4 -1", st.Additions, st.Deletions)
	}
}

func TestCommitStat_rootCommitDiffsAgainstEmptyTree(t *testing.T) {
	repo := statRepo(t)
	// The seed commit is a root commit; its stat is the full add of both files.
	rootRef, err := repo.ResolveRef("HEAD~1")
	if err != nil {
		t.Fatal(err)
	}
	st, err := repo.CommitStat(rootRef)
	if err != nil {
		t.Fatalf("CommitStat root: %v", err)
	}
	if len(st.Files) != 2 {
		t.Fatalf("root commit should show 2 added files, got %d", len(st.Files))
	}
	for _, f := range st.Files {
		if f.Status != "A" {
			t.Errorf("root file %s status = %q, want A", f.Path, f.Status)
		}
	}
}

func TestParseNumstat_handlesBinaryMarker(t *testing.T) {
	rows := parseNumstat("3\t1\tsrc/a.go\n-\t-\tlogo.bin\n")
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d", len(rows))
	}
	if rows[0] != (numstatRow{add: 3, del: 1, path: "src/a.go"}) {
		t.Errorf("text row = %+v", rows[0])
	}
	if !rows[1].binary || rows[1].path != "logo.bin" {
		t.Errorf("binary row = %+v, want binary logo.bin", rows[1])
	}
}

// TestParseNumstat_skipsMalformedLine pins the defensive skip: a line that
// doesn't split into exactly 3 tab-separated fields (never expected from real
// `git show --numstat` output, but the parser must not panic or misattribute
// fields if it ever saw one) is dropped rather than crash or produce a
// half-populated row.
func TestParseNumstat_skipsMalformedLine(t *testing.T) {
	rows := parseNumstat("3\t1\tsrc/a.go\nmalformed-line-no-tabs\n1\t0\tsrc/b.go\n")
	if len(rows) != 2 {
		t.Fatalf("want 2 rows (malformed line skipped), got %d: %+v", len(rows), rows)
	}
	if rows[0].path != "src/a.go" || rows[1].path != "src/b.go" {
		t.Errorf("rows = %+v, want a.go then b.go", rows)
	}
}

// TestParseNameStatus_skipsMalformedLine mirrors
// TestParseNumstat_skipsMalformedLine for the --name-status parser.
func TestParseNameStatus_skipsMalformedLine(t *testing.T) {
	byPath := parseNameStatus("M\tsrc/a.go\nmalformed-no-tab\nA\tsrc/b.go\n")
	if len(byPath) != 2 {
		t.Fatalf("want 2 entries (malformed line skipped), got %d: %+v", len(byPath), byPath)
	}
	if byPath["src/a.go"] != "M" || byPath["src/b.go"] != "A" {
		t.Errorf("byPath = %+v, want a.go=M b.go=A", byPath)
	}
}

// TestCommitStat_errorsOnUnresolvableHash pins the numstat query's own
// failure path.
func TestCommitStat_errorsOnUnresolvableHash(t *testing.T) {
	repo := statRepo(t)
	if _, err := repo.CommitStat("0000000000000000000000000000000000dead"); err == nil {
		t.Fatal("CommitStat on an unresolvable hash should fail, got nil error")
	}
}
