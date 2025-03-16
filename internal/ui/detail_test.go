package ui

import (
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
	out := renderDetail(c, st, true, "", plan, time.UTC, 40, newStyles())

	for _, want := range []string{
		"a1b2c3d", "wlame", "v@gmail.com",
		"2 files", "+42", "12", // diffstat header
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

	loading := renderDetail(c, gitio.Stat{}, false, "", plan, time.UTC, 40, newStyles())
	if !strings.Contains(loading, "loading") {
		t.Errorf("missing stat should show a loading hint, got:\n%s", loading)
	}

	failed := renderDetail(c, gitio.Stat{}, false, "boom", plan, time.UTC, 40, newStyles())
	if !strings.Contains(failed, "unavailable") {
		t.Errorf("stat error should show an unavailable note, got:\n%s", failed)
	}
	// The message must still render even when the stat failed.
	if !strings.Contains(failed, "Fix login bug") {
		t.Errorf("message should render despite stat error, got:\n%s", failed)
	}
}
