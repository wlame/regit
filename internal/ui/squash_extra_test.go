package ui

// squash_extra_test.go covers the functions left uncovered by squash_test.go:
// selectedHashSet (SelRange branch), guard (all cases), chainsToSpec,
// beginSquash (no-chains and chains), updateSquash (all key paths),
// stageSquash (happy path), and viewSquash.

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/wlame/regit/internal/model"
)

// squashModelReady builds a Model that is already in modeSquash with the
// given consecutive hashes selected and the wizard fully initialized.
func squashModelReady(commits []model.Commit, selected ...string) Model {
	m := modelWithSet(commits, selected...)
	out, _, _ := m.beginSquash()
	return out.(Model)
}

// TestSelectedHashSet_rangeMode verifies that selectedHashSet covers the
// SelRange case: all display indices from anchor to cursor map to commit hashes.
func TestSelectedHashSet_rangeMode(t *testing.T) {
	// Root-first: a(oldest) b c d(newest). Display-index 0=d,1=c,2=b,3=a.
	commits := linearModelCommits("a", "b", "c", "d")
	m := Model{plan: model.NewPlan(commits)}
	m.sel = newSelection()
	m.sel.mode = SelRange
	m.sel.anchor = 0 // anchor at newest (d)
	m.listCursor = 2 // extend to b

	got := m.selectedHashSet()
	// rangeBounds(2) with anchor 0 → [0,2] → d, c, b
	for _, h := range []string{"d", "c", "b"} {
		if !got[h] {
			t.Errorf("hash %q missing from range selection (got %v)", h, got)
		}
	}
	if len(got) != 3 {
		t.Errorf("want 3 hashes in range, got %d", len(got))
	}
}

// TestSquashChange_guard_mergeCommitBlocks verifies that a member that is a
// merge commit triggers a hard block before any rewrite attempt.
func TestSquashChange_guard_mergeCommitBlocks(t *testing.T) {
	ch := model.SquashChain{Members: []model.Commit{
		{Hash: "a", IsMerge: true},
		{Hash: "b"},
	}}
	sc := squashChange{plan: model.SquashPlan{Chains: []model.SquashChain{ch}}}
	g := sc.guard(model.RepoState{})
	if !g.Blocked || g.BlockReason == "" {
		t.Errorf("guard should block on a merge-commit member, got %+v", g)
	}
}

// TestSquashChange_guard_dirtyTreeBlocks verifies that an uncommitted working-
// tree change blocks the squash.
func TestSquashChange_guard_dirtyTreeBlocks(t *testing.T) {
	ch := model.SquashChain{Members: []model.Commit{{Hash: "a"}, {Hash: "b"}}}
	sc := squashChange{plan: model.SquashPlan{Chains: []model.SquashChain{ch}}}
	g := sc.guard(model.RepoState{IsDirty: true})
	if !g.Blocked || g.BlockReason == "" {
		t.Errorf("guard should block on dirty working tree, got %+v", g)
	}
}

// TestSquashChange_guard_pushedCommitsNeedConfirm verifies that squashing
// history already on an upstream requires a typed confirmation.
func TestSquashChange_guard_pushedCommitsNeedConfirm(t *testing.T) {
	ch := model.SquashChain{Members: []model.Commit{{Hash: "a"}, {Hash: "b"}}}
	sc := squashChange{plan: model.SquashPlan{Chains: []model.SquashChain{ch}}}
	state := model.RepoState{
		Branch:        "main",
		HasUpstream:   true,
		UpstreamRef:   "origin/main",
		AheadOfRemote: 0,
	}
	g := sc.guard(state)
	if g.Blocked {
		t.Errorf("guard should not block (only needs confirmation), got %+v", g)
	}
	if !g.NeedsConfirmation {
		t.Error("guard should require confirmation when commits are on upstream")
	}
	if g.ConfirmWord != "main" {
		t.Errorf("ConfirmWord = %q, want %q", g.ConfirmWord, "main")
	}
}

