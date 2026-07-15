package ui

import (
	"strings"
	"testing"

	"github.com/wlame/regit/internal/capability"
	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/model"
)

// purgeTestIndex builds a *gitio.PathIndex via the exported ParsePathScan
// parser, fed a hand-written git-log-name-status fixture: helpers.py added
// then renamed to util.py (a rename chain), plus junk.log and readme.md as
// plain adds — enough to exercise Match, RenameChain, and AddedIn without a
// real repository.
func purgeTestIndex() *gitio.PathIndex {
	scan := "\x00h1\n" +
		"A\thelpers.py\n" +
		"\x00h2\n" +
		"R100\thelpers.py\tutil.py\n" +
		"\x00h3\n" +
		"A\tjunk.log\n" +
		"\x00h4\n" +
		"A\treadme.md\n"
	return gitio.ParsePathScan(scan)
}

// sendPurge feeds keys straight to updatePurge, mirroring sendReauthor/
// squash's editField helper in this package's test style.
func sendPurge(m Model, keys ...string) Model {
	for _, k := range keys {
		updated, _ := m.updatePurge(key(k))
		m = updated.(Model)
	}
	return m
}

// modelWithPurgeIndex builds a Model in modePurge with the given index already
// delivered, as if the async scan had completed via purgeScanMsg.
func modelWithPurgeIndex(idx *gitio.PathIndex) Model {
	m := Model{purgeSt: newPurgeState(64), mode: modePurge}
	updated, _ := m.Update(purgeScanMsg{index: idx})
	return updated.(Model)
}

func TestBeginPurge_entersModeAndStartsScan(t *testing.T) {
	m := Model{}
	updated, cmd, ok := m.beginPurge()
	if !ok {
		t.Fatal("beginPurge should be handled")
	}
	mm := updated.(Model)
	if mm.mode != modePurge {
		t.Errorf("mode = %v, want modePurge", mm.mode)
	}
	if !mm.purgeSt.scanning {
		t.Error("purgeSt.scanning should be true right after beginPurge")
	}
	if cmd == nil {
		t.Error("beginPurge should return a non-nil tea.Cmd to kick off the scan")
	}
}

func TestUpdatePurge_typingFiltersMatches(t *testing.T) {
	m := modelWithPurgeIndex(purgeTestIndex())
	m = sendPurge(m, "j", "u", "n")
	if len(m.purgeSt.matches) != 1 || m.purgeSt.matches[0] != "junk.log" {
		t.Errorf("matches = %v, want [junk.log]", m.purgeSt.matches)
	}
}

func TestUpdatePurge_spaceTogglesAndChecksRenameChain(t *testing.T) {
	m := modelWithPurgeIndex(purgeTestIndex())
	m = sendPurge(m, "h", "e", "l", "p")
	if len(m.purgeSt.matches) != 2 {
		t.Fatalf("matches = %v, want 2 (helpers.py + util.py chain expansion)", m.purgeSt.matches)
	}
	m = sendPurge(m, " ") // toggles the cursor row (helpers.py) plus its rename chain
	selected := 0
	for _, on := range m.purgeSt.selected {
		if on {
			selected++
		}
	}
	if selected != 2 {
		t.Errorf("selected count = %d, want 2 (chain toggled together)", selected)
	}
	if !m.purgeSt.selected["helpers.py"] || !m.purgeSt.selected["util.py"] {
		t.Errorf("both chain members should be selected: %+v", m.purgeSt.selected)
	}
}

func TestUpdatePurge_stageWithNothingSelectedShowsInlineError(t *testing.T) {
	m := modelWithPurgeIndex(purgeTestIndex())
	out, _ := m.updatePurge(key("enter"))
	mm := out.(Model)
	if mm.mode != modePurge {
		t.Errorf("mode = %v, want modePurge (a blocked stage must not leave the wizard)", mm.mode)
	}
	if mm.purgeSt.errMsg == "" {
		t.Error("staging with nothing selected should set purgeSt.errMsg (the status bar is hidden under the modal)")
	}
}

func TestUpdatePurge_escBacksOut(t *testing.T) {
	m := modelWithPurgeIndex(purgeTestIndex())
	out, _ := m.updatePurge(key("esc"))
	mm := out.(Model)
	if mm.mode != modeMain {
		t.Errorf("mode = %v, want modeMain", mm.mode)
	}
	if mm.statusMsg == "" {
		t.Error("esc should set a status message")
	}
}

func TestRenderPurgeWizard_showsMatchesAndSelection(t *testing.T) {
	idx := purgeTestIndex()
	st := newPurgeState(64)
	st.scanning = false
	st.index = idx
	st.query.SetValue("jun")
	st = st.refreshMatches()
	st.selected["junk.log"] = true
	st.errMsg = "boom"
	out := renderPurgeWizard(st, purgeDirSet(idx), newStyles())
	for _, want := range []string{"jun", "junk.log", "[x]", "boom"} {
		if !strings.Contains(out, want) {
			t.Errorf("wizard missing %q:\n%s", want, out)
		}
	}
}

