package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/wlame/regit/internal/config"
	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/gittest"
	"github.com/wlame/regit/internal/model"
	"github.com/wlame/regit/internal/safety"
)

// fixtureMu serializes git fixture construction. The package's tests shell out to
// git to build throwaway repos; running the module's test binaries concurrently
// (go test -race ./...) let those git invocations race on shared state and
// intermittently fail with "bad tree object HEAD". Serializing fixture construction
// and giving each repo a hermetic git environment makes -race runs deterministic.
var fixtureMu sync.Mutex

// hermeticGitEnv is a thin alias for the shared gittest.Env, kept so the many
// existing call sites in this package read unchanged.
func hermeticGitEnv(home string, extra ...string) []string {
	return gittest.Env(home, extra...)
}

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
	fixtureMu.Lock()
	defer fixtureMu.Unlock()
	dir := t.TempDir()
	run := func(env []string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = hermeticGitEnv(dir, env...)
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
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "shift+left":
		return tea.KeyMsg{Type: tea.KeyShiftLeft}
	case "shift+right":
		return tea.KeyMsg{Type: tea.KeyShiftRight}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
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

// applyStrategyInPane opens the redate flow and focuses the strategy pane
// (calendar -> params), fills date fields for the Even Spread strategy (start
// 2026-06-01, end 2026-06-30) so the strategy is ready, then presses enter to
// apply and return focus to the list.
func applyStrategyInPane(m Model) Model {
	m.enterRedateFlow()
	m = send(m, "tab")  // calendar -> params pane
	m = send(m, "down") // carousel row -> Start date
	m = typeDate(m, "2026-06-01")
	m = send(m, "down") // Start date -> End date
	m = typeDate(m, "2026-06-30")
	return send(m, "enter")
}

// selectStrategy opens the redate flow, focuses the pane, and lands the
// carousel on the strategy at registry offset n from the default (Even
// Spread). It resets to the default first so n is an absolute offset
// regardless of any strategy selected earlier in the test.
func selectStrategy(m Model, n int) Model {
	m.stratIdx = 0 // start from Even Spread so n is absolute, not relative
	m.enterRedateFlow()
	m = send(m, "tab") // calendar -> params pane
	for i := 0; i < n; i++ {
		m = send(m, "right")
	}
	return m
}

// runFeatureNamed invokes a featureBinding by its Title through the same Run
// the action panel's enter calls — independent of panel cursor state and of
// any global hotkey. The panel navigation path itself is covered by the
// actionpanel tests.
func runFeatureNamed(t *testing.T, m Model, name string) Model {
	t.Helper()
	for _, b := range featureBindings {
		if b.Title == name {
			model, _ := b.Run(m)
			return model.(Model)
		}
	}
	t.Fatalf("no feature named %q", name)
	return m
}

func TestSplitChange_previewListsGroups(t *testing.T) {
	sc := splitChange{
		commit:     model.Commit{Hash: "abc1234567890000"},
		commitHash: "abc1234567890000",
		groups: []model.HunkGroup{
			{Order: 1, Subject: "Refactor the first thing", Intent: "cleanup", WIP: false},
			{Order: 2, Subject: "Add the second feature", Intent: "feature", WIP: true},
		},
	}
	st := newStyles()
	pv := sc.preview(st)
	body := strings.Join(pv.lines, "\n")
	if !strings.Contains(body, "Refactor the first") || !strings.Contains(body, "WIP") {
		t.Errorf("split preview should list both subjects and a WIP marker, got:\n%s", body)
	}
	if !strings.Contains(sc.title(), "2") {
		t.Errorf("title should mention the 2 groups, got %q", sc.title())
	}
}

func TestSplitChange_buildDerivesTailFromProposalCommit(t *testing.T) {
	m := newTestModelN(t, 4) // commits c000..c003 (storage order; c000 oldest)
	// Target the 2nd-oldest commit by hash so the tail is everything after it.
	target := m.plan.Commits[1]
	st := splitState{
		proposal: model.SplitProposal{
			CommitHash: target.Hash,
			ParentHash: m.plan.Commits[0].Hash,
			Groups: []model.HunkGroup{
				{Order: 1, Subject: "part one"},
				{Order: 2, Subject: "part two"},
			},
		},
		trees: []string{"t1", "t2"},
	}
	sc, ok := m.buildSplitChange(target, st)
	if !ok {
		t.Fatal("buildSplitChange should find the target commit in the plan")
	}
	// Tail is the commits AFTER the split commit in storage order (c002, c003).
	if len(sc.tail) != 2 || sc.tail[0].Hash != m.plan.Commits[2].Hash {
		t.Errorf("tail should be the 2 commits after the split point, got %d", len(sc.tail))
	}
	if sc.commitHash != target.Hash {
		t.Errorf("splitChange should record the proposal's commit hash")
	}
}

func TestView_mainRendersWithoutPanic(t *testing.T) {
	m := newTestModel(t)
	out := m.View()
	if !strings.Contains(out, "regit") {
		t.Error("main view should contain the title")
	}
	if !strings.Contains(out, "COMMITS") {
		t.Error("main view should contain the commit list header")
	}
}

// TestKeybarHintWidth_neverExceedsWidthAndNeverWraps: the keybar fits the width
// and drops trailing tokens with an ellipsis rather than wrapping.
func TestKeybarHintWidth_neverExceedsWidthAndNeverWraps(t *testing.T) {
	full := keybarHint()
	for _, w := range []int{20, 40, 80, 400} {
		got := keybarHintWidth(w)
		if strings.Contains(got, "\n") {
			t.Errorf("width %d: keybar wrapped:\n%s", w, got)
		}
		if lipgloss.Width(got) > w {
			t.Errorf("width %d: keybar width %d exceeds %d: %q", w, lipgloss.Width(got), w, got)
		}
	}
	if keybarHintWidth(400) != full {
		t.Errorf("at ample width the keybar should be the full hint")
	}
	if !strings.Contains(keybarHintWidth(20), "…") {
		t.Errorf("a truncated keybar should end with an ellipsis")
	}
}

// TestKeybarHintWidth_nonPositiveWidthReturnsFull: width <= 0 has no meaningful
// budget to truncate to, so keybarHintWidth treats it as "no limit" and returns
// the full, untruncated hint rather than an empty string.
func TestKeybarHintWidth_nonPositiveWidthReturnsFull(t *testing.T) {
	full := keybarHint()
	if got := keybarHintWidth(0); got != full {
		t.Errorf("width 0 should return the full hint, got %q", got)
	}
	if got := keybarHintWidth(-5); got != full {
		t.Errorf("negative width should return the full hint, got %q", got)
	}
}

// TestKeybarHintWidth_firstTokenTooWideFallsBackToBareEllipsis covers the
// branch where even the first token plus the " …" suffix overflows width: kept
// stays empty and keybarHintWidth must fall back to a bare "…" instead of an
// empty string or a token that overflows the budget.
func TestKeybarHintWidth_firstTokenTooWideFallsBackToBareEllipsis(t *testing.T) {
	const tiny = 2 // narrower than any single "[x]word" token plus " …"
	got := keybarHintWidth(tiny)
	if got == "" {
		t.Fatal("even at a tiny width, keybarHintWidth should return something")
	}
	if lipgloss.Width(got) > tiny {
		t.Errorf("width %d: keybar width %d exceeds %d: %q", tiny, lipgloss.Width(got), tiny, got)
	}
	if got != "…" {
		t.Errorf("first-token-too-wide should fall back to a bare ellipsis, got %q", got)
	}
}

// TestView_terminalSizeGate: below the minimum terminal size the whole view is
// the too-small guard screen and nothing else — no title, list, or keybar
// (truncated or not) leaks through. At/above the minimum the normal frame
// renders instead.
//
// The discriminator is the title's "regit ·" marker (see viewTitle): it is
// present in every normal render and absent from the guard screen. Comparing
// against the full keybarHint() (as this test used to) would miss a wrongly
// rendered *truncated* keybar leaking into the guard screen, since a truncated
// keybar never equals the full hint; the title marker has no such blind spot.
func TestView_terminalSizeGate(t *testing.T) {
	const marker = "regit ·"

	// Sanity-check the marker actually discriminates before trusting its
	// absence to mean "guard screen": it must show up in a normal render.
	base := newTestModel(t)
	base.width, base.height = 120, 40
	if normal := base.View(); !strings.Contains(normal, marker) {
		t.Fatalf("sanity check: marker %q should be present in a normal render:\n%s", marker, normal)
	}

	for _, tc := range []struct {
		name      string
		w, h      int
		wantGuard bool
	}{
		{"width and height too small", 50, 20, true},
		{"height only too small", 120, 10, true},
		{"width boundary just too small", 59, 20, true},
		{"height boundary just too small", 120, 15, true},
		{"boundary just big enough", 60, 16, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestModel(t)
			m.width, m.height = tc.w, tc.h
			v := m.View()
			hasMarker := strings.Contains(v, marker)

			if tc.wantGuard {
				if !strings.Contains(v, "too small") {
					t.Errorf("expected a too-small guard, got:\n%s", v)
				}
				if hasMarker {
					t.Errorf("guard screen must not render any of the main view (found marker %q):\n%s", marker, v)
				}
				return
			}
			if strings.Contains(v, "too small") {
				t.Errorf("adequate terminal should not show the guard:\n%s", v)
			}
			if !hasMarker {
				t.Errorf("adequate terminal should render the main view (marker %q missing):\n%s", marker, v)
			}
		})
	}
}

