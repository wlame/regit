package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/wlame/regit/internal/capability"
	"github.com/wlame/regit/internal/config"
	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/model"
	"github.com/wlame/regit/internal/redact"
	"github.com/wlame/regit/internal/safety"
)

// redact_test.go exercises the redact flow (redact.go): flow entry from the
// action panel, the async scan's seq guard, the occurrence picker's
// toggle/veto mapping to redact.Selection, the mode step, and redactChange's
// pendingChange contract (guard/preview/commandLines).

// redactTestModel builds a Model over a small repo whose history contains a
// literal secret suitable for a redact scan: c0 adds cfg.yaml carrying
// "token: sk-OLD"; c1 touches an unrelated file (cfg.yaml rides along
// unmodified); c2 rotates ONLY the secret line to "token: sk-NEW" — a cascade
// descendant of c0's occurrence; c3 touches the unrelated file again. Mirrors
// internal/capability/run_redact_test.go's redactRotationFiles fixture, so a
// scan for "sk-OLD" always finds exactly one occurrence with one descendant.
func redactTestModel(t *testing.T) Model {
	t.Helper()
	fixtureMu.Lock()
	defer fixtureMu.Unlock()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = hermeticGitEnv(dir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-q")
	run("symbolic-ref", "HEAD", "refs/heads/main")
	run("config", "user.name", "wlame")
	run("config", "user.email", "vvlame@gmail.com")
	run("config", "commit.gpgsign", "false")

	write("cfg.yaml", "a\ntoken: sk-OLD\n")
	run("add", ".")
	run("commit", "-q", "-m", "c0 add secret")
	write("other.txt", "unrelated-v1\n")
	run("add", ".")
	run("commit", "-q", "-m", "c1 unrelated")
	write("cfg.yaml", "a\ntoken: sk-NEW\n")
	run("add", ".")
	run("commit", "-q", "-m", "c2 rotate secret")
	write("other.txt", "unrelated-v2\n")
	run("add", ".")
	run("commit", "-q", "-m", "c3 unrelated again")

	return openRedactTestModel(t, dir)
}

// redactSideHistoryTestModel builds the "pocket" fixture (mirrors
// internal/capability/run_redact_test.go's newRedactRepoWithSideHistoryPocket
// exactly): a side branch ADDS then DELETES the same literal (net zero by
// merge time, so the merge's own tree never carries it and the merge stays
// OUT of the rewritten suffix), merges into main with no further change, and
// a LATER, unrelated main commit introduces the identical literal for real.
// This lands RedactBase at that later main commit — independent of the
// merge — so guard() never blocks on "cannot redact across a merge commit",
// letting a test isolate the SIDE-HISTORY ADVISORY path specifically: Scan's
// full-history pickaxe search (git log -S, all parents) still discovers the
// side branch's own add commit as a distinct occurrence, and
// gitio.SecretInSideHistory still flags it as unreachable-by-rewrite.
func redactSideHistoryTestModel(t *testing.T) Model {
	t.Helper()
	fixtureMu.Lock()
	defer fixtureMu.Unlock()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = hermeticGitEnv(dir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-q")
	run("symbolic-ref", "HEAD", "refs/heads/main")
	run("config", "user.name", "wlame")
	run("config", "user.email", "vvlame@gmail.com")
	run("config", "commit.gpgsign", "false")

	write("keep.txt", "v1")
	run("add", ".")
	run("commit", "-q", "-m", "c0 keep only")
	run("checkout", "-q", "-b", "side")
	write("side.txt", "token: sk-SHARED\n")
	run("add", ".")
	run("commit", "-q", "-m", "side adds the shared secret")
	run("rm", "-q", "side.txt")
	run("commit", "-q", "-m", "side deletes it again (pocket)")
	run("checkout", "-q", "main")
	run("merge", "--no-ff", "-q", "-m", "merge side (pocket)", "side")
	write("cfg.yaml", "token: sk-SHARED\n")
	run("add", ".")
	run("commit", "-q", "-m", "main adds the secret for real")

	return openRedactTestModel(t, dir)
}

// openRedactTestModel finishes a fixture directory into a sized Model,
// shared by redactTestModel and redactSideHistoryTestModel.
func openRedactTestModel(t *testing.T, dir string) Model {
	t.Helper()
	repo, err := gitio.Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	state, err := repo.ReadState()
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatalf("read commits: %v", err)
	}
	cfg := config.Default()
	cfg.Timezone = "UTC"
	m := New(repo, safety.NewManager(repo), cfg, state, commits)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	return updated.(Model)
}

// scanRedact runs a REAL redact.Engine scan against m's repo for match,
// bypassing the async tea.Cmd plumbing beginRedactScan uses — these tests
// need realistic scan data (real hashes/paths/lines), not a re-exercise of
// the async delivery mechanism itself, which
// TestRedactScan_staleResultDroppedBySeqGuard covers separately.
func scanRedact(t *testing.T, m Model, match string) redact.ScanResult {
	t.Helper()
	eng := redact.Engine{Repo: m.repo}
	res, err := eng.Scan(redact.ScanOptions{Match: match, Cascade: true})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	return res
}

// redactModelAtModeStep drives a redactTestModel through step 1 (match) and
// step 2 (picker, default all-checked) to land on step 3 (mode) — the
// jumping-off point most of this file's step-3 tests share.
func redactModelAtModeStep(t *testing.T, match string) Model {
	t.Helper()
	m := redactTestModel(t)
	m.enterRedactFlow()
	res := scanRedact(t, m, match)
	m.redactSt.matchInput.SetValue(match)
	m.redactSt.scanRes = res
	m.redactSt.rows = buildRedactRows(res)
	m.redactSt.sel = capability.AllOccurrencesSelected(res)
	m.redactSt.step = redactStepPick
	m = send(m, "tab")
	if m.redactSt.step != redactStepMode {
		t.Fatalf("setup: expected redactStepMode, got %v (err=%q)", m.redactSt.step, m.redactSt.errMsg)
	}
	return m
}

// TestRedactFeatureBinding_entersFlowFromPanelRow: the panel row exists,
// is enabled with no explicit commit selection (redact is SelGlobal), and
// running it opens the flow at step 1.
func TestRedactFeatureBinding_entersFlowFromPanelRow(t *testing.T) {
	m := redactTestModel(t)
	b, ok := featureBindings["redact"]
	if !ok {
		t.Fatal(`featureBindings has no "redact" entry`)
	}

	var row panelRow
	found := false
	for _, r := range m.panelRows() {
		if r.Verb == "redact" {
			row, found = r, true
		}
	}
	if !found {
		t.Fatal(`"redact" missing from panelRows`)
	}
	if !row.Enabled {
		t.Errorf("redact row should be enabled with no explicit selection (SelGlobal), detail=%q", row.Detail)
	}

	updated, _ := b.Run(m)
	mm := updated.(Model)
	if mm.flow != flowRedact || mm.focus != focusRedact {
		t.Fatalf("flow=%v focus=%v, want flowRedact/focusRedact", mm.flow, mm.focus)
	}
	if mm.redactSt.step != redactStepMatch {
		t.Errorf("step = %v, want redactStepMatch", mm.redactSt.step)
	}
}

// TestEnterRedactFlow_doesNotTouchSelection: redact is SelGlobal/repo-level,
// so opening it must leave an explicit commit-list selection exactly as it
// was — unlike enterRedateFlow, which scopes to the selection.
func TestEnterRedactFlow_doesNotTouchSelection(t *testing.T) {
	m := redactTestModel(t)
	h := m.plan.Commits[0].Hash
	m.sel.toggle(h, false)
	wantMode := m.sel.mode

	m.enterRedactFlow()

	if m.sel.mode != wantMode || !m.sel.isSet(h) {
		t.Fatalf("redact flow entry touched the selection: mode=%v", m.sel.mode)
	}
}

// TestEnterRedactFlow_noExplicitSelectionStaysSingle is the companion no-op
// case: with nothing explicitly selected, opening redact must not invent a
// scope the way enterRedateFlow's implicit Active default does.
func TestEnterRedactFlow_noExplicitSelectionStaysSingle(t *testing.T) {
	m := redactTestModel(t)
	m.enterRedactFlow()
	if m.sel.mode != SelSingle {
		t.Fatalf("sel.mode = %v, want SelSingle (redact must not default to a scope)", m.sel.mode)
	}
}

// TestRedactScan_staleResultDroppedBySeqGuard: a result from an abandoned
// scan (the flow was closed and reopened before the first scan returned)
// must not clobber a newer, still in-flight scan's state — mirrors
// purgeScanMsg's identical seq guard.
func TestRedactScan_staleResultDroppedBySeqGuard(t *testing.T) {
	m := redactTestModel(t)
	m.enterRedactFlow()
	m.redactSt.matchInput.SetValue("sk-OLD")
	updated, cmd := m.beginRedactScan()
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("beginRedactScan should return a non-nil tea.Cmd")
	}
	if !m.redactSt.scanning {
		t.Fatal("setup: scanning should be true right after beginRedactScan")
	}
	staleSeq := m.redactScanSeq

	// Abandon this scan and start a fresh session with a new scan.
	m.exitFlow()
	m.enterRedactFlow()
	m.redactSt.matchInput.SetValue("sk-OLD")
	updated2, _ := m.beginRedactScan()
	m = updated2.(Model)
	if m.redactScanSeq == staleSeq {
		t.Fatal("setup: the second scan should have a newer seq than the first")
	}

	// The FIRST (abandoned) scan's result arrives late: dropped.
	stale := redact.ScanResult{Occurrences: []model.RedactOccurrence{{}}}
	updatedStale, _ := m.Update(redactScanMsg{res: stale, seq: staleSeq})
	m = updatedStale.(Model)
	if !m.redactSt.scanning || len(m.redactSt.rows) != 0 {
		t.Fatalf("stale scan result should be dropped: scanning=%v rows=%d",
			m.redactSt.scanning, len(m.redactSt.rows))
	}

	// The CURRENT scan's own result is accepted.
	res := scanRedact(t, m, "sk-OLD")
	updatedCurrent, _ := m.Update(redactScanMsg{res: res, seq: m.redactScanSeq})
	m = updatedCurrent.(Model)
	if m.redactSt.scanning {
		t.Error("the current scan's result should clear scanning")
	}
	if len(m.redactSt.rows) == 0 {
		t.Error("expected rows to be populated from the accepted scan result")
	}
}

