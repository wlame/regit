package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/model"
)

func detailCommit() model.Commit {
	d := time.Date(2026, 3, 2, 10, 14, 0, 0, time.UTC)
	return model.Commit{
		Hash:           "a1b2c3d4e5f600000000000000000000000000aa",
		AuthorName:     "wlame",
		AuthorEmail:    "v@gmail.com",
		CommitterName:  "wlame",
		CommitterEmail: "v@gmail.com",
		OrigAuthorDate: d,
		OrigCommitDate: d,
		Subject:        "Fix login bug",
		Body:           "Reset the session on logout.",
	}
}

// TestRenderDetail_stripsTerminalEscapesFromRepoContent proves untrusted commit
// fields (subject, body, author, file path) cannot smuggle an ANSI/OSC escape
// through the detail panel. regit itself never emits OSC (ESC ]), so any OSC in the
// output would be an injected one; the raw ESC that introduces the payload must be
// gone entirely.
func TestRenderDetail_stripsTerminalEscapesFromRepoContent(t *testing.T) {
	c := detailCommit()
	c.Subject = "Fix bug\x1b]0;HACKED\x07"    // OSC window-title spoof
	c.Body = "body\x1b]52;c;ZXZpbA==\x07line" // OSC-52 clipboard write
	c.AuthorName = "Ann\x1b[31mMalory"        // CSI color
	plan := model.NewPlan([]model.Commit{c})
	st := gitio.Stat{Files: []gitio.FileStat{{Status: "M", Path: "a\x1b]0;x\x07b.go", Add: 1, Del: 0}}}

	out := renderDetail(c, st, true, "", plan, time.UTC, 60, 40, newStyles())

	if strings.Contains(out, "\x1b]") {
		t.Errorf("detail leaked an OSC escape sequence from repo content:\n%q", out)
	}
	// Text OUTSIDE the escape survives; the OSC payloads (title + clipboard) are
	// gone entirely because they are part of the stripped sequence.
	for _, want := range []string{"Fix bug", "AnnMalory", "b.go"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected sanitized text %q in output:\n%s", want, out)
		}
	}
	for _, gone := range []string{"HACKED", "ZXZpbA=="} {
		if strings.Contains(out, gone) {
			t.Errorf("escape payload %q should have been stripped with its sequence:\n%q", gone, out)
		}
	}
}

func TestRenderDetail_showsStatAndMessage(t *testing.T) {
	c := detailCommit()
	plan := model.NewPlan([]model.Commit{c})
	st := gitio.Stat{
		Files: []gitio.FileStat{
			{Status: "M", Path: "api/login.go", Add: 30, Del: 12},
			{Status: "A", Path: "api/session.go", Add: 12, Del: 0},
		},
		Additions: 42,
		Deletions: 12,
	}
	out := renderDetail(c, st, true, "", plan, time.UTC, 40, 40, newStyles())

	for _, want := range []string{
		"a1b2c3d", "wlame", "v@gmail.com",
		"2 files", "·", "+42", "12", // diffstat header ("N files · +X −Y")
		"api/login.go", "api/session.go",
		"Fix login bug", "Reset the session on logout.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("detail should contain %q, got:\n%s", want, out)
		}
	}
}

func TestRenderDetail_loadingAndError(t *testing.T) {
	c := detailCommit()
	plan := model.NewPlan([]model.Commit{c})

	loading := renderDetail(c, gitio.Stat{}, false, "", plan, time.UTC, 40, 40, newStyles())
	if !strings.Contains(loading, "loading") {
		t.Errorf("missing stat should show a loading hint, got:\n%s", loading)
	}

	failed := renderDetail(c, gitio.Stat{}, false, "boom", plan, time.UTC, 40, 40, newStyles())
	if !strings.Contains(failed, "unavailable") {
		t.Errorf("stat error should show an unavailable note, got:\n%s", failed)
	}
	// The message must still render even when the stat failed.
	if !strings.Contains(failed, "Fix login bug") {
		t.Errorf("message should render despite stat error, got:\n%s", failed)
	}
}

// When a pending edit moves the author date, the panel shows a "→" arrow line
// with the new (effective) date alongside the original.
func TestRenderDetail_showsEditedDateArrow(t *testing.T) {
	c := detailCommit()
	plan := model.NewPlan([]model.Commit{c})

	// Apply a date edit so EffectiveAuthorDate differs from OrigAuthorDate.
	// NewAuthorDate is a *time.Time so nil means "unchanged"; here we point it
	// at a deliberately different day.
	newDate := time.Date(2026, 5, 20, 9, 30, 0, 0, time.UTC)
	plan.SetEdit(c.Hash, model.Edit{NewAuthorDate: &newDate})

	out := renderDetail(c, gitio.Stat{}, false, "", plan, time.UTC, 40, 40, newStyles())

	if !strings.Contains(out, "→") {
		t.Errorf("edited date should render a → arrow line, got:\n%s", out)
	}
	// The effective (new) date must appear in the panel.
	if !strings.Contains(out, "2026-05-20 09:30") {
		t.Errorf("edited date should show the new value, got:\n%s", out)
	}
}

// The committer line only renders when the committer differs from the author;
// detailCommit() keeps them equal, so this fixture diverges them on purpose.
func TestRenderDetail_showsCommitterWhenDifferent(t *testing.T) {
	c := detailCommit()
	c.CommitterName = "rebot"
	c.CommitterEmail = "rebot@ci.example"
	plan := model.NewPlan([]model.Commit{c})

	out := renderDetail(c, gitio.Stat{}, false, "", plan, time.UTC, 40, 40, newStyles())

	for _, want := range []string{"committer", "rebot", "rebot@ci.example"} {
		if !strings.Contains(out, want) {
			t.Errorf("differing committer should render %q, got:\n%s", want, out)
		}
	}
}

func TestClampLines_truncatesWithEllipsis(t *testing.T) {
	lines := []string{"a", "b", "c", "d", "e"}
	out := clampLines(lines, 3)
	got := strings.Split(out, "\n")
	if len(got) != 3 {
		t.Fatalf("clampLines(_,3) = %d lines, want 3", len(got))
	}
	if got[2] != "…" {
		t.Errorf("last kept line should be the ellipsis, got %q", got[2])
	}
	if clampLines(lines, 10) != "a\nb\nc\nd\ne" {
		t.Errorf("clampLines within budget should pass through unchanged")
	}
}

func TestRenderDetail_boundsFileListAndKeepsMessage(t *testing.T) {
	c := detailCommit()
	plan := model.NewPlan([]model.Commit{c})
	var files []gitio.FileStat
	for i := 0; i < 40; i++ {
		files = append(files, gitio.FileStat{Status: "M", Path: fmt.Sprintf("pkg/file%02d.go", i), Add: 1, Del: 1})
	}
	st := gitio.Stat{Files: files, Additions: 40, Deletions: 40}
	const maxH = 14
	out := renderDetail(c, st, true, "", plan, time.UTC, 40, maxH, newStyles())

	if got := strings.Count(out, "\n") + 1; got > maxH {
		t.Fatalf("detail rendered %d lines, exceeds maxHeight %d:\n%s", got, maxH, out)
	}
	if !strings.Contains(out, "more files") {
		t.Errorf("overflowing file list should show a '… +N more files' line, got:\n%s", out)
	}
	if !strings.Contains(out, "Fix login bug") {
		t.Errorf("the message must stay visible when the file list overflows, got:\n%s", out)
	}
}