// TestSquashChange_guard_cleanLocalOK verifies the happy path: no merge
// commits, clean working tree, no upstream → guard is clear.
func TestSquashChange_guard_cleanLocalOK(t *testing.T) {
	ch := model.SquashChain{Members: []model.Commit{{Hash: "a"}, {Hash: "b"}}}
	sc := squashChange{plan: model.SquashPlan{Chains: []model.SquashChain{ch}}}
	g := sc.guard(model.RepoState{})
	if g.Blocked || g.NeedsConfirmation {
		t.Errorf("guard should be clear for clean local repo, got %+v", g)
	}
}

// TestChainsToSpec verifies the "first^..last" format used in the headless-
// equivalent command logged in the transparency footer.
func TestChainsToSpec(t *testing.T) {
	ch := model.SquashChain{Members: []model.Commit{
		{Hash: "aaaaaaaabbbbbbb1"},
		{Hash: "ccccccccdddddddd"},
	}}
	got := chainsToSpec([]model.SquashChain{ch})
	// short() takes 8 chars.
	want := "aaaaaaaa^..cccccccc"
	if got != want {
		t.Errorf("chainsToSpec = %q, want %q", got, want)
	}
}

// TestChainsToSpec_multipleChains verifies that multiple chains are joined
// with commas so the spec round-trips through parseSquashSpec.
func TestChainsToSpec_multipleChains(t *testing.T) {
	ch1 := model.SquashChain{Members: []model.Commit{{Hash: "aaaaaaa1"}, {Hash: "bbbbbbb1"}}}
	ch2 := model.SquashChain{Members: []model.Commit{{Hash: "ccccccc1"}, {Hash: "ddddddd1"}}}
	got := chainsToSpec([]model.SquashChain{ch1, ch2})
	if !strings.Contains(got, ",") {
		t.Error("multiple chains must be comma-separated")
	}
	if !strings.HasPrefix(got, "aaaaaaa1^..bbbbbbb1") {
		t.Errorf("first chain prefix wrong: %q", got)
	}
}

// TestBeginSquash_noSelectionWarns verifies that beginSquash shows a warning
// and stays in modeMain when no multi-commit chains can be derived.
func TestBeginSquash_noSelectionWarns(t *testing.T) {
	commits := linearModelCommits("a", "b", "c")
	m := modelWithSet(commits) // nothing selected
	out, _, handled := m.beginSquash()
	if !handled {
		t.Fatal("beginSquash should always return handled=true")
	}
	mm := out.(Model)
	if mm.mode != modeMain {
		t.Errorf("mode = %v, want modeMain when no chains detected", mm.mode)
	}
	if mm.statusMsg == "" {
		t.Error("beginSquash should set a non-empty warning status when no chains are found")
	}
}

// TestBeginSquash_singletonOnlyWarns verifies that a single selected commit
// (which forms no chain) does not open the wizard.
func TestBeginSquash_singletonOnlyWarns(t *testing.T) {
	commits := linearModelCommits("a", "b", "c")
	m := modelWithSet(commits, "b") // singleton; cannot form a chain
	out, _, _ := m.beginSquash()
	mm := out.(Model)
	if mm.mode != modeMain {
		t.Errorf("mode = %v, want modeMain for singleton selection", mm.mode)
	}
}

// TestBeginSquash_withChainEntersWizard verifies the happy path: 2+
// consecutive selected commits open the wizard with a populated squashSt.
func TestBeginSquash_withChainEntersWizard(t *testing.T) {
	commits := linearModelCommits("a", "b", "c")
	m := modelWithSet(commits, "a", "b") // chain {a,b}
	out, _, _ := m.beginSquash()
	mm := out.(Model)
	if mm.mode != modeSquash {
		t.Fatalf("mode = %v, want modeSquash", mm.mode)
	}
	if len(mm.squashSt.chains) != 1 {
		t.Fatalf("expected 1 chain, got %d", len(mm.squashSt.chains))
	}
	if len(mm.squashSt.chains[0].Members) != 2 {
		t.Errorf("chain should have 2 members, got %d", len(mm.squashSt.chains[0].Members))
	}
	if mm.squashSt.chains[0].Subject == "" {
		t.Error("beginSquash should prefill chain subject via DefaultSquashConfig")
	}
	if len(mm.squashSt.dateBuf) != 1 {
		t.Errorf("dateBuf length = %d, want 1", len(mm.squashSt.dateBuf))
	}
}

