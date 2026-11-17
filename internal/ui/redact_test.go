package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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

// TestUpdateRedactMatch_tabMovesFocusAndTypingEditsFocusedField drives step 1
// through the REAL Update path (send, not direct state manipulation): tab
// moves field focus between match and glob, and each field only receives the
// characters typed while IT has focus — pinning both
// updateRedactMatch's key routing and redactFocusMatchField's blur/focus
// bookkeeping, which every other test in this file bypasses via
// matchInput.SetValue.
func TestUpdateRedactMatch_tabMovesFocusAndTypingEditsFocusedField(t *testing.T) {
	m := redactTestModel(t)
	m.enterRedactFlow()
	if m.redactSt.fieldFocus != 0 || !m.redactSt.matchInput.Focused() {
		t.Fatalf("setup: expected the match field focused at fieldFocus 0, got fieldFocus=%d matchFocused=%v",
			m.redactSt.fieldFocus, m.redactSt.matchInput.Focused())
	}

	m = send(m, "s", "k")
	if got := m.redactSt.matchInput.Value(); got != "sk" {
		t.Errorf("match field value = %q, want %q", got, "sk")
	}

	m = send(m, "tab")
	if m.redactSt.fieldFocus != 1 {
		t.Fatalf("fieldFocus after tab = %d, want 1 (glob)", m.redactSt.fieldFocus)
	}
	if m.redactSt.matchInput.Focused() {
		t.Error("match field should be blurred after tab")
	}
	if !m.redactSt.globInput.Focused() {
		t.Error("glob field should be focused after tab")
	}

	m = send(m, "*", ".", "e", "n", "v")
	if got := m.redactSt.globInput.Value(); got != "*.env" {
		t.Errorf("glob field value = %q, want %q", got, "*.env")
	}
	if got := m.redactSt.matchInput.Value(); got != "sk" {
		t.Errorf("match field value changed while typing into the glob field: got %q, want unchanged %q", got, "sk")
	}

	m = send(m, "shift+tab")
	if m.redactSt.fieldFocus != 0 {
		t.Fatalf("fieldFocus after shift+tab = %d, want 0 (match)", m.redactSt.fieldFocus)
	}
	if !m.redactSt.matchInput.Focused() {
		t.Error("match field should be focused again after shift+tab")
	}
	if m.redactSt.globInput.Focused() {
		t.Error("glob field should be blurred after shift+tab")
	}
}