// TestRenderPurgeWizard_renameDestinationHasNoTrailingSlash guards against
// classifying file-vs-directory by AddedIn membership: util.py is a rename
// destination (an R record in purgeTestIndex, never a plain "A" add) and so
// is absent from AddedIn, but it is still a file — it must render without
// the directory-only trailing "/".
func TestRenderPurgeWizard_renameDestinationHasNoTrailingSlash(t *testing.T) {
	idx := purgeTestIndex()
	st := newPurgeState(64)
	st.scanning = false
	st.index = idx
	st.query.SetValue("help")
	st = st.refreshMatches()
	out := renderPurgeWizard(st, purgeDirSet(idx), newStyles())
	if !strings.Contains(out, "util.py") {
		t.Fatalf("expected util.py among the matches:\n%s", out)
	}
	if strings.Contains(out, "util.py/") {
		t.Errorf("util.py is a rename destination, not a directory — it must not render with a trailing slash:\n%s", out)
	}
}

func TestPurgeChange_previewMarksDropsAndPaths(t *testing.T) {
	commits := []model.Commit{
		{Hash: "aaa1111", Subject: "add junk"},
		{Hash: "bbb2222", Subject: "edit other"},
	}
	pv := capability.PurgePreview{
		Paths:      []string{"junk.log"},
		Affected:   commits,
		Dropped:    map[string]bool{"aaa1111": true},
		Reparented: 3,
	}
	pc := purgeChange{spec: model.PurgeSpec{Paths: []string{"junk.log"}}, pv: pv, commits: commits}
	pm := pc.preview(newStyles())
	joined := strings.Join(pm.lines, "\n")
	// "carried forward" must match the CLI wording in run_purge.go and the
	// amendment exactly, so the two surfaces cannot silently drift apart.
	for _, want := range []string{"junk.log", "✖", "3 later commit", "carried forward"} {
		if !strings.Contains(joined, want) {
			t.Errorf("preview missing %q:\n%s", want, joined)
		}
	}
}

// TestRenderPurgeWizard_sanitizesEscapeSequenceInMatchRow is the ui-layer
// IMPORTANT finding 2 regression test (wizard match rows, site 4): a
// history-derived match containing a raw OSC escape sequence must not reach
// the rendered wizard body verbatim. termsafe strips the whole OSC payload
// (introducer through terminator — see termsafe_test.go), so the injected
// text itself must be gone while the surrounding legitimate text survives.
func TestRenderPurgeWizard_sanitizesEscapeSequenceInMatchRow(t *testing.T) {
	scan := "\x00h1\n" + "A\tevil\x1b]0;PWNED\x07.txt\n"
	idx := gitio.ParsePathScan(scan)
	st := newPurgeState(64)
	st.scanning = false
	st.index = idx
	st.query.SetValue("evil")
	st = st.refreshMatches()
	out := renderPurgeWizard(st, purgeDirSet(idx), newStyles())
	if strings.ContainsAny(out, "\x1b\x07") || strings.Contains(out, "PWNED") {
		t.Fatalf("wizard output leaked the raw OSC payload: %q", out)
	}
	if !strings.Contains(out, "evil.txt") {
		t.Errorf("sanitization should keep the legitimate text around the escape sequence, got: %q", out)
	}
}

func TestNewPurgeStateUsesGivenInputWidth(t *testing.T) {
	s := newPurgeState(64)
	if s.query.Width != 64 {
		t.Fatalf("query width = %d, want 64", s.query.Width)
	}
}

// TestPurgeChange_previewSanitizesCommitSubject is the ui-layer companion for
// site 5 (purgeChange.preview's subject rows): a repo-derived commit subject
// carrying a raw escape sequence must not reach the staged-change preview
// verbatim.
func TestPurgeChange_previewSanitizesCommitSubject(t *testing.T) {
	commits := []model.Commit{{Hash: "aaaa1111", Subject: "evil\x1b]0;PWNED\x07 subject"}}
	pv := capability.PurgePreview{
		Paths:    []string{"junk.log"},
		Affected: commits,
		Dropped:  map[string]bool{"aaaa1111": true},
	}
	pc := purgeChange{spec: model.PurgeSpec{Paths: []string{"junk.log"}}, pv: pv, commits: commits}
	pm := pc.preview(newStyles())
	joined := strings.Join(pm.lines, "\n")
	if strings.ContainsAny(joined, "\x1b\x07") || strings.Contains(joined, "PWNED") {
		t.Fatalf("preview leaked the raw OSC payload: %q", joined)
	}
	if !strings.Contains(joined, "evil subject") {
		t.Errorf("sanitization should keep the legitimate text around the escape sequence, got: %q", joined)
	}
}