// Regression test: no rendered line may be WIDER than the terminal, at any width.
// This catches both the 60–108col frame overflow (the right column no longer fits
// there and is dropped) and wide-glyph truncation (a CJK subject measured by cells,
// not runes). Overflowing lines wrap and cascade the whole frame.
func TestView_neverExceedsTerminalWidth(t *testing.T) {
	m := newTestModelN(t, 12)
	// Give a commit a wide-glyph subject to exercise the cell-width truncation.
	m.plan.Commits[len(m.plan.Commits)-1].Subject = "修复一个非常长的中文提交标题用于测试换行溢出问题的场景"
	for _, w := range []int{60, 70, 80, 90, 100, 107, 108, 120, 140} {
		updated, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: 30})
		mm := updated.(Model)
		for i, line := range strings.Split(mm.View(), "\n") {
			if got := lipgloss.Width(line); got > w {
				t.Errorf("width %d: line %d has display width %d > %d:\n%q", w, i, got, w, line)
			}
		}
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

	// Search the list pane alone: the flow zone's commit-detail panel (the
	// default occupant with no flow open) also carries the cursor commit's
	// hash — short in its title, full on the line below — which would collide
	// with a hash8 substring search over the whole joined view.
	list := m.viewCommitList()
	newest := m.plan.Commits[len(m.plan.Commits)-1]
	hash8 := newest.Hash[:8]
	var entryLine string
	for _, line := range strings.Split(list, "\n") {
		if strings.Contains(line, hash8) {
			entryLine = line
			break
		}
	}
	if entryLine == "" {
		t.Fatalf("newest commit %s not rendered", hash8)
	}
	// The per-row date moved to the detail panel; the row carries hash + subject.
	if !strings.Contains(entryLine, "Refactor the authentication") {
		t.Errorf("entry line should carry the subject, got: %q", entryLine)
	}

	// All test commits share one day, so the list shows exactly one date divider
	// plus one line per visible commit within the list's visible rows.
	if !strings.Contains(list, "commit(s)") {
		t.Error("a date divider should head the day group")
	}
	window := m.listVisibleRows()
	gotVisible := 0
	for _, c := range m.plan.Commits {
		if strings.Contains(list, c.Hash[:8]) {
			gotVisible++
		}
	}
	if gotVisible != window-1 { // one window row is the divider
		t.Errorf("want %d single-line commit entries visible, got %d", window-1, gotVisible)
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
	// Open the redate flow and focus the strategy pane (Even Spread is
	// default-selected), fill the date fields (they start unset/greyed), and
	// apply.
	m.enterRedateFlow()
	m = send(m, "tab") // calendar -> params
	if m.mode != modeMain || m.focus != focusParams {
		t.Fatalf("tab should focus the params pane, got mode %v focus %v", m.mode, m.focus)
	}
	m = send(m, "down")           // carousel row -> Start date
	m = typeDate(m, "2026-06-01") // fill Start date
	m = send(m, "down")           // Start date -> End date
	m = typeDate(m, "2026-06-30") // fill End date
	m = send(m, "enter")          // apply
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

// The strategy pane renders once the redate flow is open (not by default —
// the flow zone shows the commit detail until a flow is launched), and is
// reachable via the tab cycle: calendar -> params -> list.
func TestTabCycle_includesStrategyPaneInRedateFlow(t *testing.T) {
	m := newTestModel(t)
	if strings.Contains(m.View(), "STRATEGY") {
		t.Error("main view should not render the strategy pane with no flow open")
	}
	m.enterRedateFlow()
	if !strings.Contains(m.View(), "STRATEGY") {
		t.Error("main view should render the strategy pane once the redate flow is open")
	}
	m = send(m, "tab")
	if m.focus != focusParams {
		t.Fatalf("tab should focus the params pane, got %v", m.focus)
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
	m.enterRedateFlow() // opens directly on the calendar
	if m.focus != focusCalendar {
		t.Fatal("entering the redate flow should focus the calendar")
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

// TestCalendarSnap_SkipsUnavailableDays: with an explicit selection, the
// calendar cursor may only rest on days inside the selection's segment
// windows. Selecting just the newest commit produces a single segment whose
// window floor sits on the day of the commit right below it (the new anchor);
// every earlier day is outside the window. Moving the cursor further back
// from the window's start must snap-search for an available day and, finding
// none within range, leave the cursor exactly where it was — never before
// the window start.
func TestCalendarSnap_SkipsUnavailableDays(t *testing.T) {
	m := newTestModelN(t, 6)
	m.sel.toggle(m.plan.Commits[5].Hash, false)
	m.focus = focusCalendar

	wins := m.segmentWindows()
	if len(wins) != 1 {
		t.Fatalf("setup: expected exactly one segment window, got %d", len(wins))
	}
	windowStart := wins[0].StartDay
	m.calCursor = windowStart

	m = send(m, "up") // one day earlier: outside the window in every direction
	if m.calCursor.Before(windowStart) {
		t.Errorf("cursor escaped the window: got %v, want >= %v", m.calCursor, windowStart)
	}
}

// TestCalendarSnap_NoSelectionBlocksFuture: with no explicit selection, every
// day up to today is available but the future never is. Starting the cursor
// on today and moving one day forward must snap-search for an available day
// and, finding none (every future day is unavailable), leave the cursor
// unchanged rather than advance into the future.
func TestCalendarSnap_NoSelectionBlocksFuture(t *testing.T) {
	m := newTestModelN(t, 3)
	m.focus = focusCalendar
	today := time.Now()
	m.calCursor = today

	m = send(m, "down") // one day later: tomorrow, always in the future
	if m.calCursor.After(today) {
		t.Errorf("cursor moved into the future: got %v, want <= %v", m.calCursor, today)
	}
}

// TestCalendarSnap_TimeOfDayDoesNotBlockToday: availability is a calendar-day
// question, not an instant one. calCursor carries a residual time-of-day from
// commit timestamps, so an instant comparison (day.After(time.Now())) would
// wrongly treat "today at a later hour than now" as future and strand the
// cursor one day short of today until the wall clock caught up. Seeding the
// cursor to yesterday at now+1h and pressing down must land on today's day.
func TestCalendarSnap_TimeOfDayDoesNotBlockToday(t *testing.T) {
	m := newTestModelN(t, 3)
	m.focus = focusCalendar
	m.calCursor = time.Now().Add(time.Hour).AddDate(0, 0, -1)
	todayKey := time.Now().In(m.loc).Format(dateLayout)

	m = send(m, "down")
	if got := m.calCursor.In(m.loc).Format(dateLayout); got != todayKey {
		t.Errorf("cursor should advance to today's calendar day: got %s, want %s", got, todayKey)
	}
}

// On startup the list cursor sits on the oldest unpushed commit, so the user
// can press b to freeze all pushed history immediately.
func TestStartup_cursorOnFirstUnpushedCommit(t *testing.T) {
	fixtureMu.Lock()
	defer fixtureMu.Unlock()
	dir := t.TempDir()
	origin := t.TempDir()
	git := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = hermeticGitEnv(dir,
			"GIT_AUTHOR_DATE=2026-06-05T14:00:00+00:00",
			"GIT_COMMITTER_DATE=2026-06-05T14:00:00+00:00")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	commit := func(name string) {
		os.WriteFile(filepath.Join(dir, name+".txt"), []byte(name+"\n"), 0o644)
		git("add", ".")
		git("commit", "-q", "-m", "commit "+name)
	}

	bareInit := exec.Command("git", "init", "-q", "--bare", origin)
	bareInit.Env = hermeticGitEnv(dir)
	bareInit.Run()
	git("init", "-q")
	git("symbolic-ref", "HEAD", "refs/heads/main")
	git("config", "user.name", "wlame")
	git("config", "user.email", "vvlame@gmail.com")
	git("config", "commit.gpgsign", "false")
	git("remote", "add", "origin", origin)

	// Two pushed commits, then three local-only ones.
	commit("p0")
	commit("p1")
	git("push", "-q", "-u", "origin", "main")
	commit("local0")
	commit("local1")
	commit("local2")

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
	m = updated.(Model)

	// The upstream (origin/main) is auto-detected as the base, freezing exactly
	// the two pushed commits — no manual `b` needed.
	if m.baseBranch != "origin/main" {
		t.Errorf("base branch should auto-detect the upstream, got %q", m.baseBranch)
	}
	if m.plan.Base != 2 {
		t.Errorf("upstream should auto-freeze the 2 pushed commits, got base %d", m.plan.Base)
	}
	for i := 0; i < 2; i++ {
		if !m.plan.IsFrozen(i) {
			t.Errorf("pushed commit at storage index %d should be frozen", i)
		}
	}
	// The cursor starts on the newest editable commit (just below the freeze).
	if got := m.commitAtCursor().Subject; got != "commit local0" {
		t.Errorf("cursor should start on the first editable commit, got %q", got)
	}
}

// With no upstream and the current branch being main, the no-anchor path leaves
// baseBranch and plan.Base consistently empty/zero — nothing is auto-frozen.
func TestAutoBase_noAnchorLeavesBaseEmpty(t *testing.T) {
	m := newTestModelN(t, 3)
	if m.baseBranch != "" {
		t.Errorf("no anchor: baseBranch should be empty, got %q", m.baseBranch)
	}
	if m.plan.Base != 0 {
		t.Errorf("no anchor: plan.Base should be 0, got %d", m.plan.Base)
	}
}

// A manual base (the global b key) is a cursor freeze, not a branch, so it must
// clear baseBranch — keeping the title's "base:" label honest as "none".
func TestManualBase_clearsBranchLabel(t *testing.T) {
	m := newTestModelN(t, 4)
	m = send(m, "down", "down", "b") // freeze at storage index 1
	if m.plan.Base == 0 {
		t.Fatalf("manual b should set a base, got %d", m.plan.Base)
	}
	if m.baseBranch != "" {
		t.Errorf("a manual freeze should clear the branch name, got %q", m.baseBranch)
	}
	if !strings.Contains(m.View(), "base: none") {
		t.Error("title should report base: none for a manual freeze")
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

	m = applyStrategyInPane(m) // apply Even Spread (tab tab enter)
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

// D marks every editable commit that has a body with a DropBody edit, while
// frozen commits stay untouched.
func TestBulkDropBodies_marksActiveOnly(t *testing.T) {
	m := newTestModelN(t, 4)
	m = send(m, "down", "down", "b") // base at storage index 1: root frozen
	m = runFeatureNamed(t, m, "Drop bodies")
	m = send(m, "enter")

	root := m.plan.Commits[0]
	if _, ok := m.plan.EditFor(root.Hash); ok {
		t.Error("frozen commit must not receive a DropBody edit")
	}
	dropped := 0
	for _, c := range m.plan.Active() {
		if e, ok := m.plan.EditFor(c.Hash); ok && e.DropBody {
			dropped++
		}
	}
	if dropped != 3 {
		t.Errorf("expected 3 active commits marked title-only, got %d", dropped)
	}
	if got := strings.Count(m.View(), "⚑"); got != 3 {
		t.Errorf("list should flag 3 dropped bodies, got %d", got)
	}
}

// R applies a regex replacement to every editable commit title; the list
// shows the effective (edited) titles.
func TestRetitle_regexReplacesActiveTitles(t *testing.T) {
	m := newTestModelN(t, 3) // titles: "Refactor the authentication ... c00N"
	m = runFeatureNamed(t, m, "Retitle (regex)")
	if m.mode != modeRetitle {
		t.Fatalf("R should open the retitle modal, got %v", m.mode)
	}
	for _, ch := range "Refactor" {
		m = send(m, string(ch))
	}
	m = send(m, "tab")
	for _, ch := range "Fix" {
		m = send(m, string(ch))
	}
	m = send(m, "enter")

	if m.mode != modeMain {
		t.Fatalf("successful retitle should close the modal, got %v", m.mode)
	}
	for _, c := range m.plan.Commits {
		if got := m.plan.EffectiveSubject(c); !strings.HasPrefix(got, "Fix the authentication") {
			t.Errorf("title should be rewritten, got %q", got)
		}
	}
	if !strings.Contains(m.View(), "Fix the authentication") {
		t.Error("commit list should display the effective (edited) titles")
	}

	// Replacing back to the original clears the edits entirely.
	m = runFeatureNamed(t, m, "Retitle (regex)")
	for _, ch := range "Fix" {
		m = send(m, string(ch))
	}
	m = send(m, "tab")
	for _, ch := range "Refactor" {
		m = send(m, string(ch))
	}
	m = send(m, "enter")
	if m.plan.HasChanges() {
		t.Error("restoring original titles should clear the subject edits")
	}
}

// A trailing space in the pattern must be honored (not trimmed away), and
// results never keep leading/trailing whitespace.
func TestRetitle_trailingSpaceInPatternIsHonored(t *testing.T) {
	m := newTestModelN(t, 3) // titles: "Refactor the authentication ..."
	m = runFeatureNamed(t, m, "Retitle (regex)")
	for _, ch := range "Refactor the " { // note the trailing space
		m = send(m, string(ch))
	}
	m = send(m, "enter") // empty replacement: delete the prefix

	for _, c := range m.plan.Commits {
		got := m.plan.EffectiveSubject(c)
		if !strings.HasPrefix(got, "authentication") {
			t.Errorf("prefix incl. trailing space should be removed, got %q", got)
		}
	}
}

// The capitalize and ensure-dot toggles transform every editable title; they
// also work with no pattern at all.
func TestRetitle_togglesCapitalizeAndEnsureDot(t *testing.T) {
	m := newTestModelN(t, 3)
	m = runFeatureNamed(t, m, "Retitle (regex)")
	for _, ch := range "Refactor" {
		m = send(m, string(ch))
	}
	m = send(m, "tab")
	for _, ch := range "fix" {
		m = send(m, string(ch))
	}
	m = send(m, "tab", " ") // toggle: capitalize first letter
	m = send(m, "tab", " ") // toggle: end with "."
	m = send(m, "enter")

	for _, c := range m.plan.Commits {
		got := m.plan.EffectiveSubject(c)
		if !strings.HasPrefix(got, "Fix the authentication") {
			t.Errorf("replacement should be capitalized, got %q", got)
		}
		if !strings.HasSuffix(got, ".") {
			t.Errorf("title should end with a period, got %q", got)
		}
	}

	// Toggles alone (no pattern) are a valid operation: re-running ensure-dot
	// changes nothing further, so it reports no matches.
	m = runFeatureNamed(t, m, "Retitle (regex)")
	m = send(m, "tab", "tab", "tab", " ", "enter")
	if m.mode != modeRetitle || m.retitle.errMsg == "" {
		t.Error("a no-op toggle run should stay open and report no changes")
	}
}

// TestRetitle_togglesOnlyLogsRunnableCommand verifies that a retitle using
// only the ensure-period option logs a runnable line with that flag and no
// --match, so the footer teaches the matching headless command.
func TestRetitle_togglesOnlyLogsRunnableCommand(t *testing.T) {
	m := newTestModelN(t, 3)
	m = runFeatureNamed(t, m, "Retitle (regex)")
	// Fixture titles do not end with sentence punctuation, so ensure-period
	// changes every one of them.
	m = send(m, "tab", "tab", "tab", " ") // pattern -> replace -> capitalize -> ensure-period, toggle it
	m = send(m, "enter")
	if m.mode != modeMain {
		t.Fatalf("a toggles-only retitle with real work should apply, got mode %v", m.mode)
	}
	joined := strings.Join(m.planCmds, "\n")
	if strings.Contains(joined, "--match=") {
		t.Errorf("toggles-only retitle must not log a --match flag, got %q", joined)
	}
	if !strings.Contains(joined, "regit retitle --ensure-period") {
		t.Errorf("toggles-only retitle should log a runnable --ensure-period line, got %q", joined)
	}
}

// TestRetitle_emptyResultIsSkipped: a pattern that would empty a title stages
// nothing for that commit and says so, matching the headless verb.
func TestRetitle_emptyResultIsSkipped(t *testing.T) {
	m := newTestModelN(t, 3)
	m = runFeatureNamed(t, m, "Retitle (regex)")
	for _, r := range ".*" {
		m = send(m, string(r))
	}
	m = send(m, "enter")
	if m.mode != modeRetitle {
		t.Fatalf("a pattern that empties every title should keep the wizard open, got mode %v", m.mode)
	}
	if !strings.Contains(m.retitle.errMsg, "would become empty") {
		t.Errorf("errMsg = %q, want it to mention empty titles", m.retitle.errMsg)
	}
	for _, c := range m.plan.Commits {
		if e, ok := m.plan.EditFor(c.Hash); ok && e.NewSubject != nil && *e.NewSubject == "" {
			t.Errorf("commit %s was staged with an empty title", c.Hash)
		}
	}
}

// Invalid patterns and zero-match patterns keep the modal open with an error.
func TestRetitle_invalidPatternShowsError(t *testing.T) {
	m := newTestModelN(t, 3)
	m = runFeatureNamed(t, m, "Retitle (regex)")
	m = send(m, "(", "enter")
	if m.mode != modeRetitle {
		t.Fatal("invalid pattern should keep the modal open")
	}
	if m.retitle.errMsg == "" || !strings.Contains(m.View(), "invalid pattern") {
		t.Error("invalid pattern error should render inside the modal")
	}
	m = send(m, "esc")
	if m.mode != modeMain {
		t.Error("esc should close the retitle modal")
	}
	if m.plan.HasChanges() {
		t.Error("a cancelled retitle must not edit anything")
	}
}

// Retitle respects the base boundary: frozen titles are never rewritten.
func TestRetitle_respectsBase(t *testing.T) {
	m := newTestModelN(t, 3)
	m = send(m, "down", "b") // base at storage index 1: root frozen
	m = runFeatureNamed(t, m, "Retitle (regex)")
	for _, ch := range "Refactor" {
		m = send(m, string(ch))
	}
	m = send(m, "tab")
	for _, ch := range "Fix" {
		m = send(m, string(ch))
	}
	m = send(m, "enter")

	root := m.plan.Commits[0]
	if got := m.plan.EffectiveSubject(root); !strings.HasPrefix(got, "Refactor") {
		t.Errorf("frozen title must stay unchanged, got %q", got)
	}
	if len(m.plan.Edits) != 2 {
		t.Errorf("expected 2 retitled commits, got %d edits", len(m.plan.Edits))
	}
}

// Moving the base forward drops pending edits on newly frozen commits.
func TestBaseBoundary_dropsEditsOnFrozen(t *testing.T) {
	m := newTestModelN(t, 3)
	m = applyStrategyInPane(m) // edits on all 3 (tab tab enter)
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
	m.enterRedateFlow()      // calendar cursor starts on the newest commit's day

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
// a silent failure reads as "enter does nothing". Date fields now start unset,
// so pressing enter with an empty/partial date triggers the "fill date fields"
// error rather than a parse failure.
func TestParamsForm_showsErrorInsidePane(t *testing.T) {
	m := newTestModel(t)
	// A wide terminal so the strategy pane renders at its natural width
	// instead of clamping down (layout.go's flowWidth) and wrapping its
	// description onto an extra line that would push the error line past the
	// flow zone's height budget.
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 220, Height: 40})
	m = updated.(Model)
	m.enterRedateFlow()
	m = send(m, "tab") // calendar -> params pane (Even Spread)
	if m.focus != focusParams {
		t.Fatalf("expected params focus, got %v", m.focus)
	}
	m = send(m, "down") // move from the carousel row to the first input

	m = send(m, "x", "enter") // type an invalid partial date, then apply
	if m.focus != focusParams {
		t.Fatal("failed apply should keep the params pane focused")
	}
	if m.activeStrat().form.errMsg == "" {
		t.Fatal("apply error should be stored on the form")
	}
	if !strings.Contains(m.View(), "date") {
		t.Error("date-related error should be rendered inside the strategy pane")
	}

	m = send(m, "1") // editing again clears the stale error
	if m.activeStrat().form.errMsg != "" {
		t.Error("typing should clear the form error")
	}
}

func TestCalendarQuotas_applyImmediately(t *testing.T) {
	m := newTestModel(t)
	// Select Manual Pin first: digits only gate to the active strategy.
	m = selectStrategy(m, 1) // strategy pane, Manual Pin (index 1)
	m = send(m, "tab")       // params pane -> list
	m = send(m, "tab")       // list -> calendar
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

// Digits typed while staying on the same day build a two-digit quota
// (2 → 24 → 45); moving the cursor starts a fresh entry.
//
// Uses a 60-commit fixture (not the default 3) so the single segment's pin
// capacity (== its commit count, see pickcontroller.go) never clamps any of
// the quotas this test asserts — the peak in-flight sum across both days
// (45 on the first day + 7 on the second, before the first day is re-pinned
// down to 3) reaches 52.
func TestCalendarQuotas_twoDigitEntry(t *testing.T) {
	m := newTestModelN(t, 60)
	// Select Manual Pin first: digits only gate to the active strategy.
	m = selectStrategy(m, 1) // strategy pane, Manual Pin (index 1)
	m = send(m, "tab")       // params pane -> list
	m = send(m, "tab")       // list -> calendar (cursor on 2026-06-05)
	day := m.calCursor.In(m.loc).Format("2006-01-02")

	m = send(m, "2")
	if got := m.activeStrat().quotas[day]; got != 2 {
		t.Fatalf("first digit: want quota 2, got %d", got)
	}
	m = send(m, "4")
	if got := m.activeStrat().quotas[day]; got != 24 {
		t.Fatalf("second digit should append: want 24, got %d", got)
	}
	m = send(m, "5")
	if got := m.activeStrat().quotas[day]; got != 45 {
		t.Fatalf("third digit should keep the last two: want 45, got %d", got)
	}

	// Moving away and typing starts a new number on the new day…
	m = send(m, "right", "7")
	other := m.calCursor.In(m.loc).Format("2006-01-02")
	if got := m.activeStrat().quotas[other]; got != 7 {
		t.Errorf("digit after a move should start fresh: want 7, got %d", got)
	}
	// …and returning to the first day also starts fresh (3, not 53).
	m = send(m, "left", "3")
	if got := m.activeStrat().quotas[day]; got != 3 {
		t.Errorf("digit after returning should start fresh: want 3, got %d", got)
	}
}

// TestCalendarQuotas_repinOnSharedBoundaryPreservesOtherDay is the production
// (app.go) counterpart to pickcontroller_test.go's
// TestPinWithEviction_RepinMustDeleteStaleEntryFirst: it drives the identical
// shared-boundary shape — a 3-commit segment and a 24-commit segment meeting at
// one calendar day — through real key presses on the calendar, to prove
// moveCalendarCursor's digit handler performs the HARD CONTRACT's delete-before-repin
// step. Without it, that same topology corrupts AttributePins' segment
// resolution and wrongly evicts the unrelated day's pin outright (see the
// sibling TestPinWithEviction_MissingDeleteCorruptsSharedBoundaryAttribution).
func TestCalendarQuotas_repinOnSharedBoundaryPreservesOtherDay(t *testing.T) {
	m := newTestModelN(t, 28) // all 28 commits share one author date
	// Storage indices 0..2 (3 commits) and 4..27 (24 commits) are selected;
	// index 3 is left OUT of the selection so it becomes the anchor splitting
	// the run into two segments. The anchor's calendar day is then the shared
	// boundary between both segments' windows (see strategy.SegmentTargets).
	for _, i := range []int{0, 1, 2} {
		m.sel.toggle(m.plan.Commits[i].Hash, false)
	}
	for i := 4; i < 28; i++ {
		m.sel.toggle(m.plan.Commits[i].Hash, false)
	}

	m = selectStrategy(m, 1) // strategy pane, Manual Pin
	m = send(m, "tab")       // params pane -> list
	m = send(m, "tab")       // list -> calendar; cursor seeds on the shared day
	sharedDay := m.calCursor.In(m.loc).Format(dateLayout)

	m = send(m, "up") // one day earlier: inside the 3-commit segment only
	otherDay := m.calCursor.In(m.loc).Format(dateLayout)
	m = send(m, "2") // pin the other day to quota 2

	m = send(m, "down") // back to the shared day
	if got := m.calCursor.In(m.loc).Format(dateLayout); got != sharedDay {
		t.Fatalf("setup: expected to return to the shared day %s, got %s", sharedDay, got)
	}
	m = send(m, "2") // first digit: quota 2 on the shared day
	m = send(m, "4") // second digit: combine to 24 (a re-pin, not an add)

	a := m.activeStrat()
	if got := a.quotas[otherDay]; got != 2 {
		t.Errorf("the independently pinned day must survive untouched: quotas=%v", a.quotas)
	}
	if got := a.quotas[sharedDay]; got != 24 {
		t.Errorf("the shared day should end at the combined quota 24, got quotas=%v", a.quotas)
	}
	if len(a.quotaSeq) != 2 {
		t.Errorf("exactly 2 pinned days should remain tracked, got quotaSeq=%v", a.quotaSeq)
	}
	if m.statusKind == statusWarn && strings.Contains(m.statusMsg, "Shrunk") {
		t.Errorf("no pin should have been shrunk by the re-pin, got status %q", m.statusMsg)
	}
}

// Space picks/unpicks calendar days for the Picked Days strategy; the random
// split applies immediately and reshuffles as the set changes.
func TestPickedDays_spaceTogglesAndApplies(t *testing.T) {
	m := newTestModelN(t, 6)
	// Select Picked Days first: space only gates to the active strategy.
	m = selectStrategy(m, 2) // strategy pane, Picked Days (index 2)
	m = send(m, "tab")       // params pane -> list
	m = send(m, "tab")       // list -> calendar
	m = send(m, " ")         // pick the cursor day (2026-06-05)
	day := m.calCursor.In(m.loc).Format("2006-01-02")
	if !m.activeStrat().picked[day] {
		t.Fatal("space should pick the day under the cursor")
	}
	if !m.plan.HasChanges() {
		t.Error("picking a day should apply the strategy immediately")
	}
	if got := m.dayCounts()[day]; got != 6 {
		t.Errorf("single picked day should hold all 6 commits, got %d", got)
	}

	// Picking a second day splits the commits, at least one on each.
	m = send(m, "right", " ")
	day2 := m.calCursor.In(m.loc).Format("2006-01-02")
	counts := m.dayCounts()
	if counts[day]+counts[day2] != 6 {
		t.Errorf("split should cover all commits: %d + %d", counts[day], counts[day2])
	}
	if counts[day] == 0 || counts[day2] == 0 {
		t.Errorf("each picked day should receive at least one commit: %v", counts)
	}

	// Unpicking the second day reshuffles everything back onto the first.
	m = send(m, " ")
	if m.activeStrat().picked[day2] {
		t.Fatal("space again should unpick the day")
	}
	if got := m.dayCounts()[day]; got != 6 {
		t.Errorf("after unpicking, all 6 commits should return to %s, got %d", day, got)
	}
}

// TestPickedDays_spaceToggleOff_clearsSeqEntry is a focused regression test for
// the Space handler's "already picked" branch: toggling a picked day off must
// clear BOTH a.picked and a.pickSeq. Leaving a stale pickSeq entry behind would
// violate the HARD CONTRACT — a later pickWithEviction/pinWithEviction call
// must never see a day that is already in *Seq, or the eviction replay
// double-counts it (see pickcontroller_test.go's repin tests).
func TestPickedDays_spaceToggleOff_clearsSeqEntry(t *testing.T) {
	m := newTestModelN(t, 3)
	m = selectStrategy(m, 2) // strategy pane, Picked Days
	m = send(m, "tab")       // params pane -> list
	m = send(m, "tab")       // list -> calendar
	day := m.calCursor.In(m.loc).Format(dateLayout)

	m = send(m, " ") // pick the cursor day
	a := m.activeStrat()
	if !a.picked[day] {
		t.Fatal("setup: space should pick the cursor day")
	}
	if _, ok := a.pickSeq[day]; !ok {
		t.Fatal("setup: picking should record a pickSeq entry")
	}

	m = send(m, " ") // toggle off
	a = m.activeStrat()
	if a.picked[day] {
		t.Error("space again should unpick the day")
	}
	if _, ok := a.pickSeq[day]; ok {
		t.Errorf("unpicking must also clear the pickSeq entry, got %v", a.pickSeq)
	}
}

// Action keys work from any pane: e/b from the calendar, s/p/D from the
// params pane — while digits still type into the focused form field.
func TestGlobalKeys_workFromAnyPane(t *testing.T) {
	m := newTestModelN(t, 3)

	// Calendar focus: e opens the editor on the list-cursor commit.
	m = send(m, "tab", "e")
	if m.mode != modeEditor {
		t.Fatal("e should open the editor from the calendar pane")
	}
	m = send(m, "esc")

	// Calendar focus: b sets the base at the list cursor.
	m = send(m, "b")
	if m.plan.Base != 2 {
		t.Fatalf("b should set the base from the calendar pane, got %d", m.plan.Base)
	}
	m = send(m, "b") // clear it again

	// Params pane: p opens the pipeline preview, D drops bodies.
	// Reset focus to the list first (was on calendar after the e/b actions above),
	// then apply via the carousel pane and re-focus the params pane.
	m = send(m, "tab")         // calendar -> params
	m = send(m, "tab")         // params -> list (via updateParamsPane's tab handler)
	m = applyStrategyInPane(m) // apply Even Spread to stage edits (tab tab enter)
	m = send(m, "tab", "tab")  // list -> calendar -> params
	if m.focus != focusParams {
		t.Fatal("setup: params pane should be focused")
	}
	m = send(m, "p")
	if m.mode != modePipeline {
		t.Fatal("p should open the pipeline preview from the params pane")
	}
	m = send(m, "esc")

	m = runFeatureNamed(t, m, "Drop bodies") // open the popup then confirm
	m = send(m, "enter")
	if len(m.plan.Edits) != 3 {
		t.Errorf("D+enter should drop bodies from the params pane, got %d edits", len(m.plan.Edits))
	}

	// Digits are NOT global: they belong to the focused form field.
	// Navigate to the carousel row first (focusIdx=0), then down to the first input.
	// The carousel row is always accessible via up from focusIdx=0 wrapping around.
	m.activeStrat().form.focusIdx = 0 // ensure we start at the carousel row
	m = send(m, "down")               // carousel (0) -> first input (1)
	before := m.activeStrat().form.inputs[0].Value()
	m = send(m, "1")
	if got := m.activeStrat().form.inputs[0].Value(); got != before+"1" {
		t.Errorf("digit should type into the field: want %q, got %q", before+"1", got)
	}

	// s no longer opens the palette from the params pane (carousel replaces it).
	m = send(m, "s")
	if m.mode != modeMain {
		t.Fatal("s should no longer open any overlay from the params pane")
	}
}

// Manual Pin and Picked Days applied from the strategy pane must not demand a
// date range: the pinned/picked days define it. Gating rule: select the strategy
// first, then pin/pick from the calendar — digits and space are only active
// under the correct strategy.
func TestParamsPane_manualPinAndPickedDaysApply(t *testing.T) {
	m := newTestModelN(t, 4)

	// Carousel order: 0 Even Spread, 1 Manual Pin, 2 Picked Days.
	// Select Manual Pin, then focus the calendar and pin a quota.
	m = selectStrategy(m, 1) // strategy pane, Manual Pin active
	m = send(m, "tab")       // params pane -> list
	m = send(m, "tab")       // list -> calendar
	m = send(m, "2")         // pin quota 2 on cursor day (stages live)
	if !m.plan.HasChanges() {
		t.Fatal("Manual Pin quota should stage live immediately")
	}
	m = send(m, "tab") // calendar -> params pane
	m = send(m, "enter")
	if m.activeStrat().form.errMsg != "" {
		t.Fatalf("Manual Pin from the pane should apply, got %q", m.activeStrat().form.errMsg)
	}
	if m.focus != focusList {
		t.Fatal("successful Manual Pin apply should return focus to the list")
	}

	// Select Picked Days, then focus the calendar and pick a day.
	m = selectStrategy(m, 2) // strategy pane, Picked Days active
	m = send(m, "tab")       // params pane -> list
	m = send(m, "tab")       // list -> calendar
	m = send(m, " ")         // pick the cursor day (stages live)
	m = send(m, "tab")       // calendar -> params pane
	m = send(m, "enter")
	if m.activeStrat().form.errMsg != "" {
		t.Fatalf("Picked Days from the pane should apply, got %q", m.activeStrat().form.errMsg)
	}
	if !m.plan.HasChanges() || !m.plan.IsValid() {
		t.Error("Picked Days apply should produce a valid plan")
	}
}

// The half-year heatmap labels follow the calendar cursor.
func TestCalendarHalfYear_labelFollowsCursor(t *testing.T) {
	m := newTestModel(t) // commits on 2026-06-05 -> first half
	m.enterRedateFlow()
	if !strings.Contains(m.View(), "Jan–Jun") {
		t.Error("June cursor should render the Jan–Jun half")
	}
	m = send(m, "right", "right", "right", "right") // +28 days -> July
	if !strings.Contains(m.View(), "Jul–Dec") {
		t.Error("July cursor should render the Jul–Dec half")
	}
}

func TestReset_clearsEdits(t *testing.T) {
	m := newTestModel(t)
	m = applyStrategyInPane(m) // apply even spread (tab tab enter)
	if !m.plan.HasChanges() {
		t.Fatal("setup: expected edits")
	}
	m = send(m, "R")
	if m.plan.HasChanges() {
		t.Error("reset should clear all edits")
	}
}

func TestApplyAndUndo_roundTrip(t *testing.T) {
	m := newTestModel(t)
	origHead, _ := m.repo.ResolveRef("HEAD")

	m = applyStrategyInPane(m) // spread (tab tab enter)
	m = send(m, "A", "enter")  // preview, then apply (clean local repo, no confirm)
	if m.statusKind == statusErr {
		t.Fatalf("apply errored: %s", m.statusMsg)
	}
	newHead, _ := m.repo.ResolveRef("HEAD")
	if newHead == origHead {
		t.Fatal("apply should have moved HEAD")
	}

	m = send(m, "U") // undo
	restored, _ := m.repo.ResolveRef("HEAD")
	if restored != origHead {
		t.Errorf("undo should restore original HEAD: %s != %s", restored, origHead)
	}
}

func TestRetiredHotkeys_doNothing(t *testing.T) {
	m := newTestModelN(t, 3)
	for _, k := range []string{"S", "w", "D"} {
		if mm := send(m, k); mm.mode != modeMain {
			t.Errorf("retired key %q should be a no-op, but changed mode to %v", k, mm.mode)
		}
	}
}

func TestLowercaseResetUndo_areNoOps(t *testing.T) {
	m := newTestModelN(t, 3)
	m = applyStrategyInPane(m) // stage edits via carousel (tab tab enter)
	if !m.plan.HasChanges() {
		t.Fatal("setup: expected staged edits")
	}
	m = send(m, "r") // lowercase no longer resets
	if !m.plan.HasChanges() {
		t.Error("lowercase r must no longer reset edits")
	}
	m = send(m, "R") // capital resets
	if m.plan.HasChanges() {
		t.Error("R should reset edits")
	}
}

func TestHelpToggle(t *testing.T) {
	m := newTestModel(t)
	m = send(m, "?")
	if m.mode != modeHelp {
		t.Fatal("? should open help")
	}
	if !strings.Contains(m.View(), "rewrite commit history") {
		t.Error("help view should render help content")
	}
	m = send(m, "?")
	if m.mode != modeMain {
		t.Error("? should close help")
	}
}

func TestPreviewOpens(t *testing.T) {
	m := newTestModel(t)
	m = applyStrategyInPane(m) // apply even spread (tab tab enter)
	m = send(m, "p")
	if m.mode != modePipeline {
		t.Fatal("p should open preview")
	}
	if !strings.Contains(m.View(), "→") {
		t.Error("preview should render the diff table")
	}
}

func TestModel_ListWidthGrowsWithTerminal(t *testing.T) {
	m := newTestModel(t) // sized to 120×40 by the helper
	narrow := m.listWidth()

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 220, Height: 40})
	wide := updated.(Model).listWidth()

	if wide <= narrow {
		t.Fatalf("list should widen on a 220-col terminal: narrow=%d wide=%d", narrow, wide)
	}
	if wide != listCeilWidth {
		t.Fatalf("wide list should be capped exactly at the ceiling (%d), got %d", listCeilWidth, wide)
	}
}

func TestDetail_paneRendersForCursorCommit(t *testing.T) {
	m := newTestModelN(t, 3)
	// Wide terminal -> the flow zone (showing the commit detail with no flow
	// open) is seated next to the list.
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 220, Height: 40})
	m = updated.(Model)
	if computeLayout(m.width, m.height).flowWidth == 0 {
		t.Fatal("220-col terminal should seat the flow zone")
	}
	if !strings.Contains(m.View(), "DETAIL") {
		t.Errorf("wide main view should render the detail panel header")
	}
}

func TestDetail_statCmdCachesResult(t *testing.T) {
	m := newTestModelN(t, 3)
	c := m.commitAtCursor()

	cmd := m.detailFetchCmd()
	if cmd == nil {
		t.Fatal("a fresh cursor commit should produce a stat-fetch command")
	}
	msg := cmd() // run the command synchronously and feed the result back
	updated, _ := m.Update(msg)
	m = updated.(Model)

	if _, ok := m.statCache[c.Hash]; !ok {
		t.Errorf("running the stat command should cache the result for %s", short(c.Hash))
	}
	// A second request for the same (cached) commit issues no new command.
	if m.detailFetchCmd() != nil {
		t.Errorf("a cached commit should not refetch")
	}
}

func TestDetail_overlayTogglesAtNarrowWidth(t *testing.T) {
	m := newTestModelN(t, 3)
	// 70 cols is narrower than the flow zone's showRight threshold.
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 70, Height: 40})
	m = updated.(Model)
	if computeLayout(m.width, m.height).showRight {
		t.Fatal("70-col terminal should hide the flow zone")
	}
	m = send(m, ".")
	if m.mode != modeDetail {
		t.Fatalf("'.' should open the detail overlay, got mode %v", m.mode)
	}
	if !strings.Contains(m.View(), "DETAIL") {
		t.Error("detail overlay should render the panel")
	}
	m = send(m, "esc")
	if m.mode != modeMain {
		t.Fatalf("esc should close the detail overlay, got %v", m.mode)
	}
}

