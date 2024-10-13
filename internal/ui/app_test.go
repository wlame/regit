package ui

import (
	"fmt"
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
	return newTestModelN(t, 3)
}

// newTestModelN is newTestModel with a configurable number of commits, for
// tests that need more history than fits on one screen.
func newTestModelN(t *testing.T, commitCount int) Model {
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
	for i := 0; i < commitCount; i++ {
		name := fmt.Sprintf("c%03d", i)
		os.WriteFile(filepath.Join(dir, name+".txt"), []byte(name+"\n"), 0o644)
		run(nil, "add", ".")
		// Realistic long subjects: anything that wraps inside the list pane
		// inflates entry height and overflows the terminal (regression).
		subject := fmt.Sprintf("Refactor the authentication middleware to support rotation %s", name)
		run(date, "commit", "-q", "-m", subject, "-m", "body "+name)
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

// Regression test: the view must never be taller than the terminal, whatever
// the commit count or subject length. Oversized frames get tail-clipped by
// bubbletea, pushing the title and newest commits out of sight.
func TestView_withManyCommitsFitsTerminalHeight(t *testing.T) {
	for _, tc := range []struct {
		name          string
		commits       int
		width, height int
	}{
		{"many commits, small terminal", 60, 100, 24},
		{"many commits, tall terminal", 60, 120, 40},
		{"few commits", 3, 120, 40},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestModelN(t, tc.commits)
			updated, _ := m.Update(tea.WindowSizeMsg{Width: tc.width, Height: tc.height})
			m = updated.(Model)

			view := m.View()
			lines := strings.Count(view, "\n") + 1
			if lines > tc.height {
				t.Errorf("view renders %d lines but terminal height is %d", lines, tc.height)
			}
			if !strings.Contains(view, "▸") {
				t.Error("cursor should be visible in the rendered list")
			}
		})
	}
}

// Every commit entry renders on exactly one line — date, hash, and subject
// together — and long subjects are truncated rather than wrapped. Wrapping
// inside the pane is what inflated entries to 3 lines and overflowed the UI.
func TestView_commitEntriesAreSingleLine(t *testing.T) {
	m := newTestModelN(t, 60)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	m = updated.(Model)

	view := m.View()
	// plan.Commits is oldest-first; the list displays newest-first.
	newest := m.plan.Commits[len(m.plan.Commits)-1]
	hash8 := newest.Hash[:8]
	var entryLine string
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, hash8) {
			entryLine = line
			break
		}
	}
	if entryLine == "" {
		t.Fatalf("newest commit %s not rendered", hash8)
	}
	if !strings.Contains(entryLine, "06-05 14:00") {
		t.Errorf("entry line should carry the date, got: %q", entryLine)
	}
	if !strings.Contains(entryLine, "Refactor the authentication") {
		t.Errorf("entry line should carry the subject, got: %q", entryLine)
	}

	// One line per commit: the visible window must hold listHeight-1 entries.
	wantVisible := m.listHeight() - 1
	gotVisible := 0
	for _, c := range m.plan.Commits {
		if strings.Contains(view, c.Hash[:8]) {
			gotVisible++
		}
	}
	if gotVisible != wantVisible {
		t.Errorf("want %d single-line entries visible, got %d", wantVisible, gotVisible)
	}
}

// The cursor must stay visible while scrolling far past the first page.
func TestView_cursorStaysVisibleWhenScrolling(t *testing.T) {
	m := newTestModelN(t, 60)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = updated.(Model)

	for i := 0; i < 45; i++ {
		m = send(m, "down")
	}
	view := m.View()
	if !strings.Contains(view, "▸") {
		t.Error("cursor should remain visible after scrolling down 45 entries")
	}
	if lines := strings.Count(view, "\n") + 1; lines > 24 {
		t.Errorf("scrolled view renders %d lines but terminal height is 24", lines)
	}
}