// TestRedactPick_toggleAndVetoMapToSelection: space toggles the row under
// the cursor (occurrence or a cascade descendant independently), a/n check
// all/none — all mapping directly onto parallel redact.Selection entries.
func TestRedactPick_toggleAndVetoMapToSelection(t *testing.T) {
	m := redactTestModel(t)
	m.enterRedactFlow()
	res := scanRedact(t, m, "sk-OLD")
	if len(res.Occurrences) != 1 || len(res.Occurrences[0].Descendants) != 1 {
		t.Fatalf("setup: expected 1 occurrence with 1 descendant, got %d occurrence(s)", len(res.Occurrences))
	}
	m.redactSt.scanRes = res
	m.redactSt.rows = buildRedactRows(res)
	m.redactSt.sel = capability.AllOccurrencesSelected(res)
	m.redactSt.step = redactStepPick

	if !m.redactSt.sel[0].Occurrence || !m.redactSt.sel[0].Descendants[0] {
		t.Fatal("setup: default selection should have everything checked")
	}

	// Toggle the occurrence row (cursor starts at row 0) off.
	m = send(m, " ")
	if m.redactSt.sel[0].Occurrence {
		t.Error("space should have unchecked the occurrence row")
	}
	if !m.redactSt.sel[0].Descendants[0] {
		t.Error("toggling the occurrence row must not affect its descendant")
	}

	// Move to the descendant row (row 1) and veto it too.
	m = send(m, "down", " ")
	if m.redactSt.sel[0].Descendants[0] {
		t.Error("space on the descendant row should have unchecked it")
	}

	m = send(m, "a")
	if !m.redactSt.sel[0].Occurrence || !m.redactSt.sel[0].Descendants[0] {
		t.Error("'a' should check every row")
	}

	m = send(m, "n")
	if m.redactSt.sel[0].Occurrence || m.redactSt.sel[0].Descendants[0] {
		t.Error("'n' should uncheck every row")
	}
}