// TestUpdateSquash_escReturnToMain verifies that pressing ESC dismisses the
// wizard and sets a cancel status.
func TestUpdateSquash_escReturnToMain(t *testing.T) {
	commits := linearModelCommits("a", "b", "c")
	m := squashModelReady(commits, "a", "b")
	out, _ := m.updateSquash(key("esc"))
	mm := out.(Model)
	if mm.mode != modeMain {
		t.Errorf("mode = %v after esc, want modeMain", mm.mode)
	}
	if mm.statusMsg == "" {
		t.Error("esc should set a status message")
	}
}

// TestUpdateSquash_tabCyclesField verifies that Tab cycles through all
// squashFieldCount fields and wraps back.
func TestUpdateSquash_tabCyclesField(t *testing.T) {
	commits := linearModelCommits("a", "b", "c")
	m := squashModelReady(commits, "a", "b")
	// beginSquash starts at fldTitle. Tab squashFieldCount times → back to fldTitle.
	for i := 0; i < squashFieldCount; i++ {
		out, _ := m.updateSquash(key("tab"))
		m = out.(Model)
	}
	if m.squashSt.field != fldTitle {
		t.Errorf("after %d tabs from fldTitle, field = %v, want fldTitle", squashFieldCount, m.squashSt.field)
	}
}

// TestUpdateSquash_listCursorMovement verifies ↑/↓ move the commit cursor
// (always — there is no separate list field to focus).
func TestUpdateSquash_listCursorMovement(t *testing.T) {
	commits := linearModelCommits("a", "b", "c")
	m := squashModelReady(commits, "a", "b", "c")

	out, _ := m.updateSquash(key("down"))
	m = out.(Model)
	if m.squashSt.listCursor != 1 {
		t.Errorf("listCursor = %d after down, want 1", m.squashSt.listCursor)
	}
	out, _ = m.updateSquash(key("up"))
	m = out.(Model)
	if m.squashSt.listCursor != 0 {
		t.Errorf("listCursor = %d after up, want 0", m.squashSt.listCursor)
	}
	out, _ = m.updateSquash(key("up"))
	m = out.(Model)
	if m.squashSt.listCursor != 0 {
		t.Errorf("listCursor should stay 0 at top, got %d", m.squashSt.listCursor)
	}
}

// TestUpdateSquash_downAtBottomNoop verifies that pressing ↓ at the last
// member does not advance listCursor past the end.
func TestUpdateSquash_downAtBottomNoop(t *testing.T) {
	commits := linearModelCommits("a", "b", "c")
	m := squashModelReady(commits, "a", "b") // 2-member chain
	m.squashSt.listCursor = 1                // already at the last member

	out, _ := m.updateSquash(key("down"))
	mm := out.(Model)
	if mm.squashSt.listCursor != 1 {
		t.Errorf("listCursor should stay at 1, got %d", mm.squashSt.listCursor)
	}
}

// TestUpdateSquash_arrowSwitchesTabs verifies that ← and → cycle through
// chain tabs when more than one chain is selected.
func TestUpdateSquash_arrowSwitchesTabs(t *testing.T) {
	commits := linearModelCommits("a", "b", "c", "d", "e")
	m := squashModelReady(commits, "a", "b", "d", "e") // two chains: {a,b} and {d,e}
	if len(m.squashSt.chains) != 2 {
		t.Fatalf("expected 2 chains, got %d", len(m.squashSt.chains))
	}

	out, _ := m.updateSquash(key("right"))
	mm := out.(Model)
	if mm.squashSt.tab != 1 {
		t.Errorf("tab = %d after right, want 1", mm.squashSt.tab)
	}

	out, _ = mm.updateSquash(key("left"))
	mm = out.(Model)
	if mm.squashSt.tab != 0 {
		t.Errorf("tab = %d after left, want 0", mm.squashSt.tab)
	}
}

// TestUpdateSquash_typeCharAppendsToTitle verifies that a regular rune key
// appends the character to the active text field (default: fldTitle).
func TestUpdateSquash_typeCharAppendsToTitle(t *testing.T) {
	commits := linearModelCommits("a", "b", "c")
	m := squashModelReady(commits, "a", "b")
	before := m.squashSt.chains[0].Subject

	out, _ := m.updateSquash(key("X"))
	mm := out.(Model)
	if mm.squashSt.chains[0].Subject != before+"X" {
		t.Errorf("subject = %q, want %q", mm.squashSt.chains[0].Subject, before+"X")
	}
}

