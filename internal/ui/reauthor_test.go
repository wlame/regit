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
	if m.reauthorSt.name() != "oldest" || m.reauthorSt.email() != "oldest@x" {
		t.Errorf("prefill = %q/%q, want oldest/oldest@x", m.reauthorSt.name(), m.reauthorSt.email())
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
	m.reauthorSt.nameInput.SetValue("") // clear prefill to type fresh

	m = sendReauthor(m, "x", "y")
	if m.reauthorSt.name() != "xy" {
		t.Errorf("name buffer = %q, want xy", m.reauthorSt.name())
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
	st := newReauthorState("wlame", "v@x", reauthorCommits("a"), 64)
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
	m.reauthorSt.nameInput.SetValue("wlame")
	m.reauthorSt.emailInput.SetValue("new@x")
	out, _ := m.stageReauthor()
	m = out.(Model)
	if m.mode != modePipeline {
		t.Fatalf("mode = %v, want modePipeline", m.mode)
	}
	rc, ok := m.pending.(reauthorChange)
	if !ok {
		t.Fatalf("pending should be a reauthorChange, got %T", m.pending)
	}
	if len(rc.commandLines()) == 0 {
		t.Error("the staged reauthorChange should carry a non-empty commandLines() (transparency footer)")
	}
}

func TestUpdateReauthor_keepsMultibyteRunes(t *testing.T) {
	commits := reauthorCommits("a", "b")
	m := modelWithSet(commits, "b")
	updated, _, _ := m.beginReauthor()
	m = updated.(Model)
	m.reauthorSt.nameInput.SetValue("")
	m = sendReauthor(m, "é", "ü")
	if m.reauthorSt.name() != "éü" {
		t.Errorf("multibyte runes should be retained, got %q", m.reauthorSt.name())
	}
}

// TestUpdateReauthor_cursorMovementInsertsMidText: arrow keys move the cursor and
// typing inserts at the cursor, not at the end (regression: the old hand-rolled
// buffer could only append).
func TestUpdateReauthor_cursorMovementInsertsMidText(t *testing.T) {
	m := modelWithSet(reauthorCommits("a", "b"), "b")
	updated, _, _ := m.beginReauthor()
	m = updated.(Model)
	m.reauthorSt.nameInput.SetValue("bc") // cursor sits after "bc"
	m = sendReauthor(m, "left", "left", "a")
	if m.reauthorSt.name() != "abc" {
		t.Errorf("insert at cursor = %q, want abc", m.reauthorSt.name())
	}
}

// TestUpdateReauthor_pasteMultiRuneInput: a paste arrives as one KeyRunes message
// carrying the whole string; it must be inserted, not dropped (regression: the
// old handler ignored len(Runes) > 1).
func TestUpdateReauthor_pasteMultiRuneInput(t *testing.T) {
	m := modelWithSet(reauthorCommits("a", "b"), "b")
	updated, _, _ := m.beginReauthor()
	m = updated.(Model)
	m.reauthorSt.nameInput.SetValue("")
	m = sendReauthor(m, "Alice Smith")
	if m.reauthorSt.name() != "Alice Smith" {
		t.Errorf("pasted value = %q, want %q", m.reauthorSt.name(), "Alice Smith")
	}
}

// TestUpdateReauthor_cycleKeepsFocusOnActiveInput: tab moves focus between the
// inputs; typing after a cycle lands in the newly focused field only.
func TestUpdateReauthor_cycleKeepsFocusOnActiveInput(t *testing.T) {
	m := modelWithSet(reauthorCommits("a", "b"), "b")
	updated, _, _ := m.beginReauthor()
	m = updated.(Model)
	m.reauthorSt.nameInput.SetValue("")
	m.reauthorSt.emailInput.SetValue("")
	m = sendReauthor(m, "tab", "e") // name -> email, type there
	if m.reauthorSt.email() != "e" || m.reauthorSt.name() != "" {
		t.Errorf("after tab, typing must hit email only; name=%q email=%q",
			m.reauthorSt.name(), m.reauthorSt.email())
	}
	m = sendReauthor(m, "up", "n") // email -> name (cycle back), type there
	if m.reauthorSt.name() != "n" {
		t.Errorf("after cycling back, typing must hit name; name=%q", m.reauthorSt.name())
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

func TestChangeAuthorFeature_isBoundAndPanelVisible(t *testing.T) {
	b, ok := featureBindings["reauthor"]
	if !ok {
		t.Fatal("\"reauthor\" should have a featureBinding")
	}
	if !strings.Contains(strings.ToLower(b.Title), "author") {
		t.Errorf("reauthor binding title should mention author, got %q", b.Title)
	}
	m := newTestModel(t)
	found := false
	for _, r := range m.panelRows() {
		if r.Verb == "reauthor" {
			found = true
		}
	}
	if !found {
		t.Error("the action panel should list the change-author row")
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
	m.reauthorSt.nameInput.SetValue("wlame")
	m.reauthorSt.emailInput.SetValue("new@x")
	m.state.IsDirty = true
	out, _ := m.stageReauthor()
	mm := out.(Model)
	if mm.mode == modePipeline {
		t.Error("a dirty tree must block reauthor, not enter the pipeline")
	}
	if mm.reauthorSt.errMsg == "" {
		t.Error("a blocked stage must set the wizard's inline errMsg (the status bar is hidden under the modal)")
	}
}

// TestStageReauthor_noopIdentityShowsInlineError: applying without changing the
// prefilled identity stays in the wizard WITH a visible inline explanation.
func TestStageReauthor_noopIdentityShowsInlineError(t *testing.T) {
	m := modelWithSet(reauthorCommits("a", "b"), "b")
	updated, _, _ := m.beginReauthor()
	m = updated.(Model) // prefill matches the target's identity → no-op apply
	out, _ := m.stageReauthor()
	mm := out.(Model)
	if mm.mode != modeReauthor {
		t.Fatalf("a no-op apply should stay in the wizard, got mode %v", mm.mode)
	}
	if mm.reauthorSt.errMsg == "" {
		t.Error("a no-op apply must set the wizard's inline errMsg")
	}
	rendered := renderReauthorWizard(mm.reauthorSt, newStyles())
	if !strings.Contains(rendered, mm.reauthorSt.errMsg) {
		t.Errorf("the wizard should render its errMsg inline:\n%s", rendered)
	}
}

// TestStageReauthor_emptyIdentityShowsInlineError: clearing both fields and
// applying keeps the wizard open with an inline explanation, and typing again
// clears the error.
func TestStageReauthor_emptyIdentityShowsInlineError(t *testing.T) {
	m := modelWithSet(reauthorCommits("a", "b"), "b")
	updated, _, _ := m.beginReauthor()
	m = updated.(Model)
	m.reauthorSt.nameInput.SetValue("")
	m.reauthorSt.emailInput.SetValue("")
	out, _ := m.stageReauthor()
	m = out.(Model)
	if m.reauthorSt.errMsg == "" {
		t.Fatal("an empty identity must set the wizard's inline errMsg")
	}
	m = sendReauthor(m, "x")
	if m.reauthorSt.errMsg != "" {
		t.Error("typing should clear the previous stage error")
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
	m.reauthorSt.nameInput.SetValue("wlame")
	m.reauthorSt.emailInput.SetValue("new@x")
	out, _ := m.stageReauthor()
	mm := out.(Model)
	if mm.mode != modePipeline {
		t.Fatalf("clean pushed history should enter the pipeline, got mode %v", mm.mode)
	}
	if !mm.pendingGuard.NeedsConfirmation {
		t.Error("editing a pushed commit must set pendingGuard.NeedsConfirmation")
	}
}

// TestUpdateReauthor_typingCapitalA_isText: "A" typed into name/email is text
// (P0 — typing "Alice" must not stage the wizard).
func TestUpdateReauthor_typingCapitalA_isText(t *testing.T) {
	m := modelWithSet(reauthorCommits("a", "b"), "b")
	updated, _, _ := m.beginReauthor()
	m = updated.(Model)
	m.reauthorSt.nameInput.SetValue("")
	m = sendReauthor(m, "A", "l", "i", "c", "e")
	if m.mode != modeMain && m.mode != modeReauthor {
		t.Fatalf("typing must not leave the wizard; mode %v", m.mode)
	}
	if m.mode != modeReauthor {
		t.Fatalf("typing 'A' staged the wizard (mode %v)", m.mode)
	}
	if m.reauthorSt.name() != "Alice" {
		t.Errorf("name buffer = %q, want Alice", m.reauthorSt.name())
	}
}

// TestUpdateReauthor_enterStages_andAOnCheckboxStages: enter applies from any
// field; A applies only from the (non-text) committer checkbox.
func TestUpdateReauthor_enterStages_andAOnCheckboxStages(t *testing.T) {
	m := modelWithSet(reauthorCommits("a", "b"), "b")
	updated, _, _ := m.beginReauthor()
	m = updated.(Model)
	m.reauthorSt.nameInput.SetValue("wlame")
	m.reauthorSt.emailInput.SetValue("new@x")
	m = sendReauthor(m, "enter")
	if m.mode != modePipeline {
		t.Fatalf("enter should stage from a text field, got mode %v", m.mode)
	}

	// Fresh wizard: A on the checkbox field stages.
	m2 := modelWithSet(reauthorCommits("a", "b"), "b")
	updated2, _, _ := m2.beginReauthor()
	m2 = updated2.(Model)
	m2.reauthorSt.nameInput.SetValue("wlame")
	m2.reauthorSt.emailInput.SetValue("new@x")
	m2.reauthorSt.field = fldRCommitter
	m2 = sendReauthor(m2, "A")
	if m2.mode != modePipeline {
		t.Fatalf("A on the checkbox should stage, got mode %v", m2.mode)
	}
}

// TestRenderReauthorWizard_footerMatchesFocus: the footer advertises [A]pply
// only where A actually applies.
func TestRenderReauthorWizard_footerMatchesFocus(t *testing.T) {
	st := newReauthorState("", "", reauthorCommits("a"), 64)
	if out := renderReauthorWizard(st, newStyles()); strings.Contains(out, "[A]pply") {
		t.Errorf("text-field footer must not advertise [A]pply:\n%s", out)
	}
	st.field = fldRCommitter
	if out := renderReauthorWizard(st, newStyles()); !strings.Contains(out, "[A]pply") {
		t.Errorf("checkbox footer should advertise [A]pply:\n%s", out)
	}
}

func TestNewReauthorStateUsesGivenInputWidth(t *testing.T) {
	s := newReauthorState("wlame", "vvlame@gmail.com", nil, 64)
	if s.nameInput.Width != 64 || s.emailInput.Width != 64 {
		t.Fatalf("input widths = %d/%d, want 64", s.nameInput.Width, s.emailInput.Width)
	}
}