// TestRedactMode_replaceRequiresPatternBeforeStaging: switching to replace
// mode without typing a pattern must reject staging with an inline error,
// never silently fall through to modePipeline.
func TestRedactMode_replaceRequiresPatternBeforeStaging(t *testing.T) {
	m := redactModelAtModeStep(t, "sk-OLD")

	m = send(m, "right", "enter") // remove -> replace, then try to stage

	if m.mode == modePipeline {
		t.Fatal("staging should have been rejected: replace mode needs a pattern")
	}
	if m.redactSt.errMsg == "" {
		t.Error("expected an error message about the missing pattern")
	}
}

// TestRedactChange_commandLinesRoundTripsThroughParse: the staged change's
// logged command must re-parse through capability.Parse to the same values
// it was rendered from (the same contract render_test.go pins for
// RedactArgMap directly).
func TestRedactChange_commandLinesRoundTripsThroughParse(t *testing.T) {
	m := redactModelAtModeStep(t, "sk-OLD")
	m = send(m, "enter") // stage in the default (remove) mode

	if m.mode != modePipeline {
		t.Fatalf("expected modePipeline after staging, got %v (err=%q)", m.mode, m.redactSt.errMsg)
	}
	rc, ok := m.pending.(redactChange)
	if !ok {
		t.Fatalf("m.pending is %T, want redactChange", m.pending)
	}
	lines := rc.commandLines()
	if len(lines) == 0 {
		t.Fatal("commandLines should not be empty")
	}
	toks := strings.Fields(strings.ReplaceAll(lines[0], "\\\n", " "))
	if len(toks) < 2 || toks[0] != "regit" || toks[1] != "redact" {
		t.Fatalf("unexpected command line: %q", lines[0])
	}
	c, ok := capability.Lookup("redact")
	if !ok {
		t.Fatal("redact missing from the registry")
	}
	parsed, err := capability.Parse(c, toks[2:])
	if err != nil {
		t.Fatalf("re-parse: %v (line: %q)", err, lines[0])
	}
	if got := parsed.String("match"); got != "sk-OLD" {
		t.Errorf("match round-trip: got %q, line: %q", got, lines[0])
	}
	if !parsed.Bool("execute") {
		t.Error("execute should round-trip true")
	}
}