// TestUpdateSquash_backspaceDeletesFromTitle verifies that Backspace removes
// the last character of the active text field.
func TestUpdateSquash_backspaceDeletesFromTitle(t *testing.T) {
	commits := linearModelCommits("a", "b", "c")
	m := squashModelReady(commits, "a", "b")
	before := m.squashSt.chains[0].Subject

	out, _ := m.updateSquash(tea.KeyMsg{Type: tea.KeyBackspace})
	mm := out.(Model)
	wantLen := len([]rune(before)) - 1
	gotLen := len([]rune(mm.squashSt.chains[0].Subject))
	if gotLen != wantLen {
		t.Errorf("subject len = %d after backspace, want %d (was %q, got %q)",
			gotLen, wantLen, before, mm.squashSt.chains[0].Subject)
	}
}

// TestUpdateSquash_enterStages verifies that pressing enter calls stageSquash
// and, when the plan is valid, advances to modePipeline.
func TestUpdateSquash_enterStages(t *testing.T) {
	m := squashModelReady(linearModelCommits("a", "b", "c"), "a", "b", "c")
	out, _ := m.updateSquash(key("enter"))
	mm := out.(Model)
	if mm.mode != modePipeline {
		t.Errorf("enter should stage the squash (modePipeline), got mode %v", mm.mode)
	}
}

// TestUpdateSquash_stageEnteredOnA verifies that pressing "A" calls
// stageSquash and, when the plan is valid, advances to modePipeline.
func TestUpdateSquash_stageEnteredOnA(t *testing.T) {
	commits := linearModelCommits("a", "b", "c")
	m := squashModelReady(commits, "a", "b")

	out, _ := m.updateSquash(key("A"))
	mm := out.(Model)
	if mm.mode != modePipeline {
		t.Errorf("mode = %v after 'A', want modePipeline", mm.mode)
	}
}

// TestStageSquash_validPlanEntersPipeline verifies the happy path: a valid
// chain with a parseable date advances the model to the pipeline stage and
// populates the pending change and its CLI-equivalent command string.
func TestStageSquash_validPlanEntersPipeline(t *testing.T) {
	commits := linearModelCommits("a", "b", "c")
	m := squashModelReady(commits, "a", "b")

	out, _ := m.stageSquash()
	mm := out.(Model)
	if mm.mode != modePipeline {
		t.Errorf("mode = %v, want modePipeline", mm.mode)
	}
	if mm.pending == nil {
		t.Error("pending change should be set after a successful stageSquash")
	}
	if mm.pendingCmd == "" {
		t.Error("pendingCmd should be set after a successful stageSquash")
	}
	if !mm.pipelineApply {
		t.Error("pipelineApply should be true after stageSquash")
	}
}

// TestStageSquash_invalidDateSetsError verifies that a malformed date string
// sets an error status and keeps the model out of the pipeline.
func TestStageSquash_invalidDateSetsError(t *testing.T) {
	commits := linearModelCommits("a", "b")
	m := modelWithSet(commits, "a", "b")
	chains, _ := m.affectedChains()
	chains[0] = model.DefaultSquashConfig(chains[0])
	m.squashSt = squashState{chains: chains, dateBuf: []string{"2026-13-40 99:99"}, loading: map[string]bool{}}
	out, _ := m.stageSquash()
	mm := out.(Model)
	if mm.mode == modePipeline {
		t.Error("stageSquash must not advance to pipeline when the date is invalid")
	}
	if mm.statusKind != statusErr {
		t.Errorf("statusKind = %v, want statusErr", mm.statusKind)
	}
}

// TestViewSquash_rendersNonEmpty verifies that viewSquash returns a non-empty
// string that includes the wizard header.
func TestViewSquash_rendersNonEmpty(t *testing.T) {
	commits := linearModelCommits("a", "b", "c")
	m := squashModelReady(commits, "a", "b")
	m.st = newStyles()
	got := m.viewSquash()
	if got == "" {
		t.Error("viewSquash should return a non-empty string")
	}
	if !strings.Contains(got, "SQUASH") {
		t.Error("viewSquash output should contain the wizard header 'SQUASH'")
	}
}