func TestPaletteToParamsToApply_spreadsCommits(t *testing.T) {
	m := newTestModel(t)
	// Open palette (Even Spread is default-selected), enter to configure, enter to apply.
	m = send(m, "s")
	if m.mode != modePalette {
		t.Fatalf("expected palette mode, got %v", m.mode)
	}
	m = send(m, "enter") // -> strategy pane focused with the chosen form
	if m.mode != modeMain || m.focus != focusParams {
		t.Fatalf("choosing a strategy should focus the params pane, got mode %v focus %v", m.mode, m.focus)
	}
	m = send(m, "enter") // apply
	if m.focus != focusList {
		t.Fatalf("successful apply should focus the list, got %v", m.focus)
	}
	if !m.plan.HasChanges() {
		t.Error("applying a strategy should produce edits")
	}
	if !m.plan.IsValid() {
		t.Errorf("spread plan should be sequence-valid, got %d violations", len(m.plan.Violations))
	}
}

// The strategy pane is always present and reachable via the tab cycle:
// list -> calendar -> params -> list.
func TestTabCycle_includesStrategyPane(t *testing.T) {
	m := newTestModel(t)
	if !strings.Contains(m.View(), "STRATEGY") {
		t.Error("main view should render the strategy pane")
	}
	m = send(m, "tab", "tab")
	if m.focus != focusParams {
		t.Fatalf("tab tab should focus the params pane, got %v", m.focus)
	}
	m = send(m, "tab")
	if m.focus != focusList {
		t.Fatalf("tab from params should return to the list, got %v", m.focus)
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

// Arrow keys must follow the heatmap's visual axes: weekdays run top-to-bottom
// (up/down = ∓/± 1 day) and weeks run left-to-right (left/right = ∓/± 7 days).
func TestCalendarArrows_followVisualAxes(t *testing.T) {
	m := newTestModel(t)
	m = send(m, "tab") // focus calendar
	if m.focus != focusCalendar {
		t.Fatal("tab should focus the calendar")
	}
	start := m.calCursor

	for _, tc := range []struct {
		key  string
		days int
	}{
		{"down", 1},
		{"up", -1},
		{"right", 7},
		{"left", -7},
		{"j", 1},
		{"k", -1},
		{"l", 7},
		{"h", -7},
	} {
		moved := send(m, tc.key)
		want := start.AddDate(0, 0, tc.days)
		if !moved.calCursor.Equal(want) {
			t.Errorf("%q should move %+d day(s): want %s, got %s",
				tc.key, tc.days, want.Format("2006-01-02"), moved.calCursor.Format("2006-01-02"))
		}
	}
}

// Setting a base on a commit freezes everything older: strategies only edit
// the active suffix, frozen commits reject the editor, and b on the base
// commit clears the boundary again.
func TestBaseBoundary_freezesOlderCommits(t *testing.T) {
	m := newTestModelN(t, 4)
	// Display order is newest-first: cursor 2 = storage index 1, so commits
	// at storage 0 (the root) become frozen.
	m = send(m, "down", "down", "b")
	if m.plan.Base != 1 {
		t.Fatalf("base should be storage index 1, got %d", m.plan.Base)
	}
	if !strings.Contains(m.View(), "editing 3/4 commits") {
		t.Error("title should report the active/total commit counts")
	}

	m = send(m, "s", "enter", "enter") // apply Even Spread
	root := m.plan.Commits[0]
	if _, ok := m.plan.EditFor(root.Hash); ok {
		t.Error("frozen root commit must not receive edits from a strategy")
	}
	if len(m.plan.Edits) != 3 {
		t.Errorf("expected 3 edits on active commits, got %d", len(m.plan.Edits))
	}

	// The editor must refuse frozen commits.
	m = send(m, "down", "e") // cursor to the bottom (frozen root)
	if m.mode != modeMain {
		t.Error("editor should not open on a frozen commit")
	}

	// b on the base commit unfreezes everything.
	m = send(m, "up", "b")
	if m.plan.Base != 0 {
		t.Errorf("pressing b on the base should clear it, got %d", m.plan.Base)
	}
}

// Moving the base forward drops pending edits on newly frozen commits.
func TestBaseBoundary_dropsEditsOnFrozen(t *testing.T) {
	m := newTestModelN(t, 3)
	m = send(m, "s", "enter", "enter") // edits on all 3
	if len(m.plan.Edits) != 3 {
		t.Fatalf("setup: expected 3 edits, got %d", len(m.plan.Edits))
	}
	m = send(m, "b") // base at cursor (newest commit, storage 2)
	if len(m.plan.Edits) != 1 {
		t.Errorf("edits on frozen commits should be dropped, got %d", len(m.plan.Edits))
	}
}

// Focusing the calendar highlights the commits of the cursor day with a ▌ bar
// and moves the list cursor to the newest commit of that day.
func TestCalendarFocus_highlightsCommitsOfDay(t *testing.T) {
	m := newTestModelN(t, 3) // all commits land on 2026-06-05
	m = send(m, "tab")       // calendar cursor starts on the newest commit's day

	// The list cursor row shows ▸; the two other same-day rows show ▌.
	if got := strings.Count(m.View(), "▌"); got != 2 {
		t.Errorf("want 2 highlight bars for same-day commits, got %d", got)
	}

	m = send(m, "right") // +7 days: no commits there
	if got := strings.Count(m.View(), "▌"); got != 0 {
		t.Errorf("empty day: want 0 highlight bars, got %d", got)
	}

	m = send(m, "left") // back to the commits' day
	day := m.calCursor.In(m.loc).Format("2006-01-02")
	got := m.plan.EffectiveAuthorDate(m.commitAtCursor()).In(m.loc).Format("2006-01-02")
	if got != day {
		t.Errorf("list cursor should sit on a commit of %s, got %s", day, got)
	}
	if bars := strings.Count(m.View(), "▌"); bars != 2 {
		t.Errorf("back on the day: want 2 highlight bars, got %d", bars)
	}
}

// When the list is focused, no day highlight is drawn.
func TestListFocus_noDayHighlight(t *testing.T) {
	m := newTestModelN(t, 3)
	if got := strings.Count(m.View(), "▌"); got != 0 {
		t.Errorf("list focus: want 0 highlight bars, got %d", got)
	}
}

// A failed apply must surface its error inside the strategy pane, otherwise
// a silent failure reads as "enter does nothing".
func TestParamsForm_showsErrorInsidePane(t *testing.T) {
	m := newTestModel(t)
	m = send(m, "s", "enter") // palette -> params pane (Even Spread)
	if m.focus != focusParams {
		t.Fatalf("expected params focus, got %v", m.focus)
	}

	m = send(m, "x", "enter") // corrupt the focused Start-date field, apply
	if m.focus != focusParams {
		t.Fatal("failed apply should keep the params pane focused")
	}
	if m.form.errMsg == "" {
		t.Fatal("apply error should be stored on the form")
	}
	if !strings.Contains(m.View(), "start date must be") {
		t.Error("error should be rendered inside the strategy pane")
	}

	m = send(m, "1") // editing again clears the stale error
	if m.form.errMsg != "" {
		t.Error("typing should clear the form error")
	}
}

func TestCalendarQuotas_applyImmediately(t *testing.T) {
	m := newTestModel(t)
	m = send(m, "tab") // focus calendar
	if m.focus != focusCalendar {
		t.Fatal("tab should focus the calendar")
	}
	// Pressing a digit pins a quota AND applies it right away — no 'g' needed.
	m = send(m, "3")
	if !m.plan.HasChanges() {
		t.Error("setting a quota should apply Manual Pin immediately")
	}

	// The pinned number is drawn on the calendar day itself. The cursor day
	// (2026-06-05) is a Friday, so the quota digit lands on the Fri row.
	view := m.View()
	var friRow string
	for _, line := range strings.Split(view, "\n") {
		if i := strings.Index(line, "Fri"); i >= 0 {
			friRow = line[i:]
			break
		}
	}
	if friRow == "" {
		t.Fatal("calendar should render a Fri row")
	}
	if !strings.Contains(friRow, "3") {
		t.Errorf("quota digit should be visible on the calendar day, got row: %q", friRow)
	}
}

// The half-year heatmap labels follow the calendar cursor.
func TestCalendarHalfYear_labelFollowsCursor(t *testing.T) {
	m := newTestModel(t) // commits on 2026-06-05 -> first half
	if !strings.Contains(m.View(), "Jan–Jun") {
		t.Error("June cursor should render the Jan–Jun half")
	}
	m = send(m, "tab", "right", "right", "right", "right") // +28 days -> July
	if !strings.Contains(m.View(), "Jul–Dec") {
		t.Error("July cursor should render the Jul–Dec half")
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