// TestRedactChange_narrowedNoteAppearsWhenDescendantVetoed: vetoing even one
// cascade descendant makes allSelected false, which must surface as the
// "#"-prefixed narrowing note alongside the (still headless-all) rendered
// command — see redactChange.commandLines' doc comment.
func TestRedactChange_narrowedNoteAppearsWhenDescendantVetoed(t *testing.T) {
	m := redactTestModel(t)
	m.enterRedactFlow()
	res := scanRedact(t, m, "sk-OLD")
	m.redactSt.matchInput.SetValue("sk-OLD")
	m.redactSt.scanRes = res
	m.redactSt.rows = buildRedactRows(res)
	m.redactSt.sel = capability.AllOccurrencesSelected(res)
	m.redactSt.step = redactStepPick

	// Veto the cascade descendant (row 1), then continue to stage.
	m = send(m, "down", " ", "tab", "enter")

	if m.mode != modePipeline {
		t.Fatalf("expected modePipeline after staging, got %v (err=%q)", m.mode, m.redactSt.errMsg)
	}
	rc, ok := m.pending.(redactChange)
	if !ok {
		t.Fatalf("m.pending is %T, want redactChange", m.pending)
	}
	if rc.allSelected {
		t.Fatal("vetoing a descendant should leave allSelected false")
	}
	found := false
	for _, l := range rc.commandLines() {
		if strings.HasPrefix(l, "# selection narrowed") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a narrowed-selection note in commandLines, got: %v", rc.commandLines())
	}

	// The pipeline PREVIEW must also disclose the narrowing — otherwise the
	// operator reviewing before confirming has no way to tell, from that
	// screen alone, that the vetoed descendant (still listed by
	// capability.RedactPreviewLines, which has no notion of the picker's
	// vetoes) will actually be left untouched.
	pv := rc.preview(m.st)
	previewFound := false
	for _, l := range pv.lines {
		if strings.Contains(l, "left UNCHECKED in the picker") {
			previewFound = true
		}
	}
	if !previewFound {
		t.Errorf("expected the narrowed-selection note in the preview, got:\n%s", strings.Join(pv.lines, "\n"))
	}
}

