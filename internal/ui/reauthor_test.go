package ui

import (
	"strings"
	"testing"

	"github.com/wlame/regit/internal/model"
)

// reauthorCommits builds root-first commits with explicit author identity.
func reauthorCommits(hashes ...string) []model.Commit {
	out := make([]model.Commit, len(hashes))
	for i, h := range hashes {
		out[i] = model.Commit{
			Hash: h, Subject: "c " + h,
			AuthorName: "old", AuthorEmail: "old@x",
			CommitterName: "old", CommitterEmail: "old@x",
		}
		if i > 0 {
			out[i].ParentHashes = []string{hashes[i-1]}
		}
	}
	return out
}

func TestReauthorChange_planEditsTargetsOnly(t *testing.T) {
	commits := reauthorCommits("a", "b", "c")
	targets := map[string]bool{"b": true}
	rc := newReauthorChange(commits, targets, "wlame", "new@x", true)

	if _, ok := rc.plan.EditFor("a"); ok {
		t.Error("unselected commit 'a' must not be edited")
	}
	e, ok := rc.plan.EditFor("b")
	if !ok {
		t.Fatal("selected commit 'b' should have an edit")
	}
	if e.EffectiveAuthorName(commits[1]) != "wlame" || e.EffectiveAuthorEmail(commits[1]) != "new@x" {
		t.Error("author identity not applied to 'b'")
	}
	if e.NewCommitterName == nil {
		t.Error("committer should also change when committer=true")
	}
}

func TestReauthorChange_committerFalseLeavesCommitter(t *testing.T) {
	commits := reauthorCommits("a", "b")
	rc := newReauthorChange(commits, map[string]bool{"b": true}, "wlame", "new@x", false)
	e, ok := rc.plan.EditFor("b")
	if !ok {
		t.Fatal("commit b should have an edit")
	}
	if e.NewCommitterName != nil || e.NewCommitterEmail != nil {
		t.Error("committer must be untouched when committer=false")
	}
}

func TestReauthorChange_previewShowsOldToNew(t *testing.T) {
	commits := reauthorCommits("a", "b")
	rc := newReauthorChange(commits, map[string]bool{"b": true}, "wlame", "new@x", true)
	pm := rc.preview(newStyles())
	joined := strings.Join(pm.lines, "\n")
	if !strings.Contains(joined, "old@x") || !strings.Contains(joined, "new@x") {
		t.Errorf("preview should show old -> new author:\n%s", joined)
	}
	if rc.title() == "" {
		t.Error("title should be non-empty")
	}
}

func TestBeginReauthor_prefillsFromOldestTarget(t *testing.T) {
	commits := reauthorCommits("a", "b", "c") // root-first: a oldest
	commits[0].AuthorName, commits[0].AuthorEmail = "oldest", "oldest@x"
	m := modelWithSet(commits, "a", "c") // set spans oldest + newest
	updated, _, ok := m.beginReauthor()
	if !ok {
		t.Fatal("beginReauthor should be handled")
	}
	m = updated.(Model)
	if m.mode != modeReauthor {
		t.Fatalf("mode = %v, want modeReauthor", m.mode)
	}
	if m.reauthorSt.nameBuf != "oldest" || m.reauthorSt.emailBuf != "oldest@x" {
		t.Errorf("prefill = %q/%q, want oldest/oldest@x", m.reauthorSt.nameBuf, m.reauthorSt.emailBuf)
	}
	if !m.reauthorSt.committerToo {
		t.Error("committer checkbox should default to checked")
	}
}

func TestUpdateReauthor_editsFieldsAndTogglesCommitter(t *testing.T) {
	commits := reauthorCommits("a", "b")
	m := modelWithSet(commits, "b")
	updated, _, _ := m.beginReauthor()
	m = updated.(Model)
	m.reauthorSt.nameBuf = "" // clear prefill to type fresh
	m.reauthorSt.field = fldRName

	m = sendReauthor(m, "x", "y")
	if m.reauthorSt.nameBuf != "xy" {
		t.Errorf("name buffer = %q, want xy", m.reauthorSt.nameBuf)
	}
	// Tab to committer field, space toggles it off.
	m = sendReauthor(m, "tab", "tab") // name -> email -> committer
	if m.reauthorSt.field != fldRCommitter {
		t.Fatalf("field = %v, want fldRCommitter", m.reauthorSt.field)
	}
	m = sendReauthor(m, " ")
	if m.reauthorSt.committerToo {
		t.Error("space on the committer field should uncheck it")
	}
}

func TestRenderReauthorWizard_showsFieldsAndCheckbox(t *testing.T) {
	st := reauthorState{nameBuf: "wlame", emailBuf: "v@x", committerToo: true, field: fldRName,
		targets: reauthorCommits("a")}
	out := renderReauthorWizard(st, newStyles())
	for _, want := range []string{"CHANGE AUTHOR", "wlame", "v@x", "committer"} {
		if !strings.Contains(out, want) {
			t.Errorf("wizard missing %q:\n%s", want, out)
		}
	}
}

