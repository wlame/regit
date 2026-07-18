package capability

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wlame/regit/internal/config"
	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/gittest"
	"github.com/wlame/regit/internal/model"
)

// newRedactRepo builds a temp repo with one commit per entry in commitFiles,
// writing/overwriting the named files with the given content. Modeled
// directly on run_purge_test.go's newPurgeRepo and safety/redact_test.go's
// own newRedactRepo, adapted to this package's fixture conventions.
func newRedactRepo(t *testing.T, commitFiles []map[string]string) (*gitio.Repo, string) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = gittest.Env(dir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	for i, files := range commitFiles {
		for name, content := range files {
			full := filepath.Join(dir, name)
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		run("add", ".")
		run("commit", "-q", "-m", "commit "+strconv.Itoa(i))
	}
	repo, err := gitio.Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return repo, dir
}

// redactRotationFiles is the shared 4-commit history used across most redact
// tests: c0 adds cfg.yaml carrying "token: sk-OLD"; c1 adds an unrelated
// other.txt (cfg.yaml rides along unmodified); c2 rotates ONLY the secret
// line to "token: sk-NEW" (other.txt untouched); c3 edits other.txt (the
// rotated line rides along). Mirrors safety/redact_test.go's
// redactRotationRepo exactly, so headless behavior can be cross-checked
// against the guarded-apply unit tests it already covers.
func redactRotationFiles() []map[string]string {
	return []map[string]string{
		{"cfg.yaml": "a\ntoken: sk-OLD\n"},
		{"other.txt": "unrelated-v1\n"},
		{"cfg.yaml": "a\ntoken: sk-NEW\n"},
		{"other.txt": "unrelated-v2\n"},
	}
}

func newRedactCtx(out *bytes.Buffer, repo *gitio.Repo) *RunContext {
	return &RunContext{Repo: repo, Cfg: config.Default(), Stdout: out, Stderr: out, Now: time.Unix(1000, 0)}
}

func TestRegistry_redactRowPresent(t *testing.T) {
	c, ok := Lookup("redact")
	if !ok {
		t.Fatal("redact row missing from the registry")
	}
	// redact is grouped with purge (WSRestructure): both are branch-scoped,
	// preview-first rewrites over repo-wide content — see the registry row's
	// own doc comment.
	if c.Workspace != WSRestructure {
		t.Errorf("Workspace = %v, want WSRestructure (grouped with purge)", c.Workspace)
	}
	if c.Selection != SelGlobal {
		t.Errorf("Selection = %v, want SelGlobal", c.Selection)
	}
	if !c.Mutating {
		t.Error("redact should be Mutating")
	}
	if c.Run == nil {
		t.Fatal("redact row has no Run wired up")
	}
	want := []string{"match", "in", "mode", "replace-pattern", "replace-with", "no-cascade", "execute", "force", "sign", "repo"}
	if len(c.Params) != len(want) {
		t.Fatalf("Params = %d entries, want %d: %+v", len(c.Params), len(want), c.Params)
	}
	for i, name := range want {
		if c.Params[i].Name != name {
			t.Errorf("Params[%d].Name = %q, want %q", i, c.Params[i].Name, name)
		}
	}
}

// TestParseRedact_replaceModeRequiresPattern is the AppliesIf-firing test:
// --mode=replace without --replace-pattern must be rejected at Parse time
// (Required + AppliesIf), a plain error that ExitCode maps to 1 (usage).
func TestParseRedact_replaceModeRequiresPattern(t *testing.T) {
	_, err := Parse(mustLookup(t, "redact"), []string{"--match=sk-OLD", "--mode=replace"})
	if err == nil {
		t.Fatal("expected an error: --mode=replace without --replace-pattern")
	}
	if ExitCode(err) != 1 {
		t.Errorf("ExitCode = %d, want 1 (usage error)", ExitCode(err))
	}
	if !strings.Contains(err.Error(), "replace-pattern") {
		t.Errorf("error should mention --replace-pattern, got: %v", err)
	}
}

// TestParseRedact_replaceModeWithPatternParsesOK is the positive control:
// supplying --replace-pattern satisfies the AppliesIf/Required pair.
func TestParseRedact_replaceModeWithPatternParsesOK(t *testing.T) {
	a, err := Parse(mustLookup(t, "redact"), []string{
		"--match=sk-OLD", "--mode=replace", "--replace-pattern=sk-.*", "--replace-with=REDACTED",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if a.String("mode") != "replace" || a.String("replace-pattern") != "sk-.*" {
		t.Errorf("unexpected parsed values: mode=%q pattern=%q", a.String("mode"), a.String("replace-pattern"))
	}
}

// TestParseRedact_matchRequired guards the --match Required flag on its own,
// independent of --mode.
func TestParseRedact_matchRequired(t *testing.T) {
	_, err := Parse(mustLookup(t, "redact"), nil)
	if err == nil || ExitCode(err) != 1 {
		t.Fatalf("expected a usage error for missing --match, got %v", err)
	}
}

// TestRunRedact_dryRunPrintsPreviewAndDroppedPrediction is the dry-run
// preview test: redacting "sk-OLD" on the rotation fixture, with cascade on
// by default, discovers BOTH the introduction (c0, cfg.yaml) AND its rotated
// descendant (c2's "token: sk-NEW", tracked forward by LineDescendants) —
// headless selects both, so c2 (which only ever rotated the secret line)
// collapses structurally and must be predicted DROPPED, exactly like
// safety.TestApplyRedact_dropsRotationOnlyCommitStructurally. Exit 0, ref
// unmoved.
func TestRunRedact_dryRunPrintsPreviewAndDroppedPrediction(t *testing.T) {
	repo, _ := newRedactRepo(t, redactRotationFiles())
	var out bytes.Buffer
	ctx := newRedactCtx(&out, repo)
	before, _ := repo.ReadState()

	a, err := Parse(mustLookup(t, "redact"), []string{"--match=sk-OLD"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := runRedact(ctx, a); err != nil {
		t.Fatalf("runRedact: %v", err)
	}

	s := out.String()
	if !strings.Contains(s, "cfg.yaml") {
		t.Errorf("expected the preview to mention cfg.yaml:\n%s", s)
	}
	if !strings.Contains(s, "token: sk-OLD") {
		t.Errorf("expected the preview to show the matched line:\n%s", s)
	}
	if !strings.Contains(s, "cascades to") || !strings.Contains(s, "token: sk-NEW") {
		t.Errorf("expected the preview to show the cascaded rotation:\n%s", s)
	}
	if !strings.Contains(s, "will be dropped") {
		t.Errorf("expected a drop marker for the rotation-only commit:\n%s", s)
	}
	if !strings.Contains(s, "dry run") {
		t.Errorf("expected the dry-run marker:\n%s", s)
	}
	if strings.Contains(s, HygieneWarning) {
		t.Errorf("a dry run must not print the hygiene warning:\n%s", s)
	}
	after, _ := repo.ReadState()
	if before.HeadHash != after.HeadHash {
		t.Error("dry run must not move the branch head")
	}
}

// TestRunRedact_executeHappyPath applies the same rotation-fixture redact and
// checks: the secret is gone from history, the hygiene warning and gc note
// are printed, and HEAD moved.
func TestRunRedact_executeHappyPath(t *testing.T) {
	repo, dir := newRedactRepo(t, redactRotationFiles())
	var out bytes.Buffer
	ctx := newRedactCtx(&out, repo)
	before, _ := repo.ReadState()

	a, err := Parse(mustLookup(t, "redact"), []string{"--match=sk-OLD", "--execute"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := runRedact(ctx, a); err != nil {
		t.Fatalf("runRedact execute: %v", err)
	}

	after, _ := repo.ReadState()
	if before.HeadHash == after.HeadHash {
		t.Error("execute should have moved the branch head")
	}

	cmd := exec.Command("git", "log", "-p", "main")
	cmd.Dir = dir
	cmd.Env = gittest.Env(dir)
	logOut, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v\n%s", err, logOut)
	}
	if strings.Contains(string(logOut), "sk-OLD") {
		t.Errorf("sk-OLD should no longer be reachable from main's history, got:\n%s", logOut)
	}

	s := out.String()
	if !strings.Contains(s, "backup") {
		t.Errorf("expected a backup mention:\n%s", s)
	}
	if !strings.Contains(s, "git gc") {
		t.Errorf("expected the gc hint line:\n%s", s)
	}
	if !strings.Contains(s, HygieneWarning) {
		t.Errorf("expected the hygiene warning on a successful execute:\n%s", s)
	}
	if !strings.Contains(s, "verified") {
		t.Errorf("expected the verification summary line:\n%s", s)
	}
}

func TestRunRedact_noOccurrencesIsUsageError(t *testing.T) {
	repo, _ := newRedactRepo(t, redactRotationFiles())
	var out bytes.Buffer
	ctx := newRedactCtx(&out, repo)
	before, _ := repo.ReadState()

	a, err := Parse(mustLookup(t, "redact"), []string{"--match=sk-NEVER-SEEN"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	err = runRedact(ctx, a)
	if err == nil || ExitCode(err) != 1 {
		t.Fatalf("expected a usage error (exit 1), got %v", err)
	}
	after, _ := repo.ReadState()
	if before.HeadHash != after.HeadHash {
		t.Error("a usage error must not move HEAD")
	}
}

// TestRunRedact_dirtyTreeExitsGuardBlocked: a dirty working tree must block
// --execute with exit 2, before any rewrite is attempted.
func TestRunRedact_dirtyTreeExitsGuardBlocked(t *testing.T) {
	repo, dir := newRedactRepo(t, redactRotationFiles())
	if err := os.WriteFile(filepath.Join(dir, "cfg.yaml"), []byte("dirty change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	ctx := newRedactCtx(&out, repo)
	before, _ := repo.ReadState()

	a, err := Parse(mustLookup(t, "redact"), []string{"--match=sk-OLD", "--execute"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	err = runRedact(ctx, a)
	if got := ExitCode(err); got != 2 {
		t.Fatalf("dirty-tree redact exit = %d, want 2 (err=%v)", got, err)
	}
	after, _ := repo.ReadState()
	if before.HeadHash != after.HeadHash {
		t.Error("guard-blocked redact must not move HEAD")
	}
}

// newRedactRepoWithMerge builds: c0 adds cfg.yaml carrying "token: sk-OLD"
// (this is the redact's base — the earliest commit whose tree the transform
// changes); a side branch is then merged in (a no-op merge, --no-ff, so it
// becomes a genuine first-parent-chain merge commit) BEFORE any further
// change to cfg.yaml — landing the merge squarely inside commits[base:], the
// rewritten suffix. This is the merge-in-range guard fixture.
func newRedactRepoWithMerge(t *testing.T) *gitio.Repo {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = gittest.Env(dir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-q", "-b", "main")
	write("cfg.yaml", "token: sk-OLD\n")
	run("add", ".")
	run("commit", "-q", "-m", "c0 adds the secret")
	run("checkout", "-q", "-b", "side")
	write("other.txt", "unrelated\n")
	run("add", ".")
	run("commit", "-q", "-m", "side adds other.txt")
	run("checkout", "-q", "main")
	run("merge", "--no-ff", "-q", "-m", "merge side", "side")

	repo, err := gitio.Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return repo
}

func TestRunRedact_mergeInRangeExitsGuardBlocked(t *testing.T) {
	repo := newRedactRepoWithMerge(t)
	var out bytes.Buffer
	ctx := newRedactCtx(&out, repo)
	before, _ := repo.ReadState()

	a, err := Parse(mustLookup(t, "redact"), []string{"--match=sk-OLD", "--execute"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	err = runRedact(ctx, a)
	if got := ExitCode(err); got != 2 {
		t.Fatalf("merge-in-range redact exit = %d, want 2 (err=%v)", got, err)
	}
	if !strings.Contains(err.Error(), "merge") {
		t.Errorf("error should mention merge, got: %v", err)
	}
	after, _ := repo.ReadState()
	if before.HeadHash != after.HeadHash {
		t.Error("guard-blocked redact must not move HEAD")
	}
}

// newRedactRepoWithPush is newRedactRepo plus a bare origin: the first
// pushedCount commits are pushed to it in a single push, leaving the rest
// local-only. Mirrors run_purge_test.go's newPurgeRepoWithPush.
func newRedactRepoWithPush(t *testing.T, commitFiles []map[string]string, pushedCount int) *gitio.Repo {
	t.Helper()
	dir := t.TempDir()
	origin := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = gittest.Env(dir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if out, err := exec.Command("git", "init", "-q", "--bare", origin).CombinedOutput(); err != nil {
		t.Fatalf("init bare: %v\n%s", err, out)
	}
	run("init", "-q", "-b", "main")
	run("remote", "add", "origin", origin)
	for i, files := range commitFiles {
		for name, content := range files {
			full := filepath.Join(dir, name)
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		run("add", ".")
		run("commit", "-q", "-m", "commit "+strconv.Itoa(i))
		if i+1 == pushedCount {
			run("push", "-q", "-u", "origin", "main")
		}
	}
	repo, err := gitio.Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return repo
}

// TestRunRedact_pushedRequiresForce: the redact base (c0, which introduces
// "token: sk-OLD") is pushed, so the whole rewritten suffix is pushed too —
// --execute without --force must be guard-blocked (exit 2).
func TestRunRedact_pushedRequiresForce(t *testing.T) {
	repo := newRedactRepoWithPush(t, redactRotationFiles(), 1)
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	if !commits[0].IsPushed {
		t.Fatal("setup: commit 0 (adds the secret) should be pushed")
	}

	var out bytes.Buffer
	ctx := newRedactCtx(&out, repo)
	before, _ := repo.ReadState()

	a, err := Parse(mustLookup(t, "redact"), []string{"--match=sk-OLD", "--execute"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	err = runRedact(ctx, a)
	if got := ExitCode(err); got != 2 {
		t.Fatalf("pushed redact without --force exit = %d, want 2 (err=%v)", got, err)
	}
	after, _ := repo.ReadState()
	if before.HeadHash != after.HeadHash {
		t.Error("guard-blocked redact must not move HEAD")
	}
}

// TestRunRedact_pushedWithForceSucceeds is the companion positive case:
// --force allows the same pushed-history redact through.
func TestRunRedact_pushedWithForceSucceeds(t *testing.T) {
	repo := newRedactRepoWithPush(t, redactRotationFiles(), 1)
	var out bytes.Buffer
	ctx := newRedactCtx(&out, repo)

	a, err := Parse(mustLookup(t, "redact"), []string{"--match=sk-OLD", "--execute", "--force"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := runRedact(ctx, a); err != nil {
		t.Fatalf("forced redact of pushed history should succeed: %v", err)
	}
}

// newRedactRepoWithSideHistoryPocket builds the "pocket" fixture: a side
// branch ADDS then DELETES the same literal (net zero by merge time, so the
// merge's own tree never carries it — mirroring run_purge_test.go's
// newPurgeRepoWithPocket), merges into main with no further change, and a
// LATER, unrelated main commit introduces the identical literal for real in
// cfg.yaml. This lands the redact's base at the LATER main commit (the
// pocket's own path never appears in any first-parent tree, so it never
// pulls base backward to the merge), keeping the merge out of
// commits[base:] entirely — so this exercises the SIDE-HISTORY ADVISORY
// path specifically, independent of the merge-in-range guard above.
func newRedactRepoWithSideHistoryPocket(t *testing.T) *gitio.Repo {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = gittest.Env(dir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-q", "-b", "main")
	write("keep.txt", "v1")
	run("add", ".")
	run("commit", "-q", "-m", "c0 keep only")
	run("checkout", "-q", "-b", "side")
	write("side.txt", "token: sk-SHARED\n")
	run("add", ".")
	run("commit", "-q", "-m", "SA side adds the shared secret")
	run("rm", "-q", "side.txt")
	run("commit", "-q", "-m", "SD side deletes it again (pocket)")
	run("checkout", "-q", "main")
	run("merge", "--no-ff", "-q", "-m", "merge side (pocket)", "side")
	write("cfg.yaml", "token: sk-SHARED\n")
	run("add", ".")
	run("commit", "-q", "-m", "c_target main adds the secret for real")

	repo, err := gitio.Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return repo
}

// TestRunRedact_sideHistoryAdvisoryPrintedNotGuardBlocked is the redact
// analog of purge's pocket regression test, adapted to redact's ADVISORY (not
// hard-blocking) contract for side-branch history: run_redact must NOT add
// its own guardErr for this condition (unlike purge's hard
// PathsInSideHistory block) — it always attempts the apply, after printing
// the advisory note up front.
//
// Because regit only ever rewrites the first-parent chain, the side branch's
// own commits (reachable from the rewritten head via the untouched merge's
// second parent) remain reachable no matter what the main-line rewrite does —
// so safety.Manager.ApplyRedact's own GLOBAL absence tier (armed here since
// headless always sets allSelected=true) independently re-derives the exact
// same signal from the rewritten head and correctly, DETERMINISTICALLY
// aborts, LATE, exactly as ApplyRedact's doc comment describes. That late
// abort is a SEPARATE, legitimate apply/verification failure (exit 3, pinned
// below) — this test's PRIMARY concern is run_redact's own contract: the
// advisory prints, and the exit code is NOT 2 — this package's own code
// never pre-emptively refuses the attempt.
func TestRunRedact_sideHistoryAdvisoryPrintedNotGuardBlocked(t *testing.T) {
	repo := newRedactRepoWithSideHistoryPocket(t)
	var out bytes.Buffer
	ctx := newRedactCtx(&out, repo)
	before, _ := repo.ReadState()

	a, err := Parse(mustLookup(t, "redact"), []string{"--match=sk-SHARED", "--execute"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	err = runRedact(ctx, a)

	s := out.String()
	if !strings.Contains(s, SideHistoryAdvisory) {
		t.Errorf("expected the side-history advisory note, got:\n%s", s)
	}
	if got := ExitCode(err); got == 2 {
		t.Fatalf("side-branch history must not be hard-blocked by run_redact's own guard (got exit 2): %v", err)
	}
	// The pocket's side branch stays fully intact and reachable (only the
	// merge's own SECOND parent carries it, and regit never rewrites that),
	// so safety.Manager.ApplyRedact's GLOBAL absence tier deterministically
	// aborts on this fixture, every run — pinned here rather than accepted
	// as one of two possible outcomes, so a future regression that silently
	// let this fixture "succeed" (which would mean the literal quietly
	// stayed reachable from a MOVED branch) fails this test outright.
	if err == nil {
		t.Fatal("the pocket fixture's side-branch history must deterministically abort via the " +
			"GLOBAL absence tier, not report success")
	}
	const wantSubstr = `"sk-SHARED" still reachable from the rewritten head's history`
	if !strings.Contains(err.Error(), wantSubstr) {
		t.Errorf("error should be the GLOBAL tier's wording %q, got: %v", wantSubstr, err)
	}
	after, _ := repo.ReadState()
	if before.HeadHash != after.HeadHash {
		t.Error("an aborted redact must not move HEAD")
	}
}

// TestRunRedact_sideHistoryAdvisoryAbsentWhenNoSideHistory is the negative
// control: the plain rotation fixture has no side-branch history at all, so
// the advisory must never appear.
func TestRunRedact_sideHistoryAdvisoryAbsentWhenNoSideHistory(t *testing.T) {
	repo, _ := newRedactRepo(t, redactRotationFiles())
	var out bytes.Buffer
	ctx := newRedactCtx(&out, repo)

	a, err := Parse(mustLookup(t, "redact"), []string{"--match=sk-OLD", "--execute"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := runRedact(ctx, a); err != nil {
		t.Fatalf("runRedact execute: %v", err)
	}
	if strings.Contains(out.String(), SideHistoryAdvisory) {
		t.Errorf("no side-branch history exists; the advisory must not print:\n%s", out.String())
	}
}

// TestBuildRedactPreview_singleTargetRewritesCarriersThenCarriesForward
// unit-tests the preview builder against the rotation fixture with ONLY the
// introduction line targeted (e.g. --no-cascade: the operator vetoed the
// rotated descendant). cfg.yaml's ORIGINAL (unmodified) blob carries "token:
// sk-OLD" forward from c0 through c1 unchanged — RedactBlobsInTree strips it
// from BOTH of their trees, so c1 is predicted REWRITTEN even though c1's own
// diff never touched cfg.yaml (the same "reparented" phenomenon
// BuildPurgePreview's doc comment describes for purge). Once c2 rotates
// cfg.yaml to "token: sk-NEW", the (untargeted) line is gone from every later
// tree, so c2 and c3 are no-ops for this transform and are predicted carried
// forward, unchanged.
func TestBuildRedactPreview_singleTargetRewritesCarriersThenCarriesForward(t *testing.T) {
	repo, _ := newRedactRepo(t, redactRotationFiles())
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	spec := model.RedactSpec{
		Match: "sk-OLD", Mode: model.RedactRemove,
		Targets: map[string][]string{"cfg.yaml": {"token: sk-OLD"}},
	}

	pv, err := BuildRedactPreview(repo, commits, spec)
	if err != nil {
		t.Fatal(err)
	}
	if pv.Base != 0 {
		t.Fatalf("Base = %d, want 0", pv.Base)
	}
	if !pv.Rewritten[commits[0].Hash] {
		t.Errorf("c0 (introduces sk-OLD) should be predicted rewritten")
	}
	if !pv.Rewritten[commits[1].Hash] {
		t.Errorf("c1 (carries the un-rotated sk-OLD line forward) should be predicted rewritten")
	}
	if pv.Dropped[commits[2].Hash] || pv.Rewritten[commits[2].Hash] {
		t.Errorf("c2 (already rotated away from sk-OLD) should be carried forward, untouched")
	}
	if pv.Dropped[commits[3].Hash] || pv.Rewritten[commits[3].Hash] {
		t.Errorf("c3 (unrelated other.txt edit, no sk-OLD left to strip) should be carried forward, untouched")
	}
}

// TestBuildRedactPreview_cascadeSelectedDropsRotationCommit is the
// cascade-selected shape (both the intro AND the rotated descendant
// targeted, exactly what headless selects by default): cfg.yaml's content
// needs stripping at EVERY commit that still carries either the old or the
// rotated line, so c0, c1, and c3 are all predicted rewritten — and c2, which
// only ever rotated the secret line (its post-transform tree collapses to its
// rewritten parent's), is predicted dropped, matching
// safety.TestApplyRedact_dropsRotationOnlyCommitStructurally exactly.
func TestBuildRedactPreview_cascadeSelectedDropsRotationCommit(t *testing.T) {
	repo, _ := newRedactRepo(t, redactRotationFiles())
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	spec := model.RedactSpec{
		Match: "sk-OLD", Mode: model.RedactRemove,
		Targets: map[string][]string{"cfg.yaml": {"token: sk-OLD", "token: sk-NEW"}},
	}

	pv, err := BuildRedactPreview(repo, commits, spec)
	if err != nil {
		t.Fatal(err)
	}
	if pv.Base != 0 {
		t.Fatalf("Base = %d, want 0", pv.Base)
	}
	if !pv.Rewritten[commits[0].Hash] {
		t.Errorf("c0 (introduces sk-OLD) should be predicted rewritten")
	}
	if !pv.Rewritten[commits[1].Hash] {
		t.Errorf("c1 (carries sk-OLD forward) should be predicted rewritten")
	}
	if !pv.Dropped[commits[2].Hash] {
		t.Errorf("c2 (rotation-only commit) should be predicted dropped")
	}
	if !pv.Rewritten[commits[3].Hash] {
		t.Errorf("c3 (carries the rotated sk-NEW line forward) should be predicted rewritten")
	}
}