// TestDetail_markAndFetchDedupsInFlight pins the in-flight dedup contract:
// the first markAndFetch issues a command and marks the commit in flight, and a
// second call for the same commit returns nil. The subtlety is that statInFlight
// is a map (a reference type), so the mark set inside the value-receiver
// markAndFetch persists on the caller's copy across calls.
func TestDetail_markAndFetchDedupsInFlight(t *testing.T) {
	m := newTestModelN(t, 3)
	hash := m.commitAtCursor().Hash

	first := m.markAndFetch()
	if first == nil {
		t.Fatal("first markAndFetch on a fresh commit should issue a command")
	}
	if !m.statInFlight[hash] {
		t.Errorf("markAndFetch should mark %s in flight", short(hash))
	}

	second := m.markAndFetch()
	if second != nil {
		t.Error("second markAndFetch for an in-flight commit should return nil")
	}
}

// TestDetail_reloadResetsStatCaches verifies that reload wipes every stat-side
// map so a rebuilt plan never serves a stale commit's stats.
func TestDetail_reloadResetsStatCaches(t *testing.T) {
	m := newTestModelN(t, 3)
	m.statCache["x"] = gitio.Stat{}
	m.statErr["x"] = "boom"
	m.statInFlight["x"] = true

	if err := m.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}

	if len(m.statCache) != 0 {
		t.Errorf("reload should clear statCache, got %d entries", len(m.statCache))
	}
	if len(m.statErr) != 0 {
		t.Errorf("reload should clear statErr, got %d entries", len(m.statErr))
	}
	if len(m.statInFlight) != 0 {
		t.Errorf("reload should clear statInFlight, got %d entries", len(m.statInFlight))
	}
}

