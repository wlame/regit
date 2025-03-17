package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/wlame/regit/internal/config"
	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/safety"
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
	if !strings.Contains(out, "regit") {
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
	// The per-row date moved to the detail panel; the row carries hash + subject.
	if !strings.Contains(entryLine, "Refactor the authentication") {
		t.Errorf("entry line should carry the subject, got: %q", entryLine)
	}

	// All test commits share one day, so the list shows exactly one date divider
	// plus one line per visible commit within the listHeight-1 window.
	if !strings.Contains(view, "commit(s)") {
		t.Error("a date divider should head the day group")
	}
	window := m.listHeight() - 1
	gotVisible := 0
	for _, c := range m.plan.Commits {
		if strings.Contains(view, c.Hash[:8]) {
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

// On startup the list cursor sits on the oldest unpushed commit, so the user
// can press b to freeze all pushed history immediately.
func TestStartup_cursorOnFirstUnpushedCommit(t *testing.T) {
	dir := t.TempDir()
	origin := t.TempDir()
	git := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
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

	exec.Command("git", "init", "-q", "--bare", origin).Run()
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

// D marks every editable commit that has a body with a DropBody edit, while
// frozen commits stay untouched.
func TestBulkDropBodies_marksActiveOnly(t *testing.T) {
	m := newTestModelN(t, 4)
	m = send(m, "down", "down", "b") // base at storage index 1: root frozen
	m = send(m, "D")

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
	m = send(m, "R")
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
	m = send(m, "R")
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
	m = send(m, "R")
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
	m = send(m, "R")
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
	m = send(m, "R", "tab", "tab", "tab", " ", "enter")
	if m.mode != modeRetitle || m.retitle.errMsg == "" {
		t.Error("a no-op toggle run should stay open and report no changes")
	}
}

// The transform helpers behave sensibly at the edges.
func TestRetitleTransform_edgeCases(t *testing.T) {
	plain := retitleTransform(nil, "", false, true)
	for _, tc := range []struct{ in, want string }{
		{"Fix login", "Fix login."},
		{"Fix login.", "Fix login."},
		{"Fix it!", "Fix it!"}, // already ends as a sentence
		{"What?", "What?"},
		{"", ""}, // never dot an empty title
	} {
		if got := plain(tc.in); got != tc.want {
			t.Errorf("ensure-dot(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	capital := retitleTransform(nil, "", true, false)
	for _, tc := range []struct{ in, want string }{
		{"fix login", "Fix login"},
		{"éclair fix", "Éclair fix"},
		{"123 fix", "123 fix"},
		{"", ""},
	} {
		if got := capital(tc.in); got != tc.want {
			t.Errorf("capitalize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Invalid patterns and zero-match patterns keep the modal open with an error.
func TestRetitle_invalidPatternShowsError(t *testing.T) {
	m := newTestModelN(t, 3)
	m = send(m, "R", "(", "enter")
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
	m = send(m, "R")
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

// Digits typed while staying on the same day build a two-digit quota
// (2 → 24 → 45); moving the cursor starts a fresh entry.
func TestCalendarQuotas_twoDigitEntry(t *testing.T) {
	m := newTestModel(t)
	m = send(m, "tab") // focus calendar (cursor on 2026-06-05)
	day := m.calCursor.In(m.loc).Format("2006-01-02")

	m = send(m, "2")
	if got := m.quotas[day]; got != 2 {
		t.Fatalf("first digit: want quota 2, got %d", got)
	}
	m = send(m, "4")
	if got := m.quotas[day]; got != 24 {
		t.Fatalf("second digit should append: want 24, got %d", got)
	}
	m = send(m, "5")
	if got := m.quotas[day]; got != 45 {
		t.Fatalf("third digit should keep the last two: want 45, got %d", got)
	}

	// Moving away and typing starts a new number on the new day…
	m = send(m, "right", "7")
	other := m.calCursor.In(m.loc).Format("2006-01-02")
	if got := m.quotas[other]; got != 7 {
		t.Errorf("digit after a move should start fresh: want 7, got %d", got)
	}
	// …and returning to the first day also starts fresh (3, not 53).
	m = send(m, "left", "3")
	if got := m.quotas[day]; got != 3 {
		t.Errorf("digit after returning should start fresh: want 3, got %d", got)
	}
}

// Space picks/unpicks calendar days for the Picked Days strategy; the random
// split applies immediately and reshuffles as the set changes.
func TestPickedDays_spaceTogglesAndApplies(t *testing.T) {
	m := newTestModelN(t, 6)
	m = send(m, "tab", " ") // pick the cursor day (2026-06-05)
	day := m.calCursor.In(m.loc).Format("2006-01-02")
	if !m.picked[day] {
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
	if m.picked[day2] {
		t.Fatal("space again should unpick the day")
	}
	if got := m.dayCounts()[day]; got != 6 {
		t.Errorf("after unpicking, all 6 commits should return to %s, got %d", day, got)
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

	// Params pane: p opens the preview, D drops bodies, s opens the palette.
	m = send(m, "tab") // calendar -> params
	if m.focus != focusParams {
		t.Fatal("setup: params pane should be focused")
	}
	m = send(m, "p")
	if m.mode != modePreview {
		t.Fatal("p should open the preview from the params pane")
	}
	m = send(m, "esc")

	m = send(m, "D")
	if len(m.plan.Edits) != 3 {
		t.Errorf("D should drop bodies from the params pane, got %d edits", len(m.plan.Edits))
	}

	// Digits are NOT global: they belong to the focused form field.
	before := m.form.inputs[0].Value()
	m = send(m, "1")
	if got := m.form.inputs[0].Value(); got != before+"1" {
		t.Errorf("digit should type into the field: want %q, got %q", before+"1", got)
	}

	m = send(m, "s")
	if m.mode != modePalette {
		t.Fatal("s should open the palette from the params pane")
	}
}

// The editor's fields widen to 75 columns on roomy terminals and shrink back
// toward 50 on narrow ones, so long titles show without horizontal scrolling.
func TestEditor_fieldWidthFollowsTerminal(t *testing.T) {
	m := newTestModelN(t, 3) // sized 120x40 by the helper
	if got := m.editorFieldWidth(); got != 75 {
		t.Errorf("at 120 cols the editor fields should be 75 wide, got %d", got)
	}
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)
	if got := m.editorFieldWidth(); got != 63 {
		t.Errorf("at 80 cols the editor fields should shrink to 63, got %d", got)
	}
	updated, _ = m.Update(tea.WindowSizeMsg{Width: 50, Height: 30})
	m = updated.(Model)
	if got := m.editorFieldWidth(); got != 50 {
		t.Errorf("tiny terminals should floor at 50, got %d", got)
	}

	// The editor actually shows a long title in full at 120 cols.
	m2 := newTestModelN(t, 3)
	m2 = send(m2, "e")
	if !strings.Contains(m2.View(), "support rotation c002") {
		t.Error("a ~64-char title should be fully visible in the editor")
	}
}

// Manual Pin and Picked Days applied from the strategy pane must not demand a
// date range: the pinned/picked days define it.
func TestParamsPane_manualPinAndPickedDaysApply(t *testing.T) {
	m := newTestModelN(t, 4)
	m = send(m, "tab", "2", " ") // pin quota 2 and pick the cursor day

	// Palette order: 0 Even Spread, 1 Manual Pin, 2 Picked Days.
	m = send(m, "s", "down", "enter", "enter") // Manual Pin -> apply
	if m.form.errMsg != "" {
		t.Fatalf("Manual Pin from the pane should apply, got %q", m.form.errMsg)
	}
	if m.focus != focusList {
		t.Fatal("successful Manual Pin apply should return focus to the list")
	}

	m = send(m, "s", "down", "down", "enter", "enter") // Picked Days -> apply
	if m.form.errMsg != "" {
		t.Fatalf("Picked Days from the pane should apply, got %q", m.form.errMsg)
	}
	if !m.plan.HasChanges() || !m.plan.IsValid() {
		t.Error("Picked Days apply should produce a valid plan")
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

func TestModel_ListWidthGrowsWithTerminal(t *testing.T) {
	m := newTestModel(t) // sized to 120×40 by the helper
	narrow := m.listWidth()

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 220, Height: 40})
	wide := updated.(Model).listWidth()

	if wide <= narrow {
		t.Fatalf("list should widen on a 220-col terminal: narrow=%d wide=%d", narrow, wide)
	}
	if wide < 120 {
		t.Fatalf("wide list should be ≥120 cols (old cap was 66), got %d", wide)
	}
}

func TestDetail_paneRendersForCursorCommit(t *testing.T) {
	m := newTestModelN(t, 3)
	// Wide terminal -> the detail column is seated next to the list.
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 220, Height: 40})
	m = updated.(Model)
	if computeLayout(m.width, m.height).detailWidth == 0 {
		t.Fatal("220-col terminal should seat the detail column")
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
	m := newTestModelN(t, 3) // sized 120x40 -> detail column hidden
	if computeLayout(m.width, m.height).detailWidth != 0 {
		t.Fatal("120-col terminal should hide the detail column")
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

// TestDetail_refetchesAfterApply verifies that the success path of commitApply
// schedules a fresh detail-stat fetch after reload() wipes the stat caches.
// Without the fix the apply path returns nil, leaving the panel stuck at
// "stat loading…" until the user manually moves the cursor.
func TestDetail_refetchesAfterApply(t *testing.T) {
	m := newTestModelN(t, 3)
	// Stage edits by applying a strategy (palette -> params pane -> apply).
	m = send(m, "s", "enter", "enter")
	if !m.plan.HasChanges() {
		t.Fatal("applying a strategy should stage edits")
	}
	// Apply the rewrite: reload() wipes the stat caches, so the apply path must
	// schedule a fresh fetch for the cursor commit (else the detail panel stays blank).
	updated, cmd := m.Update(key("A"))
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
	half := (m.listHeight() - 1) / 2
	if m.listCursor != before+half {
		t.Fatalf("Ctrl-D should move a half page (%d), got %d", half, m.listCursor)
	}
}

// branchModel builds a Model whose repo is on `main` (3 commits) with an extra
// local `feature` branch forked at the first commit, sized to a usable terminal.
func branchModel(t *testing.T) Model {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
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
	m = send(m, "s", "enter", "enter") // stage a strategy edit
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
	if m.statusKind != statusErr {
		t.Errorf("a refused switch should report an error status, got kind %v", m.statusKind)
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

func TestListNav_dayGroupJumpsDoNotConsumeCalendarG(t *testing.T) {
	m := newTestModelN(t, 3) // all on one day -> day jump clamps to ends
	// On the list, } jumps to the next (older) day group; with one day it lands
	// on the oldest commit.
	m = send(m, "}")
	if m.listCursor != len(m.plan.Commits)-1 {
		t.Errorf("} with a single day should land on the oldest commit, got %d", m.listCursor)
	}
	// The calendar's global g (re-apply quotas) still fires when not on the list.
	m2 := newTestModelN(t, 3)
	m2 = send(m2, "tab") // focus calendar
	m2 = send(m2, "1")   // pin a quota so applyQuotas has work
	// Clear the status so the next action's effect is unambiguous: a positive
	// signal proves g reached the quota path rather than doing nothing.
	m2.statusMsg = ""
	m2.statusKind = statusNone
	m2 = send(m2, "g") // re-apply quotas: must not be hijacked as list nav
	if m2.statusKind != statusOK || !strings.Contains(m2.statusMsg, "Manual Pin") {
		t.Errorf("calendar g should re-apply quotas (Manual Pin), got kind=%v msg=%q", m2.statusKind, m2.statusMsg)
	}
}

func TestAutoBase_mergeBaseOffFirstParentLineClaimsNoBase(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
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