// TestEditActiveField_bodyAndAuthorFields verifies that fldBody and fldAuthor
// accept typed characters and backspace, covering the two switch branches
// that title-only tests leave uncovered.
func TestEditActiveField_bodyAndAuthorFields(t *testing.T) {
	commits := linearModelCommits("a", "b")
	chains, _ := model.DetectChains(commits, map[string]bool{"a": true, "b": true})
	chains[0] = model.DefaultSquashConfig(chains[0])
	chains[0].Body = "existing body"
	chains[0].AuthorName = "Ann"
	dateStr := chains[0].AuthorDate.Format(squashDateLayout)
	st := squashState{
		chains:  chains,
		tab:     0,
		field:   fldBody,
		loading: map[string]bool{},
		dateBuf: []string{dateStr},
	}

	st2 := editActiveField(st, "!", false)
	if st2.chains[0].Body != "existing body!" {
		t.Errorf("fldBody append: body = %q, want %q", st2.chains[0].Body, "existing body!")
	}
	st3 := editActiveField(st2, "", true)
	if st3.chains[0].Body != "existing body" {
		t.Errorf("fldBody backspace: body = %q, want %q", st3.chains[0].Body, "existing body")
	}

	st.field = fldAuthor
	st4 := editActiveField(st, "X", false)
	if st4.chains[0].AuthorName != "AnnX" {
		t.Errorf("fldAuthor append: AuthorName = %q, want %q", st4.chains[0].AuthorName, "AnnX")
	}
	st5 := editActiveField(st4, "", true)
	if st5.chains[0].AuthorName != "Ann" {
		t.Errorf("fldAuthor backspace: AuthorName = %q, want %q", st5.chains[0].AuthorName, "Ann")
	}
}

// TestSquashState_switchTab_emptyChains verifies that switchTab is a safe
// no-op when the chain list is empty.
func TestSquashState_switchTab_emptyChains(t *testing.T) {
	st := squashState{chains: nil, loading: map[string]bool{}}
	got := st.switchTab(1)
	if got.tab != 0 {
		t.Errorf("switchTab on empty chains should leave tab=0, got %d", got.tab)
	}
}

// TestStageSquash_validateFailureSetsError verifies that stageSquash reports
// an error status and stays out of the pipeline when the plan fails validation.
func TestStageSquash_validateFailureSetsError(t *testing.T) {
	commits := linearModelCommits("a", "b", "c")
	m := modelWithSet(commits, "a", "b")
	chains, _ := m.affectedChains()
	chains[0] = model.DefaultSquashConfig(chains[0])
	// Replace the first member's hash with one that does not exist in the plan's
	// commit list, causing Validate to reject the chain.
	chains[0].Members[0].Hash = "nonexistent"
	m.squashSt = squashState{
		chains:  chains,
		dateBuf: []string{chains[0].AuthorDate.Format(squashDateLayout)},
		loading: map[string]bool{},
	}
	out, _ := m.stageSquash()
	mm := out.(Model)
	if mm.mode == modePipeline {
		t.Error("stageSquash should not advance to pipeline when plan validation fails")
	}
	if mm.statusKind != statusErr {
		t.Errorf("statusKind = %v, want statusErr after validation failure", mm.statusKind)
	}
}

// TestBuildSquashEngine_withProviderAndRepo verifies that buildSquashEngine
// constructs a non-nil Engine when an LLM provider is configured and a real
// git repo is available. It exercises the provider/base-URL/key resolution
// path (lines that are skipped in the no-LLM path tested elsewhere).
func TestBuildSquashEngine_withProviderAndRepo(t *testing.T) {
	m := newTestModelN(t, 2)      // provides a real git repository
	m.cfg.LLM.Provider = "ollama" // any non-empty provider triggers the construction path
	m.cfg.LLM.Model = "test-model"
	eng, err := buildSquashEngine(&m)
	if err != nil {
		t.Fatalf("buildSquashEngine with a real repo and provider should not error: %v", err)
	}
	if eng == nil {
		t.Error("buildSquashEngine should return a non-nil engine when a provider is set")
	}
}