// TestReload_clearsStrategyPinState: reload() runs after a real history
// rewrite (apply/undo), where the plan's dates ARE the applied distribution —
// the pre-apply pins were consumed by it. Leaving quotas/picked (and their
// recency seq maps) populated would pollute the next pinOwnerFor /
// AttributePins run against the rebuilt windows, so reload must reset every
// strategy slot's pin state, exactly like the R global reset does.
func TestReload_clearsStrategyPinState(t *testing.T) {
	m := newTestModelN(t, 3)
	// Populate Manual Pin state through the real key path (quota "3" on the
	// cursor day fills quotas, quotaSeq, and advances nextSeq)…
	m = selectStrategy(m, 1) // strategy pane, Manual Pin
	m = send(m, "tab")       // params pane -> list
	m = send(m, "tab")       // list -> calendar
	m = send(m, "3")
	a := m.activeStrat()
	if len(a.quotas) == 0 || len(a.quotaSeq) == 0 || a.nextSeq == 0 {
		t.Fatalf("setup: pinning should populate quotas/quotaSeq/nextSeq, got %v %v %d",
			a.quotas, a.quotaSeq, a.nextSeq)
	}
	// …and seed picked-day state on another slot directly, so the assertion
	// below covers all four maps across ALL strategy slots, not just the
	// active one.
	other := &m.strats[0]
	other.picked["2026-06-05"] = true
	other.pickSeq["2026-06-05"] = 1
	other.nextSeq = 1

	if err := m.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}

	for i := range m.strats {
		s := &m.strats[i]
		if len(s.quotas) != 0 || len(s.picked) != 0 ||
			len(s.pickSeq) != 0 || len(s.quotaSeq) != 0 || s.nextSeq != 0 {
			t.Errorf("slot %d: reload must clear all pin state, got "+
				"quotas=%v picked=%v pickSeq=%v quotaSeq=%v nextSeq=%d",
				i, s.quotas, s.picked, s.pickSeq, s.quotaSeq, s.nextSeq)
		}
	}
}

// TestCycleStrategy_reattributesStalePinsOnReturn is the regression test for
// Finding 3: picks placed on a strategy slot under a WIDE selection must be
// re-checked against the current windows when the carousel returns to that
// slot, even though the selection narrowed while a DIFFERENT strategy was
// active (so onDatesSelectionChanged never touched Picked Days' pins at the
// time). Sequence: pick 3 days under the default whole-range selection ->
// carousel away -> narrow the selection to one commit -> carousel back to
// Picked Days. Before the fix, all 3 stale picks (two now outside the
// narrowed window) would stage verbatim and land in the logged --pick= line.
func TestCycleStrategy_reattributesStalePinsOnReturn(t *testing.T) {
	m := newTestModelN(t, 3)
	loc := m.loc
	// Give the 3 commits distinct dates (root-first) so narrowing to the
	// middle commit produces a non-degenerate [floor, ceil] window instead of
	// a single instant: c0=Jun1, c1=Jun3, c2=Jun5.
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, loc)
	for i := range m.plan.Commits {
		m.plan.Commits[i].OrigAuthorDate = base.AddDate(0, 0, i*2)
	}

	m = selectStrategy(m, 2) // registry offset 2 from Even Spread -> Picked Days

	// Wide selection (default SelActive: all 3 commits, one segment,
	// capacity 3): pick three days that all fit without eviction.
	// survivorDay sits inside every window the narrowed selection will ever
	// produce ([Jun1, Jun5]); the other two only fit the WIDE window.
	const survivorDay = "2026-06-03"
	const staleDay1 = "2026-05-20"
	const staleDay2 = "2026-05-25"
	a := m.activeStrat()
	wideWins := m.segmentWindows()
	for _, day := range []string{staleDay1, staleDay2, survivorDay} {
		if _, ok := pickWithEviction(a, day, wideWins, loc); !ok {
			t.Fatalf("setup: pick %s should fit the wide window", day)
		}
	}
	if len(a.picked) != 3 {
		t.Fatalf("setup: want 3 picks staged, got %v", a.picked)
	}

	// Carousel away from Picked Days WITHOUT narrowing the selection yet —
	// the reviewer's sequence: pick under a wide selection, then switch
	// strategies, leaving Picked Days' pins un-reattributed at their old slot.
	m.cycleStrategy(1) // -> Rate Fill; not ready, stages nothing
	if m.activeStrat().form.strat.Name() == "Picked Days" {
		t.Fatal("setup: carousel must have moved off Picked Days")
	}

	// Narrow the selection to the middle commit (c1 = Jun 3). This re-attributes
	// the CURRENTLY active strategy's pins (Rate Fill has none); Picked Days'
	// stale picks are untouched by this call — that is the bug under test.
	m.sel.toggle(m.plan.Commits[1].Hash, false)
	m.onDatesSelectionChanged()

	// Carousel back to Picked Days: before the fix, its 3 stale picks (two now
	// outside the narrowed [Jun1, Jun5] window) would stage verbatim.
	m.cycleStrategy(-1)
	if got := m.activeStrat().form.strat.Name(); got != "Picked Days" {
		t.Fatalf("setup: carousel should have returned to Picked Days, got %s", got)
	}

	pd := m.activeStrat()
	if len(pd.picked) != 1 || !pd.picked[survivorDay] {
		t.Errorf("stale out-of-window picks must be dropped on carousel return, got picked=%v",
			pd.picked)
	}
	if len(pd.pickSeq) != 1 {
		t.Errorf("pickSeq must be pruned alongside picked, got %v", pd.pickSeq)
	}
	if !strings.Contains(m.redateCmd, "--pick="+survivorDay) {
		t.Errorf("staged redateCmd should pick only the in-window day, got %q", m.redateCmd)
	}
	if strings.Contains(m.redateCmd, staleDay1) || strings.Contains(m.redateCmd, staleDay2) {
		t.Errorf("staged redateCmd must not reference dropped out-of-window picks, got %q", m.redateCmd)
	}
}

// TestDetail_refetchesAfterApply verifies that the success path of commitPending
// schedules a fresh detail-stat fetch after reload() wipes the stat caches.
// Without the fix the apply path returns nil, leaving the panel stuck at
// "stat loading…" until the user manually moves the cursor.
func TestDetail_refetchesAfterApply(t *testing.T) {
	m := newTestModelN(t, 3)
	// Stage edits by applying a strategy via the carousel pane.
	m = applyStrategyInPane(m) // tab tab enter
	if !m.plan.HasChanges() {
		t.Fatal("applying a strategy should stage edits")
	}
	// Apply the rewrite: reload() wipes the stat caches, so the apply path must
	// schedule a fresh fetch for the cursor commit (else the detail panel stays blank).
	updated, _ := m.Update(key("A")) // A opens the preview
	m = updated.(Model)
	updated, cmd := m.Update(key("enter")) // enter applies
	m = updated.(Model)
	_ = m
	if cmd == nil {
		t.Fatal("apply should schedule a detail-stat refetch after reload wiped the cache")
	}
}

func TestListNav_fastJumps(t *testing.T) {
	m := newTestModelN(t, 60) // newest-first display; cursor starts at 0
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = updated.(Model)

	// G / End -> oldest commit (last display index).
	m = send(m, "G")
	if m.listCursor != len(m.plan.Commits)-1 {
		t.Fatalf("G should jump to the oldest commit, got cursor %d", m.listCursor)
	}
	// g / Home -> newest commit.
	m = send(m, "g")
	if m.listCursor != 0 {
		t.Fatalf("g should jump to the newest commit, got cursor %d", m.listCursor)
	}

	// Ctrl-D moves a half page down; the cursor advances but stays in range.
	before := m.listCursor
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	m = updated.(Model)
	half := m.listVisibleRows() / 2
	if m.listCursor != before+half {
		t.Fatalf("Ctrl-D should move a half page (%d), got %d", half, m.listCursor)
	}
}

