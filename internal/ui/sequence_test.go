package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wlame/regit/internal/config"
	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/safety"
)

// modelWithAuthorDates builds a repo with one commit per date (oldest first)
// and returns a Model on it. Out-of-order dates model history after a rebase
// or cherry-pick, which keep each commit's original author date.
func modelWithAuthorDates(t *testing.T, dates ...string) Model {
	t.Helper()
	fixtureMu.Lock()
	defer fixtureMu.Unlock()
	dir := t.TempDir()
	git := func(env []string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = hermeticGitEnv(dir, env...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	git(nil, "init", "-q")
	for i, date := range dates {
		name := string(rune('a' + i))
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
		git(nil, "add", ".")
		git([]string{"GIT_AUTHOR_DATE=" + date, "GIT_COMMITTER_DATE=2026-11-01T10:00:00Z"},
			"commit", "-q", "-m", "Commit "+name+".")
	}
	repo, err := gitio.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	state, _ := repo.ReadState()
	commits, _ := repo.ReadCommits()
	cfg := config.Default()
	cfg.Timezone = "UTC"
	return resizeTo(New(repo, safety.NewManager(repo), cfg, state, commits), 120, 40)
}

// The third commit is authored before its parent, as after a rebase.
var historyWithOldDisorder = []string{
	"2026-10-01T10:00:00Z", "2026-10-05T10:00:00Z", "2026-10-03T10:00:00Z", "2026-10-07T10:00:00Z",
}

// TestEditor_oldOutOfOrderDatesDoNotBlockAnUnrelatedEdit: a pair of commits
// that were already out of date order, and that no edit touches, must not
// block applying an edit elsewhere.
func TestEditor_oldOutOfOrderDatesDoNotBlockAnUnrelatedEdit(t *testing.T) {
	m := modelWithAuthorDates(t, historyWithOldDisorder...)
	m = send(m, "e") // the newest commit
	m.editor.title.SetValue("Renamed.")
	m = send(m, "enter")

	m = send(m, "A")

	if m.mode != modePipeline {
		t.Fatalf("A should open the apply preview, got mode %v, status %q", m.mode, m.statusMsg)
	}
}

// TestEditor_dateEditBeforeItsParentStillBlocks: an edit that itself puts a
// commit before its parent must still be refused.
func TestEditor_dateEditBeforeItsParentStillBlocks(t *testing.T) {
	m := modelWithAuthorDates(t, historyWithOldDisorder...)
	m = send(m, "e") // the newest commit, parent dated 2026-10-03
	m.editor.date.SetValue("2026-10-02 10:00")
	m = send(m, "enter")

	m = send(m, "A")

	if m.mode == modePipeline || !strings.Contains(m.statusMsg, "before its parent") {
		t.Fatalf("a date edit before the parent should block, got mode %v, status %q", m.mode, m.statusMsg)
	}
}

// TestEditor_oldDisorderShowsNoShiftWarning: the ⚠ shift marks are for
// commits an edit would move, not for untouched old disorder.
func TestEditor_oldDisorderShowsNoShiftWarning(t *testing.T) {
	m := modelWithAuthorDates(t, historyWithOldDisorder...)
	m = send(m, "e")
	m.editor.title.SetValue("Renamed.")
	m = send(m, "enter")

	if len(m.plan.Shifted) != 0 {
		t.Errorf("untouched commits flagged as shifted: %v", m.plan.Shifted)
	}
}
