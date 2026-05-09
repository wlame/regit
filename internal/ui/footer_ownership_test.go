package ui

// footer_ownership_test.go covers the command-lines-as-data ownership model:
// each pendingChange owns its own commandLines(), and the two model-level
// slots (redateCmd, the OVERWRITTEN live-restaging line; planCmds, the
// APPENDED one-shot plan-edit lines) are scoped correctly across staging,
// peeking, cancelling, and applying. See app.go's Model field comments for the
// redateCmd/planCmds split this file exercises end to end.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readCommandLog reads the on-disk .git/regit/command.log for the model's repo,
// mirroring appendCommandLog's own path construction. Returns "" if the file
// does not exist yet (e.g. no apply has happened).
func readCommandLog(t *testing.T, m Model) string {
	t.Helper()
	gitDir, err := m.repo.GitDir()
	if err != nil {
		t.Fatalf("GitDir: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(gitDir, "regit", "command.log"))
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatalf("reading command.log: %v", err)
	}
	return string(raw)
}

// TestFooter_stagingStrategyLogsExactlyOneRedateLine is the CRIT regression
// test: stageActive runs on every params keystroke/pin/carousel switch (a live
// re-staging site), so it must OVERWRITE redateCmd, not append to a shared
// slot — appending would fill the on-disk audit log with one duplicate/stale
// redate line per keystroke instead of the one real staged command.
func TestFooter_stagingStrategyLogsExactlyOneRedateLine(t *testing.T) {
	m := newTestModelN(t, 3)
	m = applyStrategyInPane(m) // fires stageActive on every digit of both dates, then on enter
	m = send(m, "A")
	m = send(m, "enter")
	if m.statusKind == statusErr {
		t.Fatalf("apply errored: %s", m.statusMsg)
	}

	log := readCommandLog(t, m)
	redateLines := 0
	for _, line := range strings.Split(log, "\n") {
		if strings.HasPrefix(line, "regit redate") {
			redateLines++
		}
	}
	if redateLines != 1 {
		t.Errorf("expected exactly 1 line starting with %q in command.log, got %d:\n%s", "regit redate", redateLines, log)
	}
}

// TestFooter_directApplyDoesNotFlushPlanLines proves a direct change (squash)
// flushes ONLY its own commandLines() — never the plan-edit lines (planCmds)
// that happen to be staged alongside it. Before the fix, commitPending flushed
// every pendingCmds entry on ANY apply, so a staged-but-never-run drop-bodies
// line would falsely appear in the squash's audit trail.
func TestFooter_directApplyDoesNotFlushPlanLines(t *testing.T) {
	m := newTestModelN(t, 4) // display: 0=newest .. 3=oldest/root

	// Stage a drop-bodies plan edit (a one-shot planCmds line) without applying it.
	m = runRestructureFeature(t, m, "Drop bodies")
	m = send(m, "enter")
	if len(m.planCmds) == 0 {
		t.Fatal("setup: drop-bodies should have staged a planCmds line")
	}

	// Select the two newest commits and squash them — a direct change with its
	// own cmd, unrelated to the plan edit above.
	m = send(m, " ", "down", " ")
	out, _, ok := m.beginSquash()
	if !ok {
		t.Fatal("beginSquash should handle the 2-commit selection")
	}
	m = out.(Model)
	if m.mode != modeSquash {
		t.Fatalf("beginSquash should enter modeSquash, got %v", m.mode)
	}

	out2, _ := m.stageSquash()
	m = out2.(Model)
	if m.mode != modePipeline {
		t.Fatalf("stageSquash should enter modePipeline, got %v", m.mode)
	}

	m = send(m, "enter") // no upstream configured -> applies directly, no confirm step
	if m.statusKind == statusErr {
		t.Fatalf("squash apply failed: %s", m.statusMsg)
	}

	log := readCommandLog(t, m)
	if !strings.Contains(log, "regit squash") {
		t.Errorf("expected the squash line in command.log, got:\n%s", log)
	}
	if strings.Contains(log, "drop-bodies") {
		t.Errorf("the staged (but never applied) drop-bodies plan line must not leak into the squash apply's log:\n%s", log)
	}
}

// TestFooter_peekDoesNotDropPlanLines proves a read-only peek (p) followed by
// esc must not drop plan-edit lines staged before the peek: they still
// describe m.plan, which survives the peek untouched. Before the fix,
// updatePipeline's esc/peek-close cleared the shared pendingCmds slot
// unconditionally, silently discarding real staged edits so the subsequent
// real apply logged nothing.
func TestFooter_peekDoesNotDropPlanLines(t *testing.T) {
	m := newTestModelN(t, 3)
	m = applyStrategyInPane(m)
	if m.redateCmd == "" {
		t.Fatal("setup: applyStrategyInPane should have staged redateCmd")
	}

	m = send(m, "p") // peek
	if m.mode != modePipeline {
		t.Fatalf("p should open the pipeline overlay, got %v", m.mode)
	}
	m = send(m, "esc") // close the peek
	if m.mode != modeMain {
		t.Fatalf("esc should return to modeMain, got %v", m.mode)
	}
	if m.redateCmd == "" {
		t.Fatal("the peek must not have dropped the staged redateCmd")
	}

	m = send(m, "A")
	m = send(m, "enter")
	if m.statusKind == statusErr {
		t.Fatalf("apply errored: %s", m.statusMsg)
	}

	log := readCommandLog(t, m)
	if !strings.Contains(log, "regit redate") {
		t.Errorf("command.log should contain the redate line staged before the peek, got:\n%s", log)
	}
	if !strings.Contains(log, "verified") {
		t.Errorf("command.log should contain a verification line, got:\n%s", log)
	}
}

// TestConfirmCancel_returnsToOriginAndNoStaleFlush covers the two confirm-cancel
// findings together: (1) cancelling the typed confirm for a pushed-history
// squash must return to pipelineOrigin (the wizard, modeSquash) instead of
// always modeMain, so the wizard's typed state is not silently discarded; and
// (2) the cancelled squash's command line — owned by the squashChange, not a
// shared slot — must not leak into a later, unrelated apply's log.
func TestConfirmCancel_returnsToOriginAndNoStaleFlush(t *testing.T) {
	m := newTestModelN(t, 4) // display: 0=newest .. 3=oldest/root, all local commits

	// Fabricate pushed-history repo state (squashChange.guard reads state-level
	// HasPushedCommits(), mirroring TestStageSquash_pushedHistorySetsConfirmGuard)
	// so the squash needs the typed confirm step, while keeping the real
	// repo/mgr backing this model so a later real apply can actually run.
	m.state.HasUpstream = true
	m.state.UpstreamRef = "origin/main"
	m.state.AheadOfRemote = 4

	m = send(m, " ", "down", " ") // select the two newest commits: a 2-member chain
	out, _, ok := m.beginSquash()
	if !ok {
		t.Fatal("beginSquash should handle the 2-commit selection")
	}
	m = out.(Model)

	out2, _ := m.stageSquash()
	m = out2.(Model)
	if m.mode != modePipeline {
		t.Fatalf("stageSquash should enter modePipeline, got %v", m.mode)
	}
	if m.pipelineOrigin != modeSquash {
		t.Fatalf("pipelineOrigin should be modeSquash, got %v", m.pipelineOrigin)
	}

	m = send(m, "enter")
	if m.mode != modeConfirm {
		t.Fatalf("a pushed-history squash should require the typed confirm, got mode %v", m.mode)
	}

	m = send(m, "esc") // cancel at the confirm prompt
	if m.mode != modeSquash {
		t.Errorf("esc at confirm should return to pipelineOrigin (modeSquash), got %v", m.mode)
	}
	if m.pending != nil {
		t.Error("esc at confirm should clear pending so its command line cannot leak later")
	}

	// Leave the wizard and apply an unrelated plan edit through the real pipeline.
	m = send(m, "esc")
	if m.mode != modeMain {
		t.Fatalf("esc in the wizard should return to modeMain, got %v", m.mode)
	}
	m = applyStrategyInPane(m)
	m = send(m, "A")
	m = send(m, "enter")
	if m.statusKind == statusErr {
		t.Fatalf("the unrelated apply failed: %s", m.statusMsg)
	}

	log := readCommandLog(t, m)
	if strings.Contains(log, "squash") {
		t.Errorf("the cancelled squash's line must not leak into a later apply's log:\n%s", log)
	}
	if !strings.Contains(log, "regit redate") {
		t.Errorf("the unrelated redate apply should still be logged:\n%s", log)
	}
}