func TestPurgeChange_guardBlocksDirtyTree(t *testing.T) {
	commits := []model.Commit{{Hash: "a"}, {Hash: "b"}}
	// base mirrors what gitio.PurgeBase would compute for this fixture (no
	// merge involved, so the naive and corrected boundaries coincide): "b" is
	// the first (and only) affected commit, at index 1.
	pc := purgeChange{commits: commits, affected: map[string]bool{"b": true}, pv: capability.PurgePreview{}, base: 1}
	g := pc.guard(model.RepoState{IsDirty: true})
	if !g.Blocked {
		t.Error("a dirty tree should block the purge guard")
	}
}

// TestPurgeChange_guardBlocksMergeAtStoredBase is the regression test for the
// wizard's pre-stage guard now trusting stagePurge's stored, corrected base
// instead of recomputing a naive one: base here points directly at a merge
// commit, mirroring what gitio.PurgeBase computes when a merge-in-prefix
// history pulls a merge into the rewritten suffix (see the gitio/safety/
// capability regression tests for the real-repo fixture this mirrors — a
// side branch adds a path, merges in untouched further, and git's own
// pathspec-limited rev-list simplifies the merge itself out of the naive
// affected set even though its tree still carries the path).
func TestPurgeChange_guardBlocksMergeAtStoredBase(t *testing.T) {
	commits := []model.Commit{{Hash: "a"}, {Hash: "merge", IsMerge: true}, {Hash: "c"}}
	pc := purgeChange{commits: commits, affected: map[string]bool{"c": true}, pv: capability.PurgePreview{}, base: 1}
	g := pc.guard(model.RepoState{})
	if !g.Blocked {
		t.Error("a purge whose stored base includes a merge commit should block")
	}
	if !strings.Contains(g.BlockReason, "merge") {
		t.Errorf("block reason should mention merge, got: %q", g.BlockReason)
	}
}

// TestPurgeChange_guardBlocksStoredSideHistory is the wizard-layer regression
// test for finding 1 of the second final-review round: a purgeChange whose
// stored sidePaths (computed at stage time by stagePurge, mirroring base)
// is non-empty must block, even when no commit in the range is itself a
// merge — mirrors the "pocket" fixture in gitio/safety/capability, where the
// merge's own tree never carries the path forward at all, so only a
// side-branch-history check (not the merge-in-suffix one) can catch it.
func TestPurgeChange_guardBlocksStoredSideHistory(t *testing.T) {
	commits := []model.Commit{{Hash: "a"}, {Hash: "b"}}
	pc := purgeChange{
		commits: commits, affected: map[string]bool{"b": true}, pv: capability.PurgePreview{},
		base: 1, sidePaths: []string{"secret.env"},
	}
	g := pc.guard(model.RepoState{})
	if !g.Blocked {
		t.Error("a purge whose stored sidePaths is non-empty should block")
	}
	if !strings.Contains(g.BlockReason, "side branch") {
		t.Errorf("block reason should mention the side branch, got: %q", g.BlockReason)
	}
}

// TestUpdatePurge_staleScanMsgDiscarded exercises the beginPurge/purgeScanMsg
// generation guard: reopening the wizard (a second beginPurge) bumps scanSeq,
// so a result from the first, now-abandoned scan must not clobber the second
// scan's already-applied index — mirrors the splitGenSeq guard on splitResultMsg.
func TestUpdatePurge_staleScanMsgDiscarded(t *testing.T) {
	m := Model{}
	updated, _, _ := m.beginPurge()
	m = updated.(Model)
	staleSeq := m.purgeSt.scanSeq

	updated, _, _ = m.beginPurge() // reopen: a second, unrelated scan generation
	m = updated.(Model)
	if m.purgeSt.scanSeq == staleSeq {
		t.Fatalf("beginPurge should bump scanSeq on each invocation, got %d both times", staleSeq)
	}

	currentIdx := purgeTestIndex()
	out, _ := m.Update(purgeScanMsg{index: currentIdx, seq: m.purgeSt.scanSeq})
	m = out.(Model)
	if m.purgeSt.index != currentIdx {
		t.Fatal("the current generation's scan result should be applied")
	}

	staleIdx := &gitio.PathIndex{}
	out, _ = m.Update(purgeScanMsg{index: staleIdx, seq: staleSeq})
	m = out.(Model)
	if m.purgeSt.index != currentIdx {
		t.Error("a stale-seq purgeScanMsg must not overwrite the current index")
	}
	if m.purgeSt.index == staleIdx {
		t.Error("the stale scan's index must never be applied")
	}
}