// TestUpdateRedactMode_tabCyclesReplaceFieldsAndTypingEditsFocusedField is
// step 3's counterpart: after switching to replace mode, tab/shift+tab must
// move focus between the pattern and replacement rows (redactCycleModeFocus)
// and typing must land in whichever field currently has it — a swapped
// modeFocus index (1 vs. 2) would make this fail even though every other
// test in this file only ever reads/writes the fields directly.
func TestUpdateRedactMode_tabCyclesReplaceFieldsAndTypingEditsFocusedField(t *testing.T) {
	m := redactModelAtModeStep(t, "sk-OLD")
	if m.redactSt.modeFocus != 0 {
		t.Fatalf("setup: expected modeFocus 0 (mode row), got %d", m.redactSt.modeFocus)
	}

	m = send(m, "right") // remove -> replace; the mode row keeps focus
	if m.redactSt.mode != model.RedactReplace {
		t.Fatalf("setup: expected replace mode after 'right', got %v", m.redactSt.mode)
	}
	if m.redactSt.modeFocus != 0 {
		t.Fatalf("switching mode should not itself move focus off the mode row, got modeFocus=%d", m.redactSt.modeFocus)
	}

	m = send(m, "tab") // mode row -> pattern row
	if m.redactSt.modeFocus != 1 {
		t.Fatalf("modeFocus after tab = %d, want 1 (pattern)", m.redactSt.modeFocus)
	}
	if !m.redactSt.patternInput.Focused() {
		t.Error("pattern field should be focused")
	}
	if m.redactSt.replaceInput.Focused() {
		t.Error("replacement field should not be focused yet")
	}

	m = send(m, "s", "k", "-", ".", "*")
	if got := m.redactSt.patternInput.Value(); got != "sk-.*" {
		t.Errorf("pattern field value = %q, want %q", got, "sk-.*")
	}

	m = send(m, "tab") // pattern row -> replacement row
	if m.redactSt.modeFocus != 2 {
		t.Fatalf("modeFocus after second tab = %d, want 2 (replacement)", m.redactSt.modeFocus)
	}
	if m.redactSt.patternInput.Focused() {
		t.Error("pattern field should be blurred once the replacement row has focus")
	}
	if !m.redactSt.replaceInput.Focused() {
		t.Error("replacement field should be focused")
	}

	m = send(m, "R", "E", "D")
	if got := m.redactSt.replaceInput.Value(); got != "RED" {
		t.Errorf("replacement field value = %q, want %q", got, "RED")
	}
	if got := m.redactSt.patternInput.Value(); got != "sk-.*" {
		t.Errorf("pattern field value changed while editing replacement: got %q, want unchanged %q", got, "sk-.*")
	}

	m = send(m, "shift+tab") // replacement row -> pattern row
	if m.redactSt.modeFocus != 1 {
		t.Fatalf("modeFocus after shift+tab = %d, want 1 (pattern)", m.redactSt.modeFocus)
	}
	if !m.redactSt.patternInput.Focused() {
		t.Error("pattern field should be focused again")
	}
	if m.redactSt.replaceInput.Focused() {
		t.Error("replacement field should be blurred")
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
	if !strings.Contains(match, "REDACT") || !strings.Contains(match, "Secret to find") {
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
	// The on-disk command log outlives the session, so it must never record
	// the secret; the in-memory panel keeps the runnable line for copying.
	onDisk := readCommandLog(t, m)
	if strings.Contains(onDisk, "sk-OLD") {
		t.Errorf("the on-disk command log must not contain the secret:\n%s", onDisk)
	}
	if !strings.Contains(onDisk, "regit redact --match=") {
		t.Errorf("the on-disk command log should still record the redact, masked:\n%s", onDisk)
	}
	if !strings.Contains(strings.Join(m.commandLog, "\n"), "--match=sk-OLD") {
		t.Errorf("the in-memory panel should keep the runnable line:\n%s", strings.Join(m.commandLog, "\n"))
	}
}

// --- redactChange.guard() ----------------------------------------------------
//
// Pure-struct-literal guard tests, mirroring purge_test.go's
// TestPurgeChange_guardBlocksDirtyTree / _guardBlocksMergeAtStoredBase /
// _guardBlocksStoredSideHistory exactly: construct a redactChange with only
// the fields guard() actually reads (c.pv.Base/Affected/Dropped, no scan or
// repo needed), so each blocking branch is pinned in isolation without
// standing up a real repo fixture.

// TestRedactChange_guardBlocksNoTargetContent: pv.Base < 0 means
// BuildRedactPreview found no commit whose tree the spec's transform would
// change — the "nothing to redact" branch.
func TestRedactChange_guardBlocksNoTargetContent(t *testing.T) {
	rc := redactChange{pv: capability.RedactPreview{Base: -1}}
	g := rc.guard(model.RepoState{})
	if !g.Blocked {
		t.Fatal("pv.Base < 0 should block the redact guard")
	}
	if !strings.Contains(g.BlockReason, "no commit's snapshot") {
		t.Errorf("block reason should mention the missing content, got: %q", g.BlockReason)
	}
}

// TestRedactChange_guardBlocksMergeInAffected: a merge commit anywhere in
// pv.Affected (the rewritten suffix) blocks — mirrors
// TestPurgeChange_guardBlocksMergeAtStoredBase.
func TestRedactChange_guardBlocksMergeInAffected(t *testing.T) {
	rc := redactChange{pv: capability.RedactPreview{
		Base:     0,
		Affected: []model.Commit{{Hash: "a"}, {Hash: "merge", IsMerge: true}, {Hash: "c"}},
	}}
	g := rc.guard(model.RepoState{})
	if !g.Blocked {
		t.Fatal("a merge commit in pv.Affected should block the redact guard")
	}
	if !strings.Contains(g.BlockReason, "merge") {
		t.Errorf("block reason should mention merge, got: %q", g.BlockReason)
	}
}

// TestRedactChange_guardBlocksDirtyTree: an uncommitted working tree blocks —
// mirrors TestPurgeChange_guardBlocksDirtyTree.
func TestRedactChange_guardBlocksDirtyTree(t *testing.T) {
	rc := redactChange{pv: capability.RedactPreview{
		Base:     0,
		Affected: []model.Commit{{Hash: "a"}},
	}}
	g := rc.guard(model.RepoState{WorktreeStatus: model.WorktreeStatus{HasTrackedChanges: true}})
	if !g.Blocked {
		t.Fatal("a dirty tree should block the redact guard")
	}
	if !strings.Contains(g.BlockReason, "uncommitted changes") {
		t.Errorf("block reason should mention uncommitted changes, got: %q", g.BlockReason)
	}
}

// TestRedactChange_guardBlocksWouldEmptyBranch: Base==0 and every affected
// commit predicted Dropped means the redact would remove the whole branch —
// the pre-check run_redact.go's headless path also performs, mirrored here
// from the SAME pv.Base/pv.Dropped BuildRedactPreview produces (see guard's
// doc comment).
func TestRedactChange_guardBlocksWouldEmptyBranch(t *testing.T) {
	affected := []model.Commit{{Hash: "a"}, {Hash: "b"}}
	rc := redactChange{pv: capability.RedactPreview{
		Base:     0,
		Affected: affected,
		Dropped:  map[string]bool{"a": true, "b": true},
	}}
	g := rc.guard(model.RepoState{})
	if !g.Blocked {
		t.Fatal("Base==0 with every affected commit dropped should block the redact guard")
	}
	if !strings.Contains(g.BlockReason, "every commit") {
		t.Errorf("block reason should mention removing every commit, got: %q", g.BlockReason)
	}
}

// TestRedactChange_guardRequiresConfirmationForPushedCommits: a pushed
// commit in pv.Affected does not hard-block — it requires the typed
// branch-name confirmation, mirroring purgeChange's identical pushed case.
func TestRedactChange_guardRequiresConfirmationForPushedCommits(t *testing.T) {
	rc := redactChange{pv: capability.RedactPreview{
		Base:     0,
		Affected: []model.Commit{{Hash: "a", IsPushed: true}},
	}}
	g := rc.guard(model.RepoState{Branch: "main"})
	if g.Blocked {
		t.Fatal("a pushed commit should require confirmation, not hard-block")
	}
	if !g.NeedsConfirmation {
		t.Fatal("a pushed commit in pv.Affected should require confirmation")
	}
	if g.ConfirmWord != "main" {
		t.Errorf("ConfirmWord = %q, want the branch name %q", g.ConfirmWord, "main")
	}
}

// TestRedactChange_guardPassesCleanRedact is the positive case: none of the
// five blocking/confirming conditions apply, so guard() returns the zero
// Guard (not blocked, no confirmation needed).
func TestRedactChange_guardPassesCleanRedact(t *testing.T) {
	rc := redactChange{pv: capability.RedactPreview{
		Base:     0,
		Affected: []model.Commit{{Hash: "a"}},
	}}
	g := rc.guard(model.RepoState{})
	if g.Blocked || g.NeedsConfirmation {
		t.Fatalf("a clean redact should pass guard() unconditionally, got: %+v", g)
	}
}

func TestRedactMatchStep_explainsTheFocusedField(t *testing.T) {
	m := send(resizeTo(newTestModelN(t, 3), 120, 40), "m", "r")

	secretStep := stripANSI(m.viewRedactFlow())
	m = send(m, "tab")
	filesStep := stripANSI(m.viewRedactFlow())

	for _, label := range []string{"Secret to find", "Only in files"} {
		if !strings.Contains(secretStep, label) {
			t.Errorf("step 1 should label a field %q:\n%s", label, secretStep)
		}
	}
	if !strings.Contains(secretStep, "exact text") {
		t.Errorf("with the secret field focused, the hint should explain it:\n%s", secretStep)
	}
	if !strings.Contains(filesStep, "every file") {
		t.Errorf("with the files field focused, the hint should explain it:\n%s", filesStep)
	}
}

func TestRedactMatchStep_longSecretStaysOnOneLine(t *testing.T) {
	m := send(resizeTo(newTestModelN(t, 3), 120, 40), "m", "r")
	short := lipgloss.Height(m.viewRedactFlow())

	m = send(m, strings.Repeat("x", 60))

	if got := lipgloss.Height(m.viewRedactFlow()); got != short {
		t.Errorf("a 60-character secret grew the step from %d to %d rows; the field should scroll", short, got)
	}
}
