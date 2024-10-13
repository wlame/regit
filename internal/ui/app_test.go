package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/wlame/ghist/internal/config"
	"github.com/wlame/ghist/internal/gitio"
	"github.com/wlame/ghist/internal/safety"
)

// newTestModel builds a Model backed by a real throwaway repo with three
// clustered commits, sized to a usable terminal.
func newTestModel(t *testing.T) Model {
	t.Helper()
	dir := t.TempDir()
	run := func(env []string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), env...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run(nil, "init", "-q")
	run(nil, "symbolic-ref", "HEAD", "refs/heads/main")
	run(nil, "config", "user.name", "wlame")
	run(nil, "config", "user.email", "vvlame@gmail.com")
	run(nil, "config", "commit.gpgsign", "false")
	date := []string{"GIT_AUTHOR_DATE=2026-06-05T14:00:00+00:00", "GIT_COMMITTER_DATE=2026-06-05T14:00:00+00:00"}
	for _, name := range []string{"a", "b", "c"} {
		os.WriteFile(filepath.Join(dir, name+".txt"), []byte(name+"\n"), 0o644)
		run(nil, "add", ".")
		run(date, "commit", "-q", "-m", "commit "+name, "-m", "body "+name)
	}

	repo, err := gitio.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	state, _ := repo.ReadState()
	commits, _ := repo.ReadCommits()
	cfg := config.Default()
	cfg.Timezone = "UTC"

	m := New(repo, safety.NewManager(repo), cfg, state, commits)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	return updated.(Model)
}

func key(s string) tea.KeyMsg {
	if len(s) == 1 {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func send(m Model, keys ...string) Model {
	for _, k := range keys {
		updated, _ := m.Update(key(k))
		m = updated.(Model)
	}
	return m
}

func TestView_mainRendersWithoutPanic(t *testing.T) {
	m := newTestModel(t)
	out := m.View()
	if !strings.Contains(out, "ghist") {
		t.Error("main view should contain the title")
	}
	if !strings.Contains(out, "COMMITS") {
		t.Error("main view should contain the commit list header")
	}
}

func TestPaletteToParamsToApply_spreadsCommits(t *testing.T) {
	m := newTestModel(t)
	// Open palette (Even Spread is default-selected), enter to configure, enter to apply.
	m = send(m, "s")
	if m.mode != modePalette {
		t.Fatalf("expected palette mode, got %v", m.mode)
	}
	m = send(m, "enter") // -> params
	if m.mode != modeParams {
		t.Fatalf("expected params mode, got %v", m.mode)
	}
	m = send(m, "enter") // apply
	if m.mode != modeMain {
		t.Fatalf("expected return to main, got %v", m.mode)
	}
	if !m.plan.HasChanges() {
		t.Error("applying a strategy should produce edits")
	}
	if !m.plan.IsValid() {
		t.Errorf("spread plan should be sequence-valid, got %d violations", len(m.plan.Violations))
	}
}

func TestEditor_dropBodyMarksEdit(t *testing.T) {
	m := newTestModel(t)
	m = send(m, "e") // open editor on selected commit
	if m.mode != modeEditor {
		t.Fatalf("expected editor mode, got %v", m.mode)
	}
	// ctrl+d drops the body.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	m = updated.(Model)
	if !m.editor.bodyDropped {
		t.Error("ctrl+d should mark the body dropped")
	}
	m = send(m, "enter") // save (focus is on title field, so enter saves)
	if m.mode != modeMain {
		t.Fatalf("editor should close on save, got mode %v", m.mode)
	}
	c := m.commitAtCursor()
	e, ok := m.plan.EditFor(c.Hash)
	if !ok || !e.DropBody {
		t.Error("saved edit should record DropBody")
	}
}

func TestCalendarQuotas_applyViaManualPin(t *testing.T) {
	m := newTestModel(t)
	m = send(m, "tab") // focus calendar
	if m.focus != focusCalendar {
		t.Fatal("tab should focus the calendar")
	}
	// Set a quota of 3 on the cursor day, then apply with 'g'.
	m = send(m, "3", "g")
	if !m.plan.HasChanges() {
		t.Error("applying quotas should produce edits")
	}
}

func TestReset_clearsEdits(t *testing.T) {
	m := newTestModel(t)
	m = send(m, "s", "enter", "enter") // apply even spread
	if !m.plan.HasChanges() {
		t.Fatal("setup: expected edits")
	}
	m = send(m, "r")
	if m.plan.HasChanges() {
		t.Error("reset should clear all edits")
	}
}

func TestApplyAndUndo_roundTrip(t *testing.T) {
	m := newTestModel(t)
	origHead, _ := m.repo.ResolveRef("HEAD")

	m = send(m, "s", "enter", "enter") // spread
	m = send(m, "A")                   // apply (clean local repo, no confirm)
	if m.statusKind == statusErr {
		t.Fatalf("apply errored: %s", m.statusMsg)
	}
	newHead, _ := m.repo.ResolveRef("HEAD")
	if newHead == origHead {
		t.Fatal("apply should have moved HEAD")
	}

	m = send(m, "u") // undo
	restored, _ := m.repo.ResolveRef("HEAD")
	if restored != origHead {
		t.Errorf("undo should restore original HEAD: %s != %s", restored, origHead)
	}
}

func TestHelpToggle(t *testing.T) {
	m := newTestModel(t)
	m = send(m, "?")
	if m.mode != modeHelp {
		t.Fatal("? should open help")
	}
	if !strings.Contains(m.View(), "rewrite commit dates") {
		t.Error("help view should render help content")
	}
	m = send(m, "?")
	if m.mode != modeMain {
		t.Error("? should close help")
	}
}

func TestPreviewOpens(t *testing.T) {
	m := newTestModel(t)
	m = send(m, "s", "enter", "enter", "p")
	if m.mode != modePreview {
		t.Fatal("p should open preview")
	}
	if !strings.Contains(m.View(), "original → new") {
		t.Error("preview should render the diff table")
	}
}