// branchModel builds a Model whose repo is on `main` (3 commits) with an extra
// local `feature` branch forked at the first commit, sized to a usable terminal.
func branchModel(t *testing.T) Model {
	t.Helper()
	fixtureMu.Lock()
	defer fixtureMu.Unlock()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = hermeticGitEnv(dir,
			"GIT_AUTHOR_DATE=2026-06-05T14:00:00+00:00",
			"GIT_COMMITTER_DATE=2026-06-05T14:00:00+00:00")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	commit := func(name string) {
		os.WriteFile(filepath.Join(dir, name+".txt"), []byte(name+"\n"), 0o644)
		run("add", ".")
		run("commit", "-q", "-m", "commit "+name)
	}
	run("init", "-q")
	run("symbolic-ref", "HEAD", "refs/heads/main")
	run("config", "user.name", "wlame")
	run("config", "user.email", "vvlame@gmail.com")
	run("config", "commit.gpgsign", "false")
	commit("c0")
	run("branch", "feature")
	commit("c1")
	commit("c2")

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

func TestPicker_opensAndSwitchesBranch(t *testing.T) {
	m := branchModel(t)
	m = send(m, "B")
	if m.mode != modePicker {
		t.Fatalf("B should open the picker, got mode %v", m.mode)
	}
	if len(m.picker.items) != 2 { // main, feature
		t.Fatalf("picker should list the 2 local branches, got %v", m.picker.items)
	}

	// Filter to "feature" and switch to it.
	m.picker.filter.SetValue("feature")
	updated, _ := m.Update(key("enter"))
	m = updated.(Model)
	if m.mode != modeMain {
		t.Fatalf("switching should return to the main view, got %v", m.mode)
	}
	if m.state.Branch != "feature" {
		t.Errorf("should have switched to feature, on %q", m.state.Branch)
	}
}

func TestPicker_switchRefusedWithPendingEdits(t *testing.T) {
	m := branchModel(t)
	m = applyStrategyInPane(m) // stage a strategy edit via carousel (tab tab enter)
	if !m.plan.HasChanges() {
		t.Fatal("strategy apply should stage edits")
	}
	m = send(m, "B")
	m.picker.filter.SetValue("feature")
	updated, _ := m.Update(key("enter"))
	m = updated.(Model)
	if m.state.Branch == "feature" {
		t.Error("switch must be refused while there are unsaved edits")
	}
	// Guard errors now surface inside the picker overlay (not the status bar),
	// so the picker stays open and errMsg is set.
	if m.mode != modePicker {
		t.Errorf("a refused switch must keep the picker open, got %v", m.mode)
	}
	if m.picker.errMsg == "" {
		t.Error("a refused switch should set picker.errMsg")
	}
}

func TestPicker_setBaseFreezesMergeBaseCone(t *testing.T) {
	m := branchModel(t) // on main (c0,c1,c2); feature forked at c0
	// On main with no upstream and current branch == main, nothing is frozen yet.
	if m.plan.Base != 0 {
		t.Fatalf("precondition: base should start at 0 on main, got %d", m.plan.Base)
	}
	m = send(m, "B")
	m.picker.filter.SetValue("feature")
	updated, _ := m.Update(key("tab")) // set feature as the base branch
	m = updated.(Model)

	if m.mode != modeMain {
		t.Fatalf("setting base should return to the main view, got %v", m.mode)
	}
	if m.baseBranch != "feature" {
		t.Errorf("base branch should be feature, got %q", m.baseBranch)
	}
	// merge-base(main, feature) is c0 (storage index 0), so Base freezes index 0.
	if m.plan.Base != 1 {
		t.Errorf("base should freeze the fork commit c0, got base %d", m.plan.Base)
	}
	if !m.plan.IsFrozen(0) {
		t.Error("the fork commit c0 (storage 0) should be frozen")
	}
}

func TestListNav_dayGroupAndTopBottomJumps(t *testing.T) {
	m := newTestModelN(t, 3) // all on one day -> day jump clamps to ends
	// On the list, } jumps to the next (older) day group; with one day it lands
	// on the oldest commit.
	m = send(m, "}")
	if m.listCursor != len(m.plan.Commits)-1 {
		t.Errorf("} with a single day should land on the oldest commit, got %d", m.listCursor)
	}
	// On the list, g jumps to the newest commit (top of list).
	m = send(m, "G") // oldest
	m = send(m, "g") // back to newest
	if m.listCursor != 0 {
		t.Errorf("g on the list should jump to the newest commit (index 0), got %d", m.listCursor)
	}
}

// A manual message edit must survive when the active strategy unstages (e.g. its
// dates are incomplete): unstageActive clears only the staged author dates, never
// the user's title/body edits.
func TestUnstage_preservesMessageEdits(t *testing.T) {
	m := newTestModelN(t, 3)
	c := m.commitAtCursor()
	neu := "Manually retitled"
	m.plan.SetEdit(c.Hash, model.Edit{NewSubject: &neu})

	// The active strategy (Even Spread) has unset dates, so staging unstages it.
	m.stageActive()

	e, ok := m.plan.EditFor(c.Hash)
	if !ok || e.NewSubject == nil || *e.NewSubject != neu {
		t.Errorf("unstaging the strategy must preserve the manual title edit, got %+v (ok=%v)", e, ok)
	}
}

// The Dates strategy owns the author-date dimension: a date typed in the per-commit
// editor is subsumed (cleared) when an unready strategy unstages, while that commit's
// message edit survives. This documents the intended ownership (spec: manual-date vs
// strategy-date coexistence is the strategy's to own in this workspace).
func TestUnstage_strategyOwnsAuthorDates(t *testing.T) {
	m := newTestModelN(t, 3)
	c := m.commitAtCursor()
	neu := "Kept title"
	d := c.OrigAuthorDate.AddDate(0, 0, 7)
	m.plan.SetEdit(c.Hash, model.Edit{NewSubject: &neu, NewAuthorDate: &d})

	m.stageActive() // Even Spread with unset dates -> unstageActive

	e, ok := m.plan.EditFor(c.Hash)
	if !ok || e.NewSubject == nil || *e.NewSubject != neu {
		t.Errorf("the message edit must survive unstage, got %+v (ok=%v)", e, ok)
	}
	if e.NewAuthorDate != nil {
		t.Error("the strategy owns author dates: a manual date edit is cleared on unstage")
	}
}

// The calendar previews as numbers only when a strategy staged a date distribution;
// a message-only edit (retitle/reword) must keep it in tile mode.
func TestStrategyStaged_ignoresMessageOnlyEdits(t *testing.T) {
	m := newTestModelN(t, 3)
	c := m.commitAtCursor()
	neu := "Retitled only"
	m.plan.SetEdit(c.Hash, model.Edit{NewSubject: &neu})
	if m.strategyStaged() {
		t.Error("a message-only edit must not count as a staged strategy")
	}
	d := c.OrigAuthorDate.AddDate(0, 0, 5)
	m.plan.SetEdit(c.Hash, model.Edit{NewSubject: &neu, NewAuthorDate: &d})
	if !m.strategyStaged() {
		t.Error("an author-date edit should count as a staged strategy")
	}
}

func TestAutoBase_mergeBaseOffFirstParentLineClaimsNoBase(t *testing.T) {
	fixtureMu.Lock()
	defer fixtureMu.Unlock()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = hermeticGitEnv(dir,
			"GIT_AUTHOR_DATE=2026-06-05T14:00:00+00:00",
			"GIT_COMMITTER_DATE=2026-06-05T14:00:00+00:00")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	commit := func(name string) {
		os.WriteFile(filepath.Join(dir, name+".txt"), []byte(name+"\n"), 0o644)
		run("add", ".")
		run("commit", "-q", "-m", "commit "+name)
	}
	run("init", "-q")
	run("symbolic-ref", "HEAD", "refs/heads/main")
	run("config", "user.name", "wlame")
	run("config", "user.email", "vvlame@gmail.com")
	run("config", "commit.gpgsign", "false")
	commit("c0")
	run("branch", "feature") // feature at c0
	commit("c1")             // main: c0 -> c1
	run("switch", "feature")
	commit("f0")                                 // feature diverges: c0 -> f0
	run("merge", "--no-ff", "--no-edit", "main") // real merge commit (parents f0, c1)

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
	m = updated.(Model)

	// applyAutoBase auto-picks "main", but its merge-base (c1) entered feature
	// via the merge's second parent — it's off feature's first-parent line — so
	// there is no honest cone to freeze. The label and the freeze must agree.
	if m.baseBranch != "" {
		t.Errorf("baseBranch should be empty when the merge-base is off the first-parent line, got %q", m.baseBranch)
	}
	if m.plan.Base != 0 {
		t.Errorf("plan.Base should be 0 (no freeze) when the merge-base is off the first-parent line, got %d", m.plan.Base)
	}
}

func TestListVisuals_setModeShowsCircleGutter(t *testing.T) {
	m := newTestModel(t) // 3 commits, none frozen, no flow open
	m.sel.mode = SelSet
	m.sel.set = map[string]bool{m.commitAtDisplayIndex(0).Hash: true}

	list := m.viewCommitList()
	if got := strings.Count(list, "●"); got != 1 {
		t.Errorf("exactly one ● (selected) expected in the list, got %d", got)
	}
	if !strings.Contains(list, "○") {
		t.Error("unmarked commits should show the ○ checkbox")
	}
	if !strings.Contains(list, "▸") {
		t.Error("the cursor caret stays visible beside the checkboxes")
	}
}

func TestListVisuals_rangeBeingMarkedShowsFilledCheckboxes(t *testing.T) {
	m := newTestModelN(t, 5) // 5 commits, none frozen
	m.sel.mode = SelRange
	m.sel.anchor = 0 // anchor at the newest
	m.listCursor = 2 // cursor 2 below it -> range [0,2]

	list := m.viewCommitList()
	if got := strings.Count(list, "●"); got != 3 {
		t.Errorf("a 3-commit band should show 3 filled checkboxes, got %d", got)
	}
}

func TestListVisuals_frozenRowsHaveNoCheckbox(t *testing.T) {
	m := newTestModelN(t, 5)
	m.plan.SetBase(3) // freeze the three oldest

	list := stripANSI(m.viewCommitList())

	if got := strings.Count(list, "○"); got != 2 {
		t.Errorf("only the 2 editable rows should show a checkbox, got %d", got)
	}
}

func TestListVisuals_redateFlowWithoutMarksFillsEveryEditableRow(t *testing.T) {
	m := send(newTestModelN(t, 4), "m", "d") // Active scope: every editable commit is a target

	list := m.viewCommitList()

	if got := strings.Count(list, "●"); got != 4 || strings.Contains(list, "○") {
		t.Errorf("Active scope should fill all 4 checkboxes, got %d ● (and ○ present: %v)",
			got, strings.Contains(list, "○"))
	}
}

func TestListTitle_saysWhatWillChange(t *testing.T) {
	m := newTestModelN(t, 6)
	if title := stripANSI(m.viewCommitList()); !strings.Contains(title, "all 6 editable") {
		t.Errorf("unmarked list title should say every commit is editable:\n%s", title)
	}
	m = send(m, " ", "down", " ")
	if title := stripANSI(m.viewCommitList()); !strings.Contains(title, "2 will change") {
		t.Errorf("list title should count the marks:\n%s", title)
	}
	m = send(m, "n", "m", "d")
	if title := stripANSI(m.viewCommitList()); !strings.Contains(title, "all 6 will change") {
		t.Errorf("redate flow title should say the whole range will change:\n%s", title)
	}
}

func TestListVisuals_globalModeDimsAndDropsCaret(t *testing.T) {
	m := newTestModel(t)
	m.sel.mode = SelGlobal
	if strings.Contains(m.viewCommitList(), "▸ ") {
		t.Error("Global mode dims the list and shows no cursor caret")
	}
}

func TestListVisuals_defaultModeShowsCaretAndEmptyCheckboxes(t *testing.T) {
	m := newTestModel(t) // 3 commits, none frozen, nothing marked
	list := m.viewCommitList()
	if !strings.Contains(list, "▸") {
		t.Error("default mode should show the ▸ cursor caret")
	}
	if got := strings.Count(list, "○"); got != 3 || strings.Contains(list, "●") {
		t.Errorf("default mode should show 3 empty checkboxes and no filled one, got %d ○", got)
	}
}

func TestSelectionKeys_spaceTogglesSet(t *testing.T) {
	m := newTestModel(t) // focusList, 3 commits, none frozen
	cursorHash := m.commitAtCursor().Hash
	m = send(m, " ")
	if m.sel.mode != SelSet || !m.sel.isSet(cursorHash) {
		t.Fatalf("space should select the cursor commit into a Set, got mode=%v", m.sel.mode)
	}
	m = send(m, " ")
	if m.sel.isSet(cursorHash) {
		t.Error("a second space should deselect the cursor commit")
	}
}

func TestSelectionKeys_selectAllAndClear(t *testing.T) {
	m := newTestModel(t)
	m = send(m, "s")
	if m.sel.mode != SelSet || len(m.sel.set) != len(m.plan.Active()) {
		t.Fatalf("s should select all %d active commits, got %d", len(m.plan.Active()), len(m.sel.set))
	}
	m = send(m, "n")
	if len(m.sel.set) != 0 {
		t.Errorf("n should clear the set, got %d", len(m.sel.set))
	}
}

// The 'a' key toggles the author column, which starts off, and the toggle is a
// pure display flag — it never touches the selection.
func TestAuthorToggle_keyTogglesColumnAndDefaultsOff(t *testing.T) {
	m := newTestModel(t)
	if m.showAuthor {
		t.Fatal("author column should default to off")
	}
	m = send(m, "a")
	if !m.showAuthor {
		t.Error("first 'a' should turn the author column on")
	}
	if m.sel.mode == SelSet {
		t.Error("'a' must not select commits (select-all moved to 's')")
	}
	m = send(m, "a")
	if m.showAuthor {
		t.Error("second 'a' should turn the author column off again")
	}
}

func TestSelectionKeys_visualRangeExtendsWithMovement(t *testing.T) {
	m := newTestModelN(t, 5)
	m = send(m, "v")
	if m.sel.mode != SelRange {
		t.Fatalf("v should start marking a range, got mode=%v", m.sel.mode)
	}
	m = send(m, "down", "down") // cursor 0 -> 2, range extends to [0,2]
	if lo, hi := m.sel.rangeBounds(m.listCursor); lo != 0 || hi != 2 {
		t.Errorf("range after two downs = [%d,%d], want [0,2]", lo, hi)
	}
	m = send(m, "v") // add the range to the marks
	if m.sel.mode != SelSet || len(m.markedHashes()) != 3 {
		t.Errorf("a second v should add the range to the marks: mode=%v marked=%d", m.sel.mode, len(m.markedHashes()))
	}
}

func TestSelectionKeys_rangeJoinsEarlierMarks(t *testing.T) {
	m := newTestModelN(t, 6)
	m = send(m, " ")                    // mark display 0
	m = send(m, "down", "down", "down") // cursor -> 3
	m = send(m, "v", "down", "enter")   // mark the range 3..4

	if got := len(m.markedHashes()); got != 3 {
		t.Errorf("marks after space + a range of 2 = %d, want 3", got)
	}
}

func TestSelectionKeys_escDropsOnlyThePendingRange(t *testing.T) {
	m := newTestModelN(t, 6)
	m = send(m, " ", "down", "down", "v", "down") // one mark, then a pending band of 2

	m = send(m, "esc")

	if m.sel.mode != SelSet || len(m.markedHashes()) != 1 {
		t.Fatalf("esc during a range should keep earlier marks: mode=%v marked=%d", m.sel.mode, len(m.markedHashes()))
	}
	m = send(m, "esc")
	if len(m.markedHashes()) != 0 {
		t.Errorf("a second esc should clear the marks, %d left", len(m.markedHashes()))
	}
}

func TestReword_rangeMarksTriggerMultiReword(t *testing.T) {
	m := newTestModelN(t, 5)
	m = send(m, "v", "down", "down", "v") // mark display 0..2

	if got := len(m.rewordTargets()); got != 3 {
		t.Errorf("reword targets after marking a range of 3 = %d, want 3", got)
	}
}

// TestSelectionKeys_escResetsSelectionToSingleWithNoFlow: with no flow open
// there is no implicit Active scope to fall back to, so esc after a Set
// selection lands on plain Single (the newSelection() default).
func TestSelectionKeys_escResetsSelectionToSingleWithNoFlow(t *testing.T) {
	m := newTestModel(t)
	m = send(m, "s") // Set mode with all selected
	m = send(m, "esc")
	if m.sel.mode != SelSingle {
		t.Errorf("esc should reset to Single with no flow open, got %v", m.sel.mode)
	}
}

// TestSelectionKeys_escResetsSelectionToActiveInRedateFlow: with the redate
// flow open, esc after a Set selection falls back to the flow's own implicit
// Active scope instead of Single (mirrors enterRedateFlow's own default).
func TestSelectionKeys_escResetsSelectionToActiveInRedateFlow(t *testing.T) {
	m := newTestModel(t)
	m.enterRedateFlow()
	m.focus = focusList // tab back to the list without leaving the flow
	m = send(m, "s")    // Set mode with all selected
	m = send(m, "esc")
	if m.sel.mode != SelActive {
		t.Errorf("esc should reset to the flow's Active default, got %v", m.sel.mode)
	}
}

// TestSelection_defaultModeIsSingleWithNoFlowOpen: with no flow open (the
// startup state — flows replaced the old per-tab default), the selection
// starts in plain Single mode. Entering the redate flow's own Active default
// is covered by TestEnterRedateFlowDefaultsToActiveScope (flow_test.go).
func TestSelection_defaultModeIsSingleWithNoFlowOpen(t *testing.T) {
	m := newTestModel(t)
	if m.sel.mode != SelSingle {
		t.Errorf("default selection mode should be Single with no flow open, got %v", m.sel.mode)
	}
}

func TestPipeline_applyShowsPreviewThenApplies(t *testing.T) {
	m := newTestModelN(t, 3) // clean local repo on main, no upstream
	c := m.commitAtCursor()
	nd := c.OrigAuthorDate.AddDate(0, 0, 3)
	m.plan.SetEdit(c.Hash, model.Edit{NewAuthorDate: &nd})

	m = send(m, "A")
	if m.mode != modePipeline {
		t.Fatalf("A should open the pipeline preview, got mode %v", m.mode)
	}
	if !m.pipelineApply {
		t.Error("A should stage the change in apply mode")
	}
	if !strings.Contains(m.View(), "→") {
		t.Error("the preview should render the date change arrow")
	}
	// enter applies on a clean local repo (no confirmation needed).
	m = send(m, "enter")
	if m.mode != modeMain {
		t.Fatalf("enter should apply and return to main, got mode %v", m.mode)
	}
	if m.statusKind != statusOK {
		t.Errorf("a successful apply should report statusOK, got %v", m.statusKind)
	}
}

func TestPipeline_escCancelsWithoutApplying(t *testing.T) {
	m := newTestModelN(t, 3)
	c := m.commitAtCursor()
	nd := c.OrigAuthorDate.AddDate(0, 0, 2)
	m.plan.SetEdit(c.Hash, model.Edit{NewAuthorDate: &nd})

	m = send(m, "A", "esc")
	if m.mode != modeMain {
		t.Fatalf("esc should cancel the pipeline, got mode %v", m.mode)
	}
	if !m.plan.HasChanges() {
		t.Error("esc must not apply — the staged edit should still be pending")
	}
}

func TestPipeline_peekDoesNotApply(t *testing.T) {
	m := newTestModelN(t, 3)
	c := m.commitAtCursor()
	nd := c.OrigAuthorDate.AddDate(0, 0, 4)
	m.plan.SetEdit(c.Hash, model.Edit{NewAuthorDate: &nd})

	m = send(m, "p")
	if m.mode != modePipeline || m.pipelineApply {
		t.Fatalf("p should open the preview in peek (non-apply) mode, got mode=%v apply=%v", m.mode, m.pipelineApply)
	}
	m = send(m, "enter") // enter in peek mode just closes
	if m.mode != modeMain {
		t.Fatalf("enter in peek mode should close the overlay, got mode %v", m.mode)
	}
	if !m.plan.HasChanges() {
		t.Error("peek must never apply")
	}
}

func TestPipeline_peekWithNoEditsReportsNothing(t *testing.T) {
	m := newTestModel(t) // no edits staged
	m = send(m, "p")
	if m.mode != modeMain {
		t.Errorf("p with no edits should stay in modeMain, got mode %v", m.mode)
	}
	if m.statusKind != statusWarn {
		t.Errorf("p with no edits should set statusWarn, got %v", m.statusKind)
	}
}

// TestPipeline_pushedRewriteRequiresTypedConfirm covers the confirm-gated apply
// path: when a plan edit touches a commit that is IsPushed AND inside
// plan.Active() (the true rewrite set — the merge-base freeze already keeps the
// genuinely shared p0/p1 prefix out of reach), safety.CheckGuards returns
// NeedsConfirmation = true with ConfirmWord = branch name. The pipeline must
// pause at modeConfirm and only proceed to apply when the user types the exact
// branch name; a wrong word must cancel with an error and leave the plan
// unchanged.
func TestPipeline_pushedRewriteRequiresTypedConfirm(t *testing.T) {
	// --- build a repo with two pushed commits then three local-only ones ---
	fixtureMu.Lock()
	defer fixtureMu.Unlock()
	dir := t.TempDir()
	origin := t.TempDir()
	git := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = hermeticGitEnv(dir,
			"GIT_AUTHOR_DATE=2026-06-05T14:00:00+00:00",
			"GIT_COMMITTER_DATE=2026-06-05T14:00:00+00:00")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	commit := func(name string) {
		os.WriteFile(filepath.Join(dir, name+".txt"), []byte(name+"\n"), 0o644)
		git("add", ".")
		git("commit", "-q", "-m", "commit "+name)
	}

	bareInit := exec.Command("git", "init", "-q", "--bare", origin)
	bareInit.Env = hermeticGitEnv(dir)
	bareInit.Run()
	git("init", "-q")
	git("symbolic-ref", "HEAD", "refs/heads/main")
	git("config", "user.name", "wlame")
	git("config", "user.email", "vvlame@gmail.com")
	git("config", "commit.gpgsign", "false")
	git("remote", "add", "origin", origin)

	commit("p0")
	commit("p1")
	git("push", "-q", "-u", "origin", "main")
	commit("local0")
	commit("local1")
	commit("local2")

	repo, err := gitio.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	state, _ := repo.ReadState()
	commits, _ := repo.ReadCommits()
	cfg := config.Default()
	cfg.Timezone = "UTC"

	// newPushedModel is a local helper that constructs a fresh Model from the
	// repo above and stages a date edit inside the ACTIVE range on a commit
	// marked pushed. p0/p1 are genuinely pushed but sit before the merge-base
	// freeze (Base), so an edit staged there would be inert — Replay never
	// visits a frozen commit, so a stale Edits entry for one can never actually
	// cause a rewrite, and CheckGuards correctly ignores it (plan.Active() is
	// the true rewrite set). Instead this marks the oldest ACTIVE commit as
	// pushed, simulating Task 1's all-remotes broadening: a commit on the
	// current branch independently pushed to some other remote (e.g. a fork)
	// that the merge-base-driven freeze has no way to know about — the
	// realistic case plan.Active()'s pushed-check exists to catch.
	newPushedModel := func() Model {
		m := New(repo, safety.NewManager(repo), cfg, state, commits)
		updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
		m = updated.(Model)

		idx := m.plan.Base // oldest active (non-frozen) commit
		m.plan.Commits[idx].IsPushed = true
		pushed := m.plan.Commits[idx]
		nd := pushed.OrigAuthorDate.AddDate(0, 0, 1)
		m.plan.SetEdit(pushed.Hash, model.Edit{NewAuthorDate: &nd})
		return m
	}

	// --- sub-test: wrong confirmation word cancels apply ---
	t.Run("wrong word keeps the prompt open to retype", func(t *testing.T) {
		m := newPushedModel()

		m = send(m, "A")
		if m.mode != modePipeline {
			t.Fatalf("A should open the pipeline preview, got mode %v", m.mode)
		}

		updated, _ := m.Update(key("enter"))
		m = updated.(Model)
		if m.mode != modeConfirm {
			t.Fatalf("enter on a pushed edit should require confirmation, got mode %v", m.mode)
		}

		// Type the wrong word and confirm: the prompt stays open (no apply, no
		// discard) so the user can fix a typo instead of re-staging.
		m.confirmInput.SetValue("nope")
		updated, _ = m.Update(key("enter"))
		m = updated.(Model)

		if m.mode != modeConfirm {
			t.Errorf("wrong confirm word should keep the prompt open, got mode %v", m.mode)
		}
		if !m.plan.HasChanges() {
			t.Error("wrong confirm word must not apply or discard — the edit should still be pending")
		}

		// esc from the prompt is the deliberate cancel.
		updated, _ = m.Update(key("esc"))
		m = updated.(Model)
		if m.mode == modeConfirm {
			t.Error("esc should leave the confirm prompt")
		}
	})

	// --- sub-test: correct confirmation word (branch name) applies ---
	t.Run("correct word applies", func(t *testing.T) {
		m := newPushedModel()

		m = send(m, "A")
		if m.mode != modePipeline {
			t.Fatalf("A should open the pipeline preview, got mode %v", m.mode)
		}

		updated, _ := m.Update(key("enter"))
		m = updated.(Model)
		if m.mode != modeConfirm {
			t.Fatalf("enter on a pushed edit should require confirmation, got mode %v", m.mode)
		}

		// Type the branch name (the required confirm word) and confirm.
		m.confirmInput.SetValue(m.state.Branch)
		updated, _ = m.Update(key("enter"))
		m = updated.(Model)

		if m.mode != modeMain {
			t.Errorf("correct confirm word should return to modeMain, got mode %v", m.mode)
		}
		if m.statusKind != statusOK {
			t.Errorf("correct confirm word should set statusOK, got %v (msg: %q)", m.statusKind, m.statusMsg)
		}
		// After a successful apply the plan reloads from the rewritten history,
		// so there are no longer any pending edits.
		if m.plan.HasChanges() {
			t.Error("after a successful apply the plan should have no pending changes")
		}
	})
}

func TestDateGroupingToggle(t *testing.T) {
	m := twoDayModel(t)
	if !m.groupByDate {
		t.Fatal("date grouping should default on")
	}
	m = send(m, "d")
	if m.groupByDate {
		t.Error("d should toggle date grouping off")
	}
	for _, r := range m.buildListRows() {
		if r.isDivider {
			t.Error("after toggling off, no dividers should be built")
		}
	}
	m = send(m, "d")
	if !m.groupByDate {
		t.Error("d should toggle grouping back on")
	}
}

// TestPipeline_pushedSplitRoutesToConfirm proves a split of pushed history is no
// longer un-appliable: its guard requires the typed branch confirmation, so enter
// in the preview routes to modeConfirm (instead of erroring at apply time).
func TestAffectedCommits_selectionWinsElseActive(t *testing.T) {
	m := newTestModelN(t, 4)
	// No explicit selection: the whole active range.
	if got, want := len(m.affectedCommits()), len(m.plan.Active()); got != want {
		t.Errorf("no selection should target all active (%d), got %d", want, got)
	}
	// Select two commits with space.
	m = send(m, " ")         // toggle the cursor commit
	m = send(m, "down", " ") // move down and toggle a second
	if got := len(m.affectedCommits()); got != 2 {
		t.Errorf("a 2-commit selection should target 2, got %d", got)
	}
}

func TestRestructureMenu_showsAffectedCount(t *testing.T) {
	m := newTestModelN(t, 4)
	m = send(m, ">") // switch to the Restructure workspace
	if !strings.Contains(m.View(), "4 commit(s)") {
		t.Errorf("Restructure title should show the active count, got:\n%s", m.View())
	}
}

func TestPipeline_pushedSplitRoutesToConfirm(t *testing.T) {
	m := newTestModelN(t, 3)
	m.state = model.RepoState{Branch: "main"}
	// Mark the split target itself pushed: splitChange.guard now checks the
	// actual rewrite set (target + tail) via model.AnyPushed, not whether the
	// repo has any upstream at all.
	target := m.plan.Commits[len(m.plan.Commits)-1] // newest
	target.IsPushed = true
	sc := splitChange{
		commit:     target,
		commitHash: target.Hash,
		groups:     []model.HunkGroup{{Order: 1, Subject: "part one"}, {Order: 2, Subject: "part two"}},
	}
	m.pending = sc
	m.pendingGuard = sc.guard(m.state)
	m.pipelineApply = true
	m.mode = modePipeline

	updated, _ := m.Update(key("enter"))
	m = updated.(Model)
	if m.mode != modeConfirm {
		t.Fatalf("a pushed split should route enter to the typed confirm, got mode %v", m.mode)
	}
	if m.confirmGuard.ConfirmWord != "main" {
		t.Errorf("the confirm word should be the branch name, got %q", m.confirmGuard.ConfirmWord)
	}
}

func TestDropBodies_popupConfirms(t *testing.T) {
	m := newTestModelN(t, 3)
	m = runFeatureNamed(t, m, "Drop bodies")
	if m.mode != modeDropBodies {
		t.Fatalf("D should open the drop-bodies popup, got %v", m.mode)
	}
	if m.plan.HasChanges() {
		t.Error("opening the popup must not drop bodies before confirm")
	}
	if !strings.Contains(m.View(), "Will drop the body of 3") {
		t.Errorf("popup should report the affected count, got:\n%s", m.View())
	}
	m = send(m, "enter")
	if m.mode != modeMain {
		t.Errorf("enter should close the popup, got %v", m.mode)
	}
	dropped := 0
	for _, c := range m.plan.Active() {
		if e, ok := m.plan.EditFor(c.Hash); ok && e.DropBody {
			dropped++
		}
	}
	if dropped != 3 {
		t.Errorf("enter should drop all 3 bodies, got %d", dropped)
	}
}

func TestDropBodies_escCancels(t *testing.T) {
	m := newTestModelN(t, 3)
	m = runFeatureNamed(t, m, "Drop bodies")
	m = send(m, "esc")
	if m.mode != modeMain {
		t.Errorf("esc should close the popup, got %v", m.mode)
	}
	if m.plan.HasChanges() {
		t.Error("esc must not drop any bodies")
	}
}

// A visual Range that spans into the frozen cone must yield only the editable
// commits in that span, root-first (oldest→newest), matching plan.Active()'s order.
func TestAffectedCommits_rangeExcludesFrozenRootFirst(t *testing.T) {
	m := newTestModelN(t, 5) // storage 0..4 (root-first); display d ↔ storage 4-d
	m.plan.SetBase(1)        // freeze storage index 0 (the oldest commit)
	// Visual range over display indices [2,4] (newest-side 2 .. oldest-side 4).
	m.sel.mode = SelRange
	m.sel.anchor = 2
	m.listCursor = 4
	got := m.affectedCommits()
	// display 2,3,4 -> storage 2,1,0; storage 0 is frozen -> excluded.
	if len(got) != 2 {
		t.Fatalf("range should yield 2 editable commits (frozen excluded), got %d", len(got))
	}
	// Root-first order: storage[1] then storage[2].
	if got[0].Hash != m.plan.Commits[1].Hash || got[1].Hash != m.plan.Commits[2].Hash {
		t.Errorf("range commits should be root-first storage[1],storage[2]; got %s,%s",
			short(got[0].Hash), short(got[1].Hash))
	}
}

// markedHashes feeds both squash (affectedChains) and reauthor
// (reauthorTargetCommits). A Range dragged across the base boundary, or a stale Set
// carrying frozen hashes, must never leak a frozen commit into that set — otherwise
// squash/reauthor would rewrite the frozen cone the UI renders as protected.
func TestMarkedHashes_excludesFrozenCommits(t *testing.T) {
	m := newTestModelN(t, 5) // storage 0..4 root-first; display d ↔ storage 4-d
	m.plan.SetBase(2)        // freeze storage indices 0 and 1
	frozen := map[string]bool{m.plan.Commits[0].Hash: true, m.plan.Commits[1].Hash: true}

	// Range over display [2,4] → storage 2,1,0; storage 0 and 1 are frozen.
	m.sel.mode = SelRange
	m.sel.anchor = 2
	m.listCursor = 4
	for h := range m.markedHashes() {
		if frozen[h] {
			t.Errorf("range selection leaked frozen commit %s", short(h))
		}
	}
	if got := len(m.markedHashes()); got != 1 { // only storage 2 is editable
		t.Fatalf("range should cover 1 editable commit, got %d", got)
	}

	// A Set that still carries a frozen hash must also drop it.
	m.sel.mode = SelSet
	m.sel.set = map[string]bool{m.plan.Commits[0].Hash: true, m.plan.Commits[3].Hash: true}
	got := m.markedHashes()
	if got[m.plan.Commits[0].Hash] {
		t.Error("set selection leaked frozen commit")
	}
	if !got[m.plan.Commits[3].Hash] {
		t.Error("set selection dropped an editable commit")
	}
}

// TestUpdateConfirm_wrongWordKeepsPromptAndPending proves a mistyped branch-name
// confirmation keeps the user in the prompt to retype instead of discarding the
// staged change; esc is the deliberate cancel.
func TestUpdateConfirm_wrongWordKeepsPromptAndPending(t *testing.T) {
	m := newTestModel(t)
	c := m.commitAtCursor()
	nd := c.OrigAuthorDate.AddDate(0, 0, 1)
	m.plan.SetEdit(c.Hash, model.Edit{NewAuthorDate: &nd})
	m.pending = newPlanChange(m.plan, m.loc, nil, nil)
	m.confirmGuard = safety.Guard{NeedsConfirmation: true, ConfirmWord: "main"}
	ti := textinput.New()
	ti.SetValue("wrong")
	m.confirmInput = ti
	m.mode = modeConfirm

	updated, _ := m.updateConfirm(key("enter"))
	m2 := updated.(Model)
	if m2.mode != modeConfirm {
		t.Errorf("a wrong confirm word should keep the prompt open, got mode %v", m2.mode)
	}
	if m2.pending == nil {
		t.Error("a wrong confirm word must not discard the staged change")
	}

	updated, _ = m2.updateConfirm(key("esc"))
	if m3 := updated.(Model); m3.pending != nil {
		t.Error("esc should cancel and clear the staged change")
	}
}

// TestSpinnerBusy_reflectsAsyncWork proves the throbber tick loop only stays armed
// while async generation is pending, so it doesn't wake the event loop forever.
func TestSpinnerBusy_reflectsAsyncWork(t *testing.T) {
	m := newTestModel(t)
	if m.spinnerBusy() {
		t.Error("a fresh model has no async work and must not be busy")
	}
	m.mode = modeSplitGen
	if !m.spinnerBusy() {
		t.Error("a split generation should read as busy")
	}
	m.mode = modeMain
	m.rewordSt.inFlight = map[string]bool{"abc": true}
	if !m.spinnerBusy() {
		t.Error("an in-flight reword should read as busy")
	}
	delete(m.rewordSt.inFlight, "abc")
	m.squashSt.loading = map[string]bool{"def": true}
	if !m.spinnerBusy() {
		t.Error("a loading squash summary should read as busy")
	}
}

// TestGlobalAction_detailWithNoCommitsDoesNotPanic guards the empty branch: pressing
// '.' must not open the detail overlay (which would index an empty commit slice).
func TestGlobalAction_detailWithNoCommitsDoesNotPanic(t *testing.T) {
	m := newTestModel(t)
	m.plan = model.NewPlan(nil) // empty branch
	m.listCursor = 0
	m2 := send(m, ".")
	if m2.mode == modeDetail {
		t.Error("detail overlay must not open with zero commits")
	}
}

func TestDropBodies_honorsSelection(t *testing.T) {
	m := newTestModelN(t, 4)
	m = send(m, " ") // select the cursor (newest) commit
	m = runFeatureNamed(t, m, "Drop bodies")
	if !strings.Contains(m.View(), "Will drop the body of 1") {
		t.Errorf("with one commit selected the popup should target 1, got:\n%s", m.View())
	}
	m = send(m, "enter")
	dropped := 0
	for _, c := range m.plan.Commits {
		if e, ok := m.plan.EditFor(c.Hash); ok && e.DropBody {
			dropped++
		}
	}
	if dropped != 1 {
		t.Errorf("only the selected commit's body should drop, got %d", dropped)
	}
}

// TestDropBodies_contiguousSelectionLogsCommitsRange verifies that a
// contiguous, non-root Set selection renders a real --commits=<range> line
// (not the whole-branch default), reproducing exactly what was affected.
func TestDropBodies_contiguousSelectionLogsCommitsRange(t *testing.T) {
	m := newTestModelN(t, 4) // display: 0=newest .. 3=oldest/root
	m = send(m, " ")         // select display 0
	m = send(m, "down", " ") // select display 1 too -> {display0, display1} contiguous, non-root
	m = runFeatureNamed(t, m, "Drop bodies")
	m = send(m, "enter")
	found := false
	for _, l := range m.planCmds {
		if strings.Contains(l, "drop-bodies") && strings.Contains(l, "--commits=") {
			found = true
		}
	}
	if !found {
		t.Errorf("contiguous selection should log a --commits=<range> line, got %v", m.planCmds)
	}
}

// TestDropBodies_nonContiguousSelectionLogsHonestNote verifies that a
// non-contiguous selection cannot be expressed as one --commits= range (the
// headless CLI takes only "", "A..B", or a single ref), so the footer gets an
// honest "#" note instead of a wrong whole-branch command.
func TestDropBodies_nonContiguousSelectionLogsHonestNote(t *testing.T) {
	m := newTestModelN(t, 4)         // display: 0=newest .. 3=oldest/root
	m = send(m, " ")                 // select display 0
	m = send(m, "down", "down", " ") // skip display 1, select display 2 -> a gap
	m = runFeatureNamed(t, m, "Drop bodies")
	m = send(m, "enter")
	found := false
	for _, l := range m.planCmds {
		if strings.HasPrefix(l, "#") && strings.Contains(l, "drop-bodies") {
			found = true
		}
	}
	if !found {
		t.Errorf("non-contiguous selection should log an honest note, got %v", m.planCmds)
	}
}

// TestCommitPending_honestNoteNotWrittenToOnDiskLog drives a non-contiguous
// drop-bodies selection through the real apply path (commitPending) instead of
// stopping at staging, then checks both directions of the "#"-note rule: the
// honest note must land in the in-memory footer panel (m.commandLog) but must
// never reach the on-disk .git/regit/command.log file. Tests that only inspect
// planCmds (like TestDropBodies_nonContiguousSelectionLogsHonestNote above)
// would not notice a regression that drops or inverts the
// `if !strings.HasPrefix(line, "#")` guard inside commitPending.
func TestCommitPending_honestNoteNotWrittenToOnDiskLog(t *testing.T) {
	m := newTestModelN(t, 4)         // display: 0=newest .. 3=oldest/root
	m = send(m, " ")                 // select display 0
	m = send(m, "down", "down", " ") // skip display 1, select display 2 -> a gap
	m = runFeatureNamed(t, m, "Drop bodies")
	m = send(m, "enter") // confirm the popup -> stages DropBody edits + the "#" note
	staged := false
	for _, l := range m.planCmds {
		if strings.HasPrefix(l, "#") && strings.Contains(l, "drop-bodies") {
			staged = true
		}
	}
	if !staged {
		t.Fatalf("setup: expected the honest note staged in planCmds, got %v", m.planCmds)
	}

	m = send(m, "A")     // stage the plan for apply -> modePipeline
	m = send(m, "enter") // apply through the real commitPending, flushing planCmds
	if m.statusKind == statusErr {
		t.Fatalf("apply errored: %s", m.statusMsg)
	}

	inMemory := false
	for _, l := range m.commandLog {
		if strings.HasPrefix(l, "#") && strings.Contains(l, "drop-bodies") {
			inMemory = true
		}
	}
	if !inMemory {
		t.Errorf("the honest note should be in the in-memory command log after apply, got %v", m.commandLog)
	}

	gitDir, err := m.repo.GitDir()
	if err != nil {
		t.Fatalf("GitDir: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(gitDir, "regit", "command.log"))
	if err != nil {
		t.Fatalf("reading on-disk command log: %v", err)
	}
	onDisk := string(raw)
	if strings.Contains(onDisk, "# drop-bodies") {
		t.Errorf("the honest note must never be written to the on-disk command log, got:\n%s", onDisk)
	}
	// Confirm the flush actually wrote real lines to the file, so the note's
	// absence is a deliberate exclusion and not just an empty/untouched file.
	if !strings.Contains(onDisk, "verified") {
		t.Errorf("on-disk log should still contain the real verification line, got:\n%s", onDisk)
	}
}

// mkBranch creates a git branch in the model's repo so a picker test can target
// a branch other than the current one.
func mkBranch(t *testing.T, m Model, name string) {
	t.Helper()
	fixtureMu.Lock()
	defer fixtureMu.Unlock()
	cmd := exec.Command("git", "branch", name)
	cmd.Dir = m.repo.Root()
	cmd.Env = hermeticGitEnv(m.repo.Root())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git branch %s: %v\n%s", name, err, out)
	}
}

func TestStrategyCarousel_cyclesAndRepaints(t *testing.T) {
	m := newTestModelN(t, 3)
	m.enterRedateFlow()
	m = send(m, "tab") // focus the strategy pane (calendar -> params)
	if m.focus != focusParams {
		t.Fatalf("tab should focus the params pane, got %v", m.focus)
	}
	start := m.activeStrat().form.strat.Name()
	m = send(m, "right") // carousel: next strategy
	if m.activeStrat().form.strat.Name() == start {
		t.Errorf("→ should advance the strategy carousel, still on %q", start)
	}
	if !strings.Contains(m.View(), m.activeStrat().form.strat.Name()) {
		t.Error("the pane should show the newly selected strategy name")
	}
	m = send(m, "left")
	if m.activeStrat().form.strat.Name() != start {
		t.Errorf("← should return to %q, got %q", start, m.activeStrat().form.strat.Name())
	}
}

func TestStrategyPalette_removed(t *testing.T) {
	m := newTestModelN(t, 3)
	if mm := send(m, "s"); mm.mode != modeMain {
		t.Errorf("s should no longer open a palette, mode = %v", mm.mode)
	}
}

// typeDate types a YYYY-MM-DD value into the focused form field, char by char.
func typeDate(m Model, date string) Model {
	for _, ch := range date {
		m = send(m, string(ch))
	}
	return m
}

func TestStrategy_unsetDatesAreNotApplicable(t *testing.T) {
	m := newTestModelN(t, 3)
	m = send(m, "tab", "tab") // strategy pane, Even Spread, dates UNSET
	if m.activeStrat().form.ready() {
		t.Error("a date-range strategy with unset dates must not be ready")
	}
	if m.plan.HasChanges() {
		t.Error("an unready strategy must stage nothing")
	}
}

func TestStrategy_fillingDatesStagesLive(t *testing.T) {
	m := newTestModelN(t, 3)
	m.enterRedateFlow()
	m = send(m, "tab")            // strategy pane (Even Spread)
	m = send(m, "down")           // move from carousel row to Start date field
	m = typeDate(m, "2026-06-01") // Start date field
	m = send(m, "down")           // to End date
	m = typeDate(m, "2026-06-30")
	if !m.activeStrat().form.ready() {
		t.Fatal("both dates set -> strategy should be ready")
	}
	if !m.plan.HasChanges() {
		t.Error("a ready strategy should stage its distribution live")
	}
}

// TestStageActive_RespectsSetSelection: with an explicit Set selection active,
// staging must edit ONLY the selected commits — the whole-active-range default
// must not leak through once the user has picked specific commits.
func TestStageActive_RespectsSetSelection(t *testing.T) {
	m := newTestModelN(t, 6)
	// Select commits at storage indices 2 and 4 (Set mode).
	m.sel.toggle(m.plan.Commits[4].Hash, false)
	m.sel.toggle(m.plan.Commits[2].Hash, false)

	// Fill Even Spread's date range through the real key path — the same one
	// TestStrategy_fillingDatesStagesLive uses — since updateParamsPane already
	// re-stages live on every keystroke; no separate stage call is needed.
	m.enterRedateFlow()
	m = send(m, "tab")  // calendar -> params (Even Spread, default)
	m = send(m, "down") // carousel -> Start date
	m = typeDate(m, "2026-06-01")
	m = send(m, "down") // Start date -> End date
	m = typeDate(m, "2026-06-30")

	for i, c := range m.plan.Commits {
		e, ok := m.plan.EditFor(c.Hash)
		edited := ok && e.NewAuthorDate != nil
		want := i == 2 || i == 4
		if edited != want {
			t.Errorf("commit[%d] edited=%v want %v", i, edited, want)
		}
	}

	// The footer's logged command must teach the exact non-interactive scope
	// too — "whole active range" would be silently wrong once a selection is
	// staged, so stageActive must render --commits= into m.redateCmd.
	if !strings.Contains(m.redateCmd, "--commits=") {
		t.Errorf("redateCmd should carry the selection scope, got %q", m.redateCmd)
	}
}

// TestStageActive_NoSelectionStagesWholeActiveRange: with no explicit
// selection (the workspace default), staging must cover every active commit.
func TestStageActive_NoSelectionStagesWholeActiveRange(t *testing.T) {
	m := newTestModelN(t, 4)
	m.enterRedateFlow()
	m = send(m, "tab")
	m = send(m, "down")
	m = typeDate(m, "2026-06-01")
	m = send(m, "down")
	m = typeDate(m, "2026-06-30")

	staged := 0
	for _, c := range m.plan.Active() {
		if e, ok := m.plan.EditFor(c.Hash); ok && e.NewAuthorDate != nil {
			staged++
		}
	}
	if staged != len(m.plan.Active()) {
		t.Errorf("staged %d of %d active commits", staged, len(m.plan.Active()))
	}
}

func TestReset_clearsAllStrategyState(t *testing.T) {
	m := newTestModelN(t, 3)
	m.enterRedateFlow()
	m = send(m, "tab")
	m = send(m, "down")
	m = typeDate(m, "2026-06-01")
	m = send(m, "down")
	m = typeDate(m, "2026-06-30")
	if !m.plan.HasChanges() {
		t.Fatal("setup: expected staged edits")
	}
	m = send(m, "R") // reset
	if m.plan.HasChanges() {
		t.Error("reset should clear staged edits")
	}
	if m.activeStrat().form.ready() {
		t.Error("reset should return date fields to unset (greyed)")
	}
}

func TestApply_blockedUntilStrategyReady(t *testing.T) {
	m := newTestModelN(t, 3)
	m = send(m, "tab", "tab") // strategy pane, dates unset -> nothing staged
	m = send(m, "A")          // apply
	if m.plan.HasChanges() {
		t.Error("apply must not stage anything when the strategy is unready")
	}
	if m.statusKind != statusWarn {
		t.Errorf("apply with nothing staged should warn, got kind %v", m.statusKind)
	}
}

func TestBranchPicker_dirtyErrorStaysInOverlay(t *testing.T) {
	m := newTestModelN(t, 3)
	mkBranch(t, m, "other")
	// A real modification: the switch re-reads the working tree from disk.
	if err := os.WriteFile(filepath.Join(m.repo.Root(), "c000.txt"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m = send(m, "B") // open the branch picker
	if m.mode != modePicker {
		t.Fatalf("B should open the picker, got %v", m.mode)
	}
	m = send(m, "o", "t", "h", "e", "r") // filter to the "other" branch
	m = send(m, "enter")                 // switch attempt -> blocked (dirty)
	if m.mode != modePicker {
		t.Fatalf("a blocked switch must keep the picker open, got %v", m.mode)
	}
	if m.picker.errMsg == "" {
		t.Fatal("the dirty-tree guard should set the picker error")
	}
	if !strings.Contains(m.View(), "uncommitted changes") {
		t.Errorf("the error should render inside the picker overlay, got:\n%s", m.View())
	}
	m = send(m, "x") // typing in the filter clears the stale error
	if m.picker.errMsg != "" {
		t.Error("typing should clear the picker error")
	}
}

func TestRecomputeViolations_populatesShiftedBlastRadius(t *testing.T) {
	// Four commits share one author date. Editing the second-oldest far into the
	// future leaves the two newer commits before it: one is a pairwise violation,
	// both are in the cascade blast radius. So Shifted (2) exceeds Violations (1).
	m := newTestModelN(t, 4)
	edited := m.plan.Commits[1] // root-first index 1
	future := edited.OrigAuthorDate.AddDate(0, 0, 30)
	m.plan.SetEdit(edited.Hash, model.Edit{NewAuthorDate: &future})

	m.recomputeViolations()

	if len(m.plan.Violations) != 1 {
		t.Fatalf("expected 1 pairwise violation, got %d", len(m.plan.Violations))
	}
	if len(m.plan.Shifted) != 2 {
		t.Fatalf("expected 2 shifted commits (blast radius), got %d: %v", len(m.plan.Shifted), m.plan.Shifted)
	}
}

func TestReportPlan_warnsWithConflictCount(t *testing.T) {
	m := newTestModelN(t, 4)
	edited := m.plan.Commits[1]
	future := edited.OrigAuthorDate.AddDate(0, 0, 30)
	m.plan.SetEdit(edited.Hash, model.Edit{NewAuthorDate: &future})
	m.recomputeViolations()

	m.reportPlan("Edit")

	if m.statusKind != statusWarn {
		t.Errorf("expected statusWarn, got %v", m.statusKind)
	}
	if !strings.Contains(m.statusMsg, "2 conflicting") {
		t.Errorf("status should report 2 conflicting, got %q", m.statusMsg)
	}
}

func TestReportPlan_cleanPlanIsSequential(t *testing.T) {
	m := newTestModelN(t, 3)
	c := m.commitAtCursor() // newest commit; editing the tip cannot reorder
	nd := c.OrigAuthorDate.AddDate(0, 0, 1)
	m.plan.SetEdit(c.Hash, model.Edit{NewAuthorDate: &nd})
	m.recomputeViolations()

	m.reportPlan("Edit")

	if !strings.Contains(m.statusMsg, "✓ sequential") {
		t.Errorf("clean plan should report sequential, got %q", m.statusMsg)
	}
}

func TestCalendarPane_showsConflictCountNotOldViolation(t *testing.T) {
	m := newTestModelN(t, 4)
	edited := m.plan.Commits[1] // root-first index 1
	future := edited.OrigAuthorDate.AddDate(0, 0, 30)
	m.plan.SetEdit(edited.Hash, model.Edit{NewAuthorDate: &future})
	m.recomputeViolations() // Violations = 1, Shifted = 2

	out := m.viewCalendarPane()
	if !strings.Contains(out, "⚠ 2 conflicting") {
		t.Errorf("calendar pane should show the shifted conflict count, got:\n%s", out)
	}
	if strings.Contains(out, "out-of-order") || strings.Contains(out, "✗") {
		t.Errorf("calendar pane should not use the old out-of-order/✗ marker, got:\n%s", out)
	}
}

// TestPipelineCancel_clearsPendingKeepsPlanLines guards the ownership-model fix:
// esc/q in a PEEK of the pipeline overlay must clear m.pending (so a stale
// direct-change's command line — owned by the change object, not a shared slot —
// cannot leak into a later unrelated apply) but must NOT drop the plan-edit
// lines (redateCmd here) staged before the peek, since those still describe
// m.plan, which survives the cancel. This inverts the old single-slot test's
// assertion (esc used to wipe the shared pendingCmds slot outright, which is
// exactly the bug that dropped a real staged redate line after a peek).
func TestPipelineCancel_clearsPendingKeepsPlanLines(t *testing.T) {
	m := newTestModel(t)
	m = applyStrategyInPane(m) // stage edits (tab tab enter) so p works
	if m.redateCmd == "" {
		t.Fatal("setup: applyStrategyInPane should have set redateCmd")
	}
	m = send(m, "p") // open the pipeline overlay (modePipeline) as a peek
	if m.mode != modePipeline {
		t.Fatalf("p should open the pipeline overlay, got %v", m.mode)
	}
	m = send(m, "esc") // cancel the peek
	if m.mode != modeMain {
		t.Fatalf("esc should return to main mode, got %v", m.mode)
	}
	if m.pending != nil {
		t.Error("esc in pipeline overlay should clear the pending change")
	}
	if m.redateCmd == "" {
		t.Error("esc from a peek must not drop the staged plan-edit line (redateCmd)")
	}
}

// TestFooter_undoIsLogged: an undo appends a `regit undo` line to the command log.
func TestFooter_undoIsLogged(t *testing.T) {
	m := newTestModelN(t, 3)
	// Stage + apply a strategy so a backup exists to undo. Real idiom: stage with
	// applyStrategyInPane(m), then drive send(m,"A") → send(m,"enter") to apply
	// through commitPending (creates the backup ref). See TestDetail_refetchesAfterApply
	// (app_test.go) for the exact apply sequence to copy.
	m = applyStrategyInPane(m)
	m = send(m, "A")     // stage → modePipeline
	m = send(m, "enter") // apply through commitPending
	before := len(m.commandLog)
	updated, _ := m.doUndo()
	m = updated.(Model)
	found := false
	for _, l := range m.commandLog[before:] {
		if strings.Contains(l, "regit undo") {
			found = true
		}
	}
	if !found {
		t.Errorf("undo should append a `regit undo` line; log tail: %v", m.commandLog[before:])
	}
}

// TestFooter_compoundApplyLogsAllPlanLines proves the redateCmd++planCmds
// snapshot taken by stagePlanApply actually reaches the on-disk log: stage a
// strategy (redateCmd) AND a drop-bodies edit (a planCmds line) together, apply
// once, and confirm BOTH command lines survive to command.log alongside the
// verification line. TestFooter_stagingStrategyLogsExactlyOneRedateLine and
// TestFooter_directApplyDoesNotFlushPlanLines (footer_ownership_test.go) each
// cover one side of the split in isolation; neither drives a plan apply staging
// both redateCmd and a real (non-"#") planCmds entry at once, so a regression
// that dropped one half of the concatenation in stagePlanApply could still pass
// both of those.
func TestFooter_compoundApplyLogsAllPlanLines(t *testing.T) {
	m := newTestModelN(t, 4) // display: 0=newest .. 3=oldest/root

	m = applyStrategyInPane(m) // stages redateCmd
	if m.redateCmd == "" {
		t.Fatal("setup: applyStrategyInPane should have set redateCmd")
	}

	m = runFeatureNamed(t, m, "Drop bodies") // stages a planCmds line
	m = send(m, "enter")                     // confirm the drop-bodies popup
	if len(m.planCmds) == 0 {
		t.Fatal("setup: drop-bodies should have staged a planCmds line")
	}

	m = send(m, "A")     // stagePlanApply snapshots redateCmd ++ planCmds
	m = send(m, "enter") // apply through the real commitPending
	if m.statusKind == statusErr {
		t.Fatalf("apply errored: %s", m.statusMsg)
	}

	gitDir, err := m.repo.GitDir()
	if err != nil {
		t.Fatalf("GitDir: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(gitDir, "regit", "command.log"))
	if err != nil {
		t.Fatalf("reading on-disk command log: %v", err)
	}
	onDisk := string(raw)
	if !strings.Contains(onDisk, "regit redate") {
		t.Errorf("on-disk log should contain the redate line, got:\n%s", onDisk)
	}
	if !strings.Contains(onDisk, "regit drop-bodies") {
		t.Errorf("on-disk log should contain the drop-bodies line, got:\n%s", onDisk)
	}
	if !strings.Contains(onDisk, "verified") {
		t.Errorf("on-disk log should contain the verification line, got:\n%s", onDisk)
	}
}

// TestSelectionCommitSpec_rangeVsNonContiguous: a contiguous non-root selection
// yields a range; a non-contiguous one is not expressible.
func TestSelectionCommitSpec_rangeVsNonContiguous(t *testing.T) {
	// Root-first hashes a,b,c,d,e (storage indices 0..4). modelWithSet selects a
	// Set of the named hashes; b,c,d is a contiguous non-root run (indices 1..3).
	commits := linearModelCommits("a", "b", "c", "d", "e")
	m := modelWithSet(commits, "b", "c", "d")
	spec, ok := m.selectionCommitSpec()
	if !ok || !strings.Contains(spec, "..") {
		t.Errorf("contiguous non-root selection should be an expressible range, got %q ok=%v", spec, ok)
	}
	// Non-contiguous {b,d} (indices 1,3) → not expressible as one range.
	m2 := modelWithSet(commits, "b", "d")
	if _, ok := m2.selectionCommitSpec(); ok {
		t.Error("a non-contiguous selection must not be expressible as one range")
	}
}

// TestRedateCommitSpec_allSelectionForms exercises every branch of
// redateCommitSpec: a non-adjacent Set selection falls back to a comma-joined
// short-hash list (root-first, since resolveTargetSet unions comma parts), a
// contiguous non-root Range selection renders the A^..B form, and no explicit
// selection renders empty (the flag's own "whole active range" default).
func TestRedateCommitSpec_allSelectionForms(t *testing.T) {
	commits := linearModelCommits(
		"1111111111111111111111111111111111aaaa",
		"2222222222222222222222222222222222bbbb",
		"3333333333333333333333333333333333cccc",
		"4444444444444444444444444444444444dddd",
		"5555555555555555555555555555555555eeee",
	)

	// Non-adjacent Set: root-first storage indices 0 and 2, a gap at 1.
	setModel := modelWithSet(commits, commits[0].Hash, commits[2].Hash)
	wantSet := short(commits[0].Hash) + "," + short(commits[2].Hash)
	if got := setModel.redateCommitSpec(); got != wantSet {
		t.Errorf("non-adjacent Set spec = %q, want %q", got, wantSet)
	}

	// Contiguous non-root Range over storage indices 2,3 (display indices 1,2,
	// since display is newest-first), confirmed through the real selection API
	// (beginVisual/confirmVisual) the same way the list's visual mode does.
	rangeModel := Model{plan: model.NewPlan(commits), sel: newSelection()}
	rangeModel.listCursor = 1
	rangeModel.sel.beginVisual(rangeModel.listCursor)
	rangeModel.listCursor = 2
	rangeModel.finishRange()
	wantRange := short(commits[2].Hash) + "^.." + short(commits[3].Hash)
	if got := rangeModel.redateCommitSpec(); got != wantRange {
		t.Errorf("contiguous Range spec = %q, want %q", got, wantRange)
	}

	// No explicit selection: the workspace default is the whole active range,
	// which is already the --commits flag's own default, so the spec is empty.
	noSelModel := Model{plan: model.NewPlan(commits), sel: newSelection()}
	if got := noSelModel.redateCommitSpec(); got != "" {
		t.Errorf("no-selection spec = %q, want empty", got)
	}
}

// TestHelpOverlay_scrolls: down/up change the visible window and clamp at both
// ends on a short terminal.
func TestHelpOverlay_scrolls(t *testing.T) {
	m := newTestModel(t)
	m.width, m.height = 100, 20 // fewer rows than helpText lines
	m.mode = modeHelp
	first := m.View()
	down, _ := m.handleKey(key("down"))
	m = down.(Model)
	if m.helpOffset == 0 {
		t.Error("down should advance helpOffset")
	}
	if m.View() == first {
		t.Error("scrolling should change the help view")
	}
	// clamp at top
	for i := 0; i < 100; i++ {
		up, _ := m.handleKey(key("up"))
		m = up.(Model)
	}
	if m.helpOffset != 0 {
		t.Errorf("helpOffset should clamp at 0, got %d", m.helpOffset)
	}
}

// TestCommitPending_statusSaysCapitalU: the post-apply hint uses the real binding U.
func TestCommitPending_statusSaysCapitalU(t *testing.T) {
	m := newTestModelN(t, 3)
	m = applyStrategyInPane(m)
	m = send(m, "A")     // stage → modePipeline
	m = send(m, "enter") // apply through commitPending (sets the status)
	if !strings.Contains(m.statusMsg, "press U to undo") {
		t.Errorf("apply status should say 'press U to undo', got %q", m.statusMsg)
	}
}

// TestStatus_clearedWhenTheScreenChanges: a message about the previous screen
// must not linger on the next one, where it reads as a new error.
func TestStatus_clearedWhenTheScreenChanges(t *testing.T) {
	m := newTestModelN(t, 3)
	m.setStatus("Marks cleared", statusOK)
	m = send(m, "m") // open the action panel: a new screen
	if m.statusMsg == "Marks cleared" {
		t.Errorf("opening the action panel should clear the old status, still %q", m.statusMsg)
	}
}

// TestStatus_keptWhenTheKeySetsANewOne: a key that changes the screen AND
// reports something keeps its own message.
func TestStatus_keptWhenTheKeySetsANewOne(t *testing.T) {
	m := newTestModelN(t, 3)
	m = send(m, "m")
	m = send(m, "esc") // closing the panel
	m.setStatus("Squash cancelled", statusOK)
	before := m.statusMsg
	m = send(m, "j") // same screen: the status stays
	if m.statusMsg != before {
		t.Errorf("moving the cursor should not clear the status, got %q", m.statusMsg)
	}
}