// TestRedactFlow_escNeverMutatesCommitListSelection: esc exits the flow from
// any step without ever touching an explicit commit-list selection —
// redact's whole point is that it is orthogonal to the selection.
func TestRedactFlow_escNeverMutatesCommitListSelection(t *testing.T) {
	m := redactTestModel(t)
	h := m.plan.Commits[0].Hash
	m.sel.toggle(h, false)
	wantMode := m.sel.mode

	m.enterRedactFlow()
	m = send(m, "esc")

	if m.sel.mode != wantMode || !m.sel.isSet(h) {
		t.Fatalf("esc from the redact flow mutated the selection: mode=%v", m.sel.mode)
	}
	if m.flow != flowNone || m.focus != focusList {
		t.Fatalf("esc should exit the flow: flow=%v focus=%v", m.flow, m.focus)
	}
}

// TestRedactFlow_escExitsFromEveryStep: "esc at any step exits the flow" is
// the flow's contract (redact.go's updateRedactFlow doc comment); pin it for
// each of the three steps independently rather than just the default one.
func TestRedactFlow_escExitsFromEveryStep(t *testing.T) {
	for _, step := range []redactStep{redactStepMatch, redactStepPick, redactStepMode} {
		m := redactTestModel(t)
		m.enterRedactFlow()
		m.redactSt.step = step

		m = send(m, "esc")

		if m.flow != flowNone || m.focus != focusList {
			t.Errorf("step %v: esc should exit the flow, got flow=%v focus=%v", step, m.flow, m.focus)
		}
	}
}

// TestRedactChange_previewWarnsOnSideHistoryFixture drives the flow on the
// pocket merge fixture with everything checked (the headless-equivalent,
// allSelected=true selection) and confirms: guard() does NOT block (redact's
// side-history handling is advisory, unlike purge's hard PathsInSideHistory
// block — see redactChange.guard's doc comment), and preview() surfaces the
// SAME coherent side-history note run_redact prints headless
// (capability.SideHistoryAdvisory).
func TestRedactChange_previewWarnsOnSideHistoryFixture(t *testing.T) {
	m := redactSideHistoryTestModel(t)
	m.enterRedactFlow()
	res := scanRedact(t, m, "sk-SHARED")
	if len(res.Occurrences) == 0 {
		t.Fatal("setup: expected at least 1 occurrence on the pocket fixture")
	}
	m.redactSt.matchInput.SetValue("sk-SHARED")
	m.redactSt.scanRes = res
	m.redactSt.rows = buildRedactRows(res)
	m.redactSt.sel = capability.AllOccurrencesSelected(res)
	m.redactSt.step = redactStepPick

	m = send(m, "tab", "enter") // step2 -> step3 (default remove mode) -> stage

	if m.mode != modePipeline {
		t.Fatalf("expected modePipeline after staging, got %v (err=%q)", m.mode, m.redactSt.errMsg)
	}
	rc, ok := m.pending.(redactChange)
	if !ok {
		t.Fatalf("m.pending is %T, want redactChange", m.pending)
	}
	if !rc.sideHistory {
		t.Fatal("expected sideHistory to be detected on the pocket merge fixture")
	}
	if m.pendingGuard.Blocked {
		t.Error("side-branch history must be advisory, not a guard block")
	}
	pv := rc.preview(m.st)
	found := false
	for _, l := range pv.lines {
		if strings.Contains(l, "merged side-branch history") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected the side-history advisory in the preview, got:\n%s", strings.Join(pv.lines, "\n"))
	}
}