func TestStageReauthor_entersPipelineWithChange(t *testing.T) {
	commits := reauthorCommits("a", "b")
	m := modelWithSet(commits, "b")
	updated, _, _ := m.beginReauthor()
	m = updated.(Model)
	m.reauthorSt.nameBuf, m.reauthorSt.emailBuf = "wlame", "new@x"
	out, _ := m.stageReauthor()
	m = out.(Model)
	if m.mode != modePipeline {
		t.Fatalf("mode = %v, want modePipeline", m.mode)
	}
	if _, ok := m.pending.(reauthorChange); !ok {
		t.Errorf("pending should be a reauthorChange, got %T", m.pending)
	}
	if m.pendingCmd == "" {
		t.Error("pendingCmd (transparency footer) should be set")
	}
}

func TestUpdateReauthor_keepsMultibyteRunes(t *testing.T) {
	commits := reauthorCommits("a", "b")
	m := modelWithSet(commits, "b")
	updated, _, _ := m.beginReauthor()
	m = updated.(Model)
	m.reauthorSt.nameBuf = ""
	m.reauthorSt.field = fldRName
	m = sendReauthor(m, "é", "ü")
	if m.reauthorSt.nameBuf != "éü" {
		t.Errorf("multibyte runes should be retained, got %q", m.reauthorSt.nameBuf)
	}
}

func TestReauthorChange_previewShowsCommitterLine(t *testing.T) {
	commits := reauthorCommits("a", "b")
	// committer=true means both author and committer lines should appear.
	rc := newReauthorChange(commits, map[string]bool{"b": true}, "wlame", "new@x", true)
	pm := rc.preview(newStyles())
	joined := strings.Join(pm.lines, "\n")
	if !strings.Contains(joined, "committer") {
		t.Errorf("preview should show a committer line when committer=true and identity changed:\n%s", joined)
	}
	// committer=false: only the author line should appear, no committer line.
	rcNoC := newReauthorChange(commits, map[string]bool{"b": true}, "wlame", "new@x", false)
	pmNoC := rcNoC.preview(newStyles())
	joinedNoC := strings.Join(pmNoC.lines, "\n")
	if strings.Contains(joinedNoC, "committer") {
		t.Errorf("preview should not show a committer line when committer=false:\n%s", joinedNoC)
	}
}

// sendReauthor feeds keys straight to updateReauthor, so these tests do not
// depend on the top-level mode dispatch (added in Task 6).
func sendReauthor(m Model, keys ...string) Model {
	for _, k := range keys {
		updated, _ := m.updateReauthor(key(k))
		m = updated.(Model)
	}
	return m
}

func TestAuthorshipWorkspace_enabledWithChangeAuthorRow(t *testing.T) {
	feats := featuresFor(WSAuthorship)
	if len(feats) == 0 {
		t.Fatal("Authorship workspace should expose at least one feature")
	}
	found := false
	for _, f := range feats {
		if strings.Contains(strings.ToLower(f.Name), "author") {
			found = true
		}
	}
	if !found {
		t.Errorf("Authorship menu should list a change-author feature, got %+v", feats)
	}
	meta, ok := workspaceByID(WSAuthorship)
	if !ok || !meta.Enabled {
		t.Error("Authorship tab should be enabled")
	}
}

func TestModeReauthor_dispatchesToWizardUpdate(t *testing.T) {
	commits := reauthorCommits("a", "b")
	m := modelWithSet(commits, "b")
	updated, _, _ := m.beginReauthor()
	m = updated.(Model)
	// Drive through the top-level Update → handleKey → updateReauthor path.
	m = send(m, "esc")
	if m.mode != modeMain {
		t.Errorf("esc in the wizard should return to modeMain, got %v", m.mode)
	}
}

// TestStageReauthor_dirtyTreeBlocksPipeline: a dirty tree must be caught by the
// pre-flight guard so the wizard never enters the apply pipeline (H1).
func TestStageReauthor_dirtyTreeBlocksPipeline(t *testing.T) {
	m := modelWithSet(reauthorCommits("a", "b"), "b")
	updated, _, _ := m.beginReauthor()
	m = updated.(Model)
	m.reauthorSt.nameBuf, m.reauthorSt.emailBuf = "wlame", "new@x"
	m.state.IsDirty = true
	out, _ := m.stageReauthor()
	mm := out.(Model)
	if mm.mode == modePipeline {
		t.Error("a dirty tree must block reauthor, not enter the pipeline")
	}
}

// TestStageReauthor_pushedHistorySetsConfirmGuard: editing a pushed commit must
// set pendingGuard.NeedsConfirmation so the typed-confirm step runs (H1).
func TestStageReauthor_pushedHistorySetsConfirmGuard(t *testing.T) {
	commits := reauthorCommits("a", "b")
	commits[1].IsPushed = true // the target "b" already exists upstream
	m := modelWithSet(commits, "b")
	updated, _, _ := m.beginReauthor()
	m = updated.(Model)
	m.reauthorSt.nameBuf, m.reauthorSt.emailBuf = "wlame", "new@x"
	out, _ := m.stageReauthor()
	mm := out.(Model)
	if mm.mode != modePipeline {
		t.Fatalf("clean pushed history should enter the pipeline, got mode %v", mm.mode)
	}
	if !mm.pendingGuard.NeedsConfirmation {
		t.Error("editing a pushed commit must set pendingGuard.NeedsConfirmation")
	}
}
