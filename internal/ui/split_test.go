package ui

import (
	"strings"
	"testing"

	"github.com/wlame/regit/internal/model"
)

// sampleProposalForUI returns a fixed two-group SplitProposal for use in UI tests.
// Group 1 passes tests; group 2 is marked WIP so we can assert that the WIP
// marker appears in the rendered review.
func sampleProposalForUI() model.SplitProposal {
	return model.SplitProposal{
		CommitHash:  "abc1234567890000",
		ShouldSplit: true,
		Groups: []model.HunkGroup{
			{
				Order:     1,
				Subject:   "Refactor the first thing",
				Intent:    "cleanup",
				TestsPass: true,
				WIP:       false,
			},
			{
				Order:     2,
				Subject:   "Add the second feature",
				Intent:    "feature",
				TestsPass: false,
				WIP:       true,
			},
		},
	}
}

func TestSplitReview_rendersGroups(t *testing.T) {
	prop := sampleProposalForUI()
	sc := splitChange{commit: model.Commit{Hash: prop.CommitHash}, commitHash: prop.CommitHash, groups: prop.Groups}
	view := strings.Join(sc.preview(newStyles()).lines, "\n")
	if !strings.Contains(view, "first") || !strings.Contains(view, "WIP") {
		t.Fatalf("split review missing expected content:\n%s", view)
	}
}

// TestBeginSplit_noLLMShowsError: newTestModelN builds its config via
// config.Default(), which leaves LLM.Provider empty, so buildSplitInputs always
// fails fast with "no LLM configured" here — there is no way to inject a fake
// provider through the test harness. beginSplit must surface that as a status
// error and stay in modeMain rather than entering modeSplitGen with no engine to
// run. (The async-entry path — beginSplit firing a command and moving to
// modeSplitGen — is exercised implicitly by TestSplitResultMsg_staleResultIgnored
// driving the result side of that transition.)
func TestBeginSplit_noLLMShowsError(t *testing.T) {
	m := newTestModelN(t, 3)
	updated, cmd, ok := m.beginSplit()
	m = updated.(Model)
	if !ok {
		t.Fatal("beginSplit should report handled=true")
	}
	if cmd != nil {
		t.Errorf("beginSplit with no LLM configured should return a nil command, got %v", cmd)
	}
	if m.mode != modeMain {
		t.Errorf("beginSplit with no LLM configured should stay in modeMain, got %v", m.mode)
	}
	if m.statusKind != statusErr {
		t.Errorf("beginSplit with no LLM configured should set a status error, got kind %v", m.statusKind)
	}
	if !strings.Contains(m.statusMsg, "no LLM configured") {
		t.Errorf("status message should explain the missing LLM config, got %q", m.statusMsg)
	}
}

// TestSplitResultMsg_staleResultIgnored: a splitResultMsg for an abandoned
// generation — either the mode has moved on (esc, or the user opened another
// overlay) or the seq token no longer matches the active generation — must be
// dropped rather than reviving a pipeline stage for a split the user cancelled.
func TestSplitResultMsg_staleResultIgnored(t *testing.T) {
	m := newTestModelN(t, 3)
	m.mode = modeSplitGen
	m.splitGenHash = "activehash"
	m.splitGenSeq = 2
	updated, _ := m.Update(splitResultMsg{hash: "otherhash", seq: 1, state: splitState{proposal: model.SplitProposal{ShouldSplit: true}}})
	m = updated.(Model)
	if m.mode != modeSplitGen {
		t.Errorf("a stale split result must be ignored, mode changed to %v", m.mode)
	}
}

// TestSplitResultMsg_forActiveCommitStagesPipeline: a result matching the mode and
// seq token the TUI is actively waiting on moves into the apply pipeline.
func TestSplitResultMsg_forActiveCommitStagesPipeline(t *testing.T) {
	m := newTestModelN(t, 4)
	target := m.plan.Commits[1]
	m.mode = modeSplitGen
	m.splitGenHash = target.Hash
	m.splitGenSeq = 1
	prop := model.SplitProposal{
		CommitHash: target.Hash,
		ParentHash: m.plan.Commits[0].Hash,
		Groups: []model.HunkGroup{
			{Order: 1, Subject: "part one"},
			{Order: 2, Subject: "part two"},
		},
	}
	updated, _ := m.Update(splitResultMsg{hash: target.Hash, seq: 1, state: splitState{proposal: prop, trees: []string{"t1", "t2"}}})
	m = updated.(Model)
	if m.mode != modePipeline {
		t.Fatalf("a result for the active split should enter modePipeline, got %v", m.mode)
	}
	if len(m.pendingCmds) == 0 {
		t.Error("pendingCmds (transparency footer) should be set once the split is staged")
	}
}