// TestRedactChange_previewOmitsAbortClaimWhenSelectionNarrowedOnSideHistoryFixture
// is the companion negative case: SideHistoryAdvisory predicts
// safety.Manager.ApplyRedact's GLOBAL absence tier aborting, but that tier
// only ever runs when allSelected (see verifyRedactAbsence's doc comment) —
// so on the SAME pocket fixture, once the operator vetoes the side-branch
// occurrence specifically (narrowing the selection, allSelected=false), the
// preview must NOT claim the run "will abort" (it won't — the apply
// actually succeeds, silently leaving the side-branch copy unaddressed), and
// must instead show the narrowed-selection note.
func TestRedactChange_previewOmitsAbortClaimWhenSelectionNarrowedOnSideHistoryFixture(t *testing.T) {
	m := redactSideHistoryTestModel(t)
	m.enterRedactFlow()
	res := scanRedact(t, m, "sk-SHARED")
	sideIdx := -1
	for i, occ := range res.Occurrences {
		if occ.Intro.Path == "side.txt" {
			sideIdx = i
		}
	}
	if sideIdx < 0 {
		t.Fatalf("setup: expected an occurrence on side.txt among %d occurrence(s)", len(res.Occurrences))
	}
	m.redactSt.matchInput.SetValue("sk-SHARED")
	m.redactSt.scanRes = res
	m.redactSt.rows = buildRedactRows(res)
	m.redactSt.sel = capability.AllOccurrencesSelected(res)
	m.redactSt.sel[sideIdx].Occurrence = false // veto the side-branch occurrence specifically
	m.redactSt.step = redactStepPick

	m = send(m, "tab", "enter") // step2 -> step3 (default remove mode) -> stage

	if m.mode != modePipeline {
		t.Fatalf("expected modePipeline after staging, got %v (err=%q)", m.mode, m.redactSt.errMsg)
	}
	rc, ok := m.pending.(redactChange)
	if !ok {
		t.Fatalf("m.pending is %T, want redactChange", m.pending)
	}
	if rc.allSelected {
		t.Fatal("setup: vetoing the side-branch occurrence should leave allSelected false")
	}
	if !rc.sideHistory {
		t.Fatal("setup: sideHistory is independent of the picker selection and should still be true")
	}

	pv := rc.preview(m.st)
	for _, l := range pv.lines {
		if strings.Contains(l, "This run will abort") {
			t.Errorf("the side-history 'will abort' claim must not appear when allSelected is false "+
				"(the GLOBAL absence tier that would abort never runs for a narrowed selection), got:\n%s",
				strings.Join(pv.lines, "\n"))
		}
	}
	found := false
	for _, l := range pv.lines {
		if strings.Contains(l, "left UNCHECKED in the picker") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected the narrowed-selection note in the preview, got:\n%s", strings.Join(pv.lines, "\n"))
	}
}

