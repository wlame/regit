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
	m := Model{purgeSt: newPurgeState(), mode: modePurge}
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
	st := newPurgeState()
	st.scanning = false
	st.index = idx
	st.query.SetValue("jun")
	st = st.refreshMatches()
	st.selected["junk.log"] = true
	st.errMsg = "boom"
	out := renderPurgeWizard(st, idx.AddedIn, newStyles())
	for _, want := range []string{"jun", "junk.log", "[x]", "boom"} {
		if !strings.Contains(out, want) {
			t.Errorf("wizard missing %q:\n%s", want, out)
		}
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
	for _, want := range []string{"junk.log", "✖", "3 later commit"} {
		if !strings.Contains(joined, want) {
			t.Errorf("preview missing %q:\n%s", want, joined)
		}
	}
}

func TestPurgeChange_guardBlocksDirtyTree(t *testing.T) {
	commits := []model.Commit{{Hash: "a"}, {Hash: "b"}}
	pc := purgeChange{commits: commits, affected: map[string]bool{"b": true}, pv: capability.PurgePreview{}}
	g := pc.guard(model.RepoState{IsDirty: true})
	if !g.Blocked {
		t.Error("a dirty tree should block the purge guard")
	}
}