// TestBuildSquashEngine_withZeroTimeout verifies that a zero TimeoutSec is
// clamped to the 60-second default inside buildSquashEngine.
func TestBuildSquashEngine_withZeroTimeout(t *testing.T) {
	m := newTestModelN(t, 2)
	m.cfg.LLM.Provider = "ollama"
	m.cfg.LLM.TimeoutSec = 0 // zero triggers the clamping branch
	eng, err := buildSquashEngine(&m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if eng == nil {
		t.Error("engine should be non-nil even with zero TimeoutSec")
	}
}

// TestPendingPreviewLen_nonNilPending verifies that pendingPreviewLen returns
// the correct number of preview lines when a squashChange is staged.
func TestPendingPreviewLen_nonNilPending(t *testing.T) {
	commits := linearModelCommits("a", "b", "c")
	chains, _ := model.DetectChains(commits, map[string]bool{"a": true, "b": true})
	chains[0] = model.DefaultSquashConfig(chains[0])
	chains[0].Subject = "combined"
	m := modelWithSet(commits)
	m.pending = squashChange{commits: commits, plan: model.SquashPlan{Chains: chains}}
	n := m.pendingPreviewLen()
	if n != 1 {
		t.Errorf("pendingPreviewLen with one chain = %d, want 1", n)
	}
}

// TestPendingPreviewLen_nilPending verifies the nil-guard in pendingPreviewLen.
func TestPendingPreviewLen_nilPending(t *testing.T) {
	m := modelWithSet(linearModelCommits("a", "b"))
	if n := m.pendingPreviewLen(); n != 0 {
		t.Errorf("pendingPreviewLen with nil pending = %d, want 0", n)
	}
}

// TestTimeModelByName_fallbackForUnknown verifies that timeModelByName returns
// a usable time model (WorkingHoursModel) when the name is not registered.
func TestTimeModelByName_fallbackForUnknown(t *testing.T) {
	got := timeModelByName("this-time-model-does-not-exist")
	if got.Name() == "" {
		t.Error("fallback model must have a non-empty name")
	}
}

// TestStageSquash_dirtyTreeBlocksPipeline: a dirty tree must be caught by the
// pre-flight guard so the wizard never enters the apply pipeline (H1).
func TestStageSquash_dirtyTreeBlocksPipeline(t *testing.T) {
	m := squashModelReady(linearModelCommits("a", "b", "c"), "a", "b", "c")
	m.state.IsDirty = true
	out, _ := m.stageSquash()
	mm := out.(Model)
	if mm.mode == modePipeline {
		t.Error("a dirty tree must block squash, not enter the pipeline")
	}
}

// TestStageSquash_pushedHistorySetsConfirmGuard: pushed history must set
// pendingGuard.NeedsConfirmation so updatePipeline runs the typed-confirm step (H1).
func TestStageSquash_pushedHistorySetsConfirmGuard(t *testing.T) {
	m := squashModelReady(linearModelCommits("a", "b", "c"), "a", "b", "c")
	m.state = model.RepoState{Branch: "main", HasUpstream: true, UpstreamRef: "origin/main"}
	out, _ := m.stageSquash()
	mm := out.(Model)
	if mm.mode != modePipeline {
		t.Fatalf("clean pushed history should enter the pipeline, got mode %v", mm.mode)
	}
	if !mm.pendingGuard.NeedsConfirmation {
		t.Error("pushed history must set pendingGuard.NeedsConfirmation")
	}
}

// TestEditActiveField_backspaceRuneSafe: backspace must delete a whole rune, not
// a single byte, so multibyte field content is not corrupted (M1).
func TestEditActiveField_backspaceRuneSafe(t *testing.T) {
	commits := linearModelCommits("a", "b")
	chains, _ := model.DetectChains(commits, map[string]bool{"a": true, "b": true})
	st := squashState{chains: chains, tab: 0, field: fldTitle, loading: map[string]bool{}}
	st.chains[0].Subject = "café"
	st2 := editActiveField(st, "", true)
	if st2.chains[0].Subject != "caf" {
		t.Errorf("rune-safe backspace should yield %q, got %q", "caf", st2.chains[0].Subject)
	}
}