// TestViewRedactFlow_rendersAtEveryStepWithoutPanicking is a rendering smoke
// test: each of the three steps' body must render non-empty, recognizable
// content, and the full Model.View() must not panic while the flow is open —
// the cheapest regression net against a template/format-verb mismatch in the
// render helpers, which the behavioral tests above never exercise (they only
// touch state, not renderRedactFlow's output).
func TestViewRedactFlow_rendersAtEveryStepWithoutPanicking(t *testing.T) {
	m := redactModelAtModeStep(t, "sk-OLD") // drives through step 1 and step 2 already; lands on step 3

	fresh := newRedactState(40)
	match := renderRedactFlow(fresh, m.st)
	if !strings.Contains(match, "REDACT") || !strings.Contains(match, "Match:") {
		t.Errorf("step 1 render missing expected content:\n%s", match)
	}

	stepTwo := m.redactSt
	stepTwo.step = redactStepPick
	pick := renderRedactFlow(stepTwo, m.st)
	if !strings.Contains(pick, "cfg.yaml") {
		t.Errorf("step 2 render missing an occurrence row:\n%s", pick)
	}
	if !strings.Contains(pick, "selected") {
		t.Errorf("step 2 render missing the checked-count line:\n%s", pick)
	}

	mode := renderRedactFlow(m.redactSt, m.st) // m is already at step 3
	if !strings.Contains(mode, "Mode:") {
		t.Errorf("step 3 render missing the mode toggle:\n%s", mode)
	}

	// A live replace-mode example must show the before/after preview line.
	replaceSt := m.redactSt
	replaceSt.mode = model.RedactReplace
	replaceSt.patternInput.SetValue("sk-.*")
	replaceModeBody := renderRedactFlow(replaceSt, m.st)
	if !strings.Contains(replaceModeBody, "→") {
		t.Errorf("replace mode render missing the live example:\n%s", replaceModeBody)
	}

	// The full View() must not panic across every step while the flow is
	// hosted in the flow zone (not just the pure render helpers above).
	for _, step := range []redactStep{redactStepMatch, redactStepPick, redactStepMode} {
		mm := m
		mm.redactSt.step = step
		_ = mm.View()
	}
}

// TestRedactFlow_realApplyRewritesHistoryAndClosesTheFlow drives the redact
// flow all the way to a REAL apply through commitPending (the same
// stage->modePipeline->enter path every other feature's end-to-end apply
// test uses — see TestDetail_refetchesAfterApply): the secret must actually
// leave the branch's history, the status line must carry the hygiene
// warning, and — because a redact's scanned occurrences reference commit
// hashes the rewrite just replaced — the flow must close rather than keep
// showing stale data (see commitPending's isRedact handling, app.go).
func TestRedactFlow_realApplyRewritesHistoryAndClosesTheFlow(t *testing.T) {
	m := redactModelAtModeStep(t, "sk-OLD")

	m = send(m, "enter") // stage (remove mode) -> modePipeline
	if m.mode != modePipeline {
		t.Fatalf("expected modePipeline after staging, got %v (err=%q)", m.mode, m.redactSt.errMsg)
	}

	m = send(m, "enter") // apply through the real commitPending

	if m.flow != flowNone || m.focus != focusList {
		t.Fatalf("a successful redact apply should close the flow: flow=%v focus=%v", m.flow, m.focus)
	}
	if m.mode != modeMain {
		t.Errorf("mode = %v, want modeMain after apply", m.mode)
	}
	if !strings.Contains(m.statusMsg, "rotate the credential") {
		t.Errorf("status line should carry the hygiene warning, got: %q", m.statusMsg)
	}
	found, err := m.repo.GrepTree("HEAD", "sk-OLD")
	if err != nil {
		t.Fatalf("GrepTree: %v", err)
	}
	if found {
		t.Error("sk-OLD should no longer be present at the rewritten HEAD's tree")
	}
	reachable, err := m.repo.SecretAddedFrom("HEAD", "sk-OLD")
	if err != nil {
		t.Fatalf("SecretAddedFrom: %v", err)
	}
	if len(reachable) > 0 {
		t.Errorf("sk-OLD should no longer be reachable from HEAD's history, found in %v", reachable)
	}
}