// TestSplitResultMsg_engineErrorReturnsToMain: an errMsg on the result (e.g. the
// engine declined to split, or Propose failed) surfaces as a status error and
// returns to modeMain rather than leaving the spinner running forever.
func TestSplitResultMsg_engineErrorReturnsToMain(t *testing.T) {
	m := newTestModelN(t, 3)
	target := m.commitAtCursor()
	m.mode = modeSplitGen
	m.splitGenHash = target.Hash
	m.splitGenSeq = 1
	updated, _ := m.Update(splitResultMsg{hash: target.Hash, seq: 1, state: splitState{errMsg: "not splitting: too small"}})
	m = updated.(Model)
	if m.mode != modeMain {
		t.Errorf("an engine error result should return to modeMain, got %v", m.mode)
	}
	if m.statusKind != statusErr || !strings.Contains(m.statusMsg, "not splitting") {
		t.Errorf("engine error should surface as a status error, got kind=%v msg=%q", m.statusKind, m.statusMsg)
	}
}

// TestSplitResultMsg_sameHashRestartDropsStale: aborting a split on a commit
// (esc → modeMain, but the background Propose keeps running with
// context.Background()) and immediately restarting a split on the SAME commit
// produces two Propose calls that share one hash. Only the seq token — bumped
// by beginSplit on every dispatch — distinguishes the abandoned first attempt
// from the fresh second one, so the stale seq-1 result must be dropped even
// though its hash matches the commit the TUI is currently generating for, and
// the seq-2 result (the actual current generation) must be accepted.
func TestSplitResultMsg_sameHashRestartDropsStale(t *testing.T) {
	m := newTestModelN(t, 4)
	target := m.plan.Commits[1]
	m.mode = modeSplitGen
	m.splitGenHash = target.Hash
	// seq=2 simulates the second generation on this commit, after an abort (seq=1
	// abandoned in flight) and an immediate restart.
	m.splitGenSeq = 2
	prop := model.SplitProposal{
		CommitHash: target.Hash,
		ParentHash: m.plan.Commits[0].Hash,
		Groups: []model.HunkGroup{
			{Order: 1, Subject: "part one"},
			{Order: 2, Subject: "part two"},
		},
	}

	// The abandoned seq=1 generation's result arrives first, same hash as the
	// current (seq=2) generation. It must be dropped, leaving modeSplitGen intact.
	updated, _ := m.Update(splitResultMsg{hash: target.Hash, seq: 1, state: splitState{proposal: prop, trees: []string{"t1", "t2"}}})
	m = updated.(Model)
	if m.mode != modeSplitGen {
		t.Fatalf("the stale seq-1 result (same hash as the active generation) must be dropped, mode changed to %v", m.mode)
	}
	if m.splitGenSeq != 2 {
		t.Fatalf("splitGenSeq must be untouched by the dropped stale result, got %d", m.splitGenSeq)
	}

	// The current seq=2 generation's result arrives next and must be accepted.
	updated, _ = m.Update(splitResultMsg{hash: target.Hash, seq: 2, state: splitState{proposal: prop, trees: []string{"t1", "t2"}}})
	m = updated.(Model)
	if m.mode != modePipeline {
		t.Fatalf("the current seq-2 result must be accepted into modePipeline, got %v", m.mode)
	}
	if len(m.pendingCmds) == 0 {
		t.Error("pendingCmds (transparency footer) should be set once the seq-2 split is staged")
	}
}

// TestSplitGen_escAbandons: esc while modeSplitGen returns to modeMain and warns
// that the split was cancelled, so a later splitResultMsg for that hash arrives
// after the mode has moved on and is dropped by the guard above.
func TestSplitGen_escAbandons(t *testing.T) {
	m := newTestModelN(t, 3)
	m.mode = modeSplitGen
	m.splitGenHash = m.commitAtCursor().Hash
	m = send(m, "esc")
	if m.mode != modeMain {
		t.Errorf("esc from modeSplitGen should return to modeMain, got %v", m.mode)
	}
	if m.statusKind != statusWarn {
		t.Errorf("esc from modeSplitGen should set a warning status, got kind %v", m.statusKind)
	}
}
