package capability

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wlame/regit/internal/config"
	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/gittest"
	"github.com/wlame/regit/internal/model"
)

// TestRedateEvenSpreadDryRun proves a dry-run preview both leaves HEAD alone
// and actually proposes new dates. Now must be farFutureNow — a clock fixed
// before the requested --start/--end range (see farFutureNow's doc) caps
// every window before the range even begins, so the strategy finds no
// overlap and stages zero edits; previewDates still prints an old -> new
// line for every active commit regardless of whether anything changed, so a
// bare Contains(out, "->") check passes vacuously even with no real edits.
// parseDateChanges distinguishes a printed line whose old and new dates
// actually differ from one where they don't, closing that hole.
func TestRedateEvenSpreadDryRun(t *testing.T) {
	_, repo := newTestRepo(t, 4)
	var out bytes.Buffer
	ctx := &RunContext{Repo: repo, Cfg: config.Default(), Stdout: &out, Stderr: &out, Now: farFutureNow}
	a, err := Parse(mustLookup(t, "redate"), []string{
		"--strategy=Even-Spread", "--time=Working-hours",
		"--start=2026-01-01", "--end=2026-03-01",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	before, _ := repo.ReadState()
	if err := runRedate(ctx, a); err != nil {
		t.Fatalf("runRedate: %v", err)
	}
	after, _ := repo.ReadState()
	if before.HeadHash != after.HeadHash {
		t.Error("dry-run redate moved HEAD")
	}
	changes := parseDateChanges(t, out.String())
	if len(changes) == 0 {
		t.Fatalf("expected a date plan:\n%s", out.String())
	}
	anyChanged := false
	for _, changed := range changes {
		if changed {
			anyChanged = true
			break
		}
	}
	if !anyChanged {
		t.Errorf("expected at least one commit's date to actually change:\n%s", out.String())
	}
}

func TestRedateManualPinRequiresPin(t *testing.T) {
	if _, err := Parse(mustLookup(t, "redate"), []string{"--strategy=Manual-Pin"}); err == nil {
		t.Error("Manual-Pin without --pin should error")
	}
}

// TestRedate_configFixedTimeWithoutTimeOfDayErrors proves the silent-midnight bug
// is closed: a config default of Fixed-time with no --time-fixed must fail fast with
// an actionable message, not date every commit at 00:00.
func TestRedate_configFixedTimeWithoutTimeOfDayErrors(t *testing.T) {
	_, repo := newTestRepo(t, 3)
	cfg := config.Default()
	cfg.DefaultTimeModel = "Fixed time"
	var out bytes.Buffer
	ctx := &RunContext{Repo: repo, Cfg: cfg, Stdout: &out, Stderr: &out, Now: time.Unix(1000, 0)}
	a := newParsedArgs(map[string]any{"strategy": "Even-Spread", "start": "2026-01-01", "end": "2026-03-01"}, nil)
	err := runRedate(ctx, a)
	if err == nil {
		t.Fatal("Fixed-time from config without --time-fixed must error")
	}
	if !strings.Contains(err.Error(), "time-fixed") {
		t.Errorf("error should mention --time-fixed, got: %v", err)
	}
}

// TestRedateParams_jitterZeroCannotOverrideConfig documents that --jitter-min=0 is
// treated as absent and cannot zero a configured jitter (a.Int 0 == unset; a zero
// jitter is a no-op anyway).
func TestRedateParams_jitterZeroDisablesJitter(t *testing.T) {
	cfg := config.Default()
	cfg.JitterMinutes = 15
	ctx := &RunContext{Cfg: cfg, Now: time.Unix(1000, 0)}
	// An explicit --jitter-min=0 disables jitter, overriding the config default.
	p, err := redateParams(ctx, newParsedArgs(map[string]any{"jitter-min": 0}, nil))
	if err != nil {
		t.Fatalf("redateParams: %v", err)
	}
	if p.JitterMin != 0 {
		t.Errorf("JitterMin = %d, want 0 (--jitter-min=0 disables jitter)", p.JitterMin)
	}
	// Omitting --jitter-min still falls back to the config default.
	p2, err := redateParams(ctx, newParsedArgs(map[string]any{}, nil))
	if err != nil {
		t.Fatalf("redateParams: %v", err)
	}
	if p2.JitterMin != 15 {
		t.Errorf("omitted jitter-min: JitterMin = %d, want config default 15", p2.JitterMin)
	}
}

// TestRedateParams_invalidLunchClockErrors verifies a bad lunch clock is
// rejected exactly like a bad workday clock — the old behavior silently
// disabled the gap on a typo.
func TestRedateParams_invalidLunchClockErrors(t *testing.T) {
	ctx := &RunContext{Cfg: config.Default(), Now: time.Unix(1000, 0)}
	if _, err := redateParams(ctx, newParsedArgs(map[string]any{"lunch-start": "99:99"}, nil)); err == nil {
		t.Fatal("a bad lunch clock should be a parse error, like workday clocks")
	}
	if _, err := redateParams(ctx, newParsedArgs(map[string]any{"lunch-end": "nope"}, nil)); err == nil {
		t.Fatal("a bad lunch-end clock should be a parse error")
	}
	// Valid clocks still parse.
	p, err := redateParams(ctx, newParsedArgs(map[string]any{"lunch-start": "12:30", "lunch-end": "13:00"}, nil))
	if err != nil {
		t.Fatalf("valid lunch clocks should parse: %v", err)
	}
	if p.LunchStart != 12*60+30 || p.LunchEnd != 13*60 {
		t.Errorf("lunch window = %d–%d, want 750–780", p.LunchStart, p.LunchEnd)
	}
}

func TestRedateExecuteEmitsVerificationLine(t *testing.T) {
	_, repo := newTestRepo(t, 4)
	var out bytes.Buffer
	// Now must sit after the "--end=2026-03-01" below; see farFutureNow's doc.
	ctx := &RunContext{
		Repo: repo, Cfg: config.Default(), Stdout: &out, Stderr: &out, Now: farFutureNow,
	}
	a, err := Parse(mustLookup(t, "redate"), []string{
		"--strategy=Even-Spread", "--time=Working-hours",
		"--start=2026-01-01", "--end=2026-03-01", "--execute",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := runRedate(ctx, a); err != nil {
		t.Fatalf("runRedate: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("verified")) || !bytes.Contains(out.Bytes(), []byte("0 paths changed")) {
		t.Errorf("expected a verification line in output:\n%s", out.String())
	}
}

// redateTestRepoWithMerge builds a throwaway repo whose main-branch history
// (the line ReadCommits walks via --first-parent) contains a real merge
// commit: a -> b on main, with "feature" diverging from a and merged back into
// main with --no-ff so the merge keeps both parents. ReadCommits then returns
// [a, b, merge] with merge.IsMerge == true (2 parents), giving runRedate's
// merge gate a genuine merge commit inside plan.Active() to trip on.
func redateTestRepoWithMerge(t *testing.T) (string, *gitio.Repo) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = gittest.Env(dir,
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	commit := func(name string) {
		f := filepath.Join(dir, name+".txt")
		if err := os.WriteFile(f, []byte(name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		run("add", ".")
		run("commit", "-q", "-m", "commit "+name)
	}
	run("init", "-q")
	run("symbolic-ref", "HEAD", "refs/heads/main")
	run("config", "commit.gpgsign", "false")
	commit("a")
	run("branch", "feature")
	commit("b") // main: a -> b
	run("switch", "feature")
	commit("c") // feature: a -> c
	run("switch", "main")
	run("merge", "--no-ff", "--no-edit", "feature") // main: a -> b -> merge(parents b, c)

	repo, err := gitio.Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return dir, repo
}

// parseRedate parses redate flags through the real registry (Lookup + Parse),
// exactly like every other redate test, so the test never risks drifting from
// how the CLI itself parses --strategy/--execute/etc.
func parseRedate(t *testing.T, args ...string) ParsedArgs {
	t.Helper()
	a, err := Parse(mustLookup(t, "redate"), args)
	if err != nil {
		t.Fatalf("parse redate args %v: %v", args, err)
	}
	return a
}

// TestRunRedate_refusesMergeWithoutAllowMerges proves the merge gate: applying
// a redate whose active range contains a merge commit is refused with the
// guard exit code (2) unless --allow-merges is passed, since the rewrite
// leaves the merge's non-first-parent side branch on its original dates
// (Replay only remaps the first parent — see gitio.Repo.Replay).
func TestRunRedate_refusesMergeWithoutAllowMerges(t *testing.T) {
	_, repo := redateTestRepoWithMerge(t)
	ctx := &RunContext{Repo: repo, Cfg: config.Default(), Stdout: io.Discard, Stderr: io.Discard, Now: time.Unix(1_700_000_000, 0)}

	err := runRedate(ctx, parseRedate(t, "--strategy=Even-Spread", "--start=2026-01-01", "--end=2026-02-01", "--execute"))
	if ExitCode(err) != 2 {
		t.Fatalf("expected guard exit 2 for a merge without --allow-merges, got err=%v code=%d", err, ExitCode(err))
	}

	// With --allow-merges the gate passes (may still succeed or hit a later step).
	err = runRedate(ctx, parseRedate(t, "--strategy=Even-Spread", "--start=2026-01-01", "--end=2026-02-01", "--execute", "--allow-merges"))
	if ExitCode(err) == 2 {
		t.Fatalf("with --allow-merges the merge gate must not block; got %v", err)
	}
}

// TestRedatePreview_warnsAboutMerges proves the dry-run preview surfaces the
// same merge warning, so a user sees it before ever touching --execute.
func TestRedatePreview_warnsAboutMerges(t *testing.T) {
	_, repo := redateTestRepoWithMerge(t)
	var out bytes.Buffer
	ctx := &RunContext{Repo: repo, Cfg: config.Default(), Stdout: &out, Stderr: &out, Now: time.Unix(1_700_000_000, 0)}

	if err := runRedate(ctx, parseRedate(t, "--strategy=Even-Spread", "--start=2026-01-01", "--end=2026-02-01")); err != nil {
		t.Fatalf("runRedate dry-run: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("merge commit")) {
		t.Errorf("expected dry-run preview to warn about merge commits:\n%s", out.String())
	}
}

// TestRedateParams_weekdaysFallBackToConfig verifies that a headless redate
// without --weekdays uses the configured weekday set — the same default the TUI
// seeds its strategies with — instead of silently distributing over all seven
// days. An explicit flag still wins, and a config mask with no valid names
// degrades to all days.
func TestRedateParams_weekdaysFallBackToConfig(t *testing.T) {
	cfg := config.Default()
	cfg.Weekdays = []string{"mon", "wed"}
	ctx := &RunContext{Cfg: cfg, Now: time.Unix(1000, 0)}

	p, err := redateParams(ctx, newParsedArgs(map[string]any{}, nil))
	if err != nil {
		t.Fatalf("redateParams: %v", err)
	}
	want := [7]bool{}
	want[int(time.Monday)], want[int(time.Wednesday)] = true, true
	if [7]bool(p.WeekdayMask) != want {
		t.Errorf("WeekdayMask = %v, want config days mon+wed", p.WeekdayMask)
	}

	// An explicit --weekdays flag overrides the config.
	p2, err := redateParams(ctx, newParsedArgs(map[string]any{"weekdays": []string{"sat"}}, nil))
	if err != nil {
		t.Fatalf("redateParams: %v", err)
	}
	if !p2.WeekdayMask[int(time.Saturday)] || p2.WeekdayMask[int(time.Monday)] {
		t.Errorf("--weekdays flag should override config, got %v", p2.WeekdayMask)
	}

	// A config mask with no valid day names degrades to all days, not none.
	cfg.Weekdays = []string{"noday"}
	ctx = &RunContext{Cfg: cfg, Now: time.Unix(1000, 0)}
	p3, err := redateParams(ctx, newParsedArgs(map[string]any{}, nil))
	if err != nil {
		t.Fatalf("redateParams: %v", err)
	}
	if !p3.WeekdayMask.Any() || !p3.WeekdayMask[int(time.Sunday)] {
		t.Errorf("invalid config weekdays should degrade to all days, got %v", p3.WeekdayMask)
	}
}

// TestRedateParams_rejectsNonPositiveScale verifies --scale=0 or a negative
// scale is refused up front instead of collapsing or inverting the span.
func TestRedateParams_rejectsNonPositiveScale(t *testing.T) {
	ctx := &RunContext{Cfg: config.Default(), Now: time.Unix(1000, 0)}
	for _, bad := range []float64{0, -0.5} {
		if _, err := redateParams(ctx, newParsedArgs(map[string]any{"scale": bad}, nil)); err == nil {
			t.Errorf("scale=%g should be rejected", bad)
		}
	}
	if _, err := redateParams(ctx, newParsedArgs(map[string]any{"scale": 0.5}, nil)); err != nil {
		t.Errorf("scale=0.5 should be accepted: %v", err)
	}
}

// TestRedateParams_rejectsInvertedWorkdayFlags verifies the per-invocation
// flags cannot re-invert the workday window config.Validate already guards.
func TestRedateParams_rejectsInvertedWorkdayFlags(t *testing.T) {
	ctx := &RunContext{Cfg: config.Default(), Now: time.Unix(1000, 0)}
	_, err := redateParams(ctx, newParsedArgs(map[string]any{
		"workday-start": "18:00", "workday-end": "09:00",
	}, nil))
	if err == nil {
		t.Fatal("inverted workday window flags should be rejected")
	}
}

// TestRedateParams_invertedRangeIsAUsageError pins the parse-time check. The
// WORKDAY window already errors cleanly when inverted; the calendar range did
// not, so `--start=2026-03-01 --end=2026-01-01 --execute` built an empty plan
// and surfaced as exit 3 ("nothing to apply") — a runtime error for what is
// plainly bad input.
func TestRedateParams_invertedRangeIsAUsageError(t *testing.T) {
	args := newParsedArgs(map[string]any{
		"start": time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		"end":   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}, nil)

	_, err := redateParams(&RunContext{Cfg: config.Default(), Now: time.Now()}, args)
	if err == nil {
		t.Fatal("redateParams accepted an inverted range")
	}
	if got := ExitCode(err); got != 1 {
		t.Errorf("ExitCode = %d, want 1 (usage error)", got)
	}
	if !strings.Contains(err.Error(), "--end") {
		t.Errorf("error %q does not name the offending flag", err)
	}
}

// redateScopedTestRepo builds a 4-commit repo (c1..c4) with author dates one
// calendar day apart — 2026-01-01 .. 2026-01-04, noon UTC — so a segment
// spanning more than one commit also spans more than one calendar day. This
// is required to exercise --commits scoping and window/capacity validation
// deterministically: newTestRepo's commits all land within the same
// wall-clock instant the test runs in, which would collapse every segment's
// window to a single day and make an "outside the window" or "two picks
// inside one window" scenario impossible to construct reliably.
func redateScopedTestRepo(t *testing.T) (*gitio.Repo, []model.Commit) {
	t.Helper()
	dir := t.TempDir()
	run := func(env []string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = gittest.Env(dir, env...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run(nil, "init", "-q")
	run(nil, "symbolic-ref", "HEAD", "refs/heads/main")
	run(nil, "config", "commit.gpgsign", "false")
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 4; i++ {
		f := filepath.Join(dir, "f.txt")
		if err := os.WriteFile(f, []byte{byte('a' + i)}, 0o644); err != nil {
			t.Fatal(err)
		}
		run(nil, "add", "f.txt")
		d := base.AddDate(0, 0, i).Format(time.RFC3339)
		run([]string{"GIT_AUTHOR_DATE=" + d, "GIT_COMMITTER_DATE=" + d},
			"commit", "-q", "-m", "commit "+string(rune('A'+i)))
	}
	repo, err := gitio.Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatalf("read commits: %v", err)
	}
	return repo, commits
}

// parseDateChanges scans a redate preview's "Date plan" lines (see
// previewDates) and reports, per printed short hash, whether the old and new
// dates on that line actually differ. previewDates always prints an
// "old -> new" line for every active commit, changed or not, so a plain
// substring check for "->" cannot distinguish a real proposal from a no-op.
func parseDateChanges(t *testing.T, output string) map[string]bool {
	t.Helper()
	changed := map[string]bool{}
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		// short-hash  YYYY-MM-DD HH:MM -> YYYY-MM-DD HH:MM
		if len(fields) != 6 || fields[3] != "->" {
			continue
		}
		before := fields[1] + " " + fields[2]
		after := fields[4] + " " + fields[5]
		changed[fields[0]] = before != after
	}
	return changed
}

// TestRedate_CommitsFlagScopesTargets proves --commits narrows the
// strategy's targets to the given subset: only the named commit gets a
// proposed date, later commits show their date unchanged, and commits older
// than the first target are frozen and left out of the plan.
func TestRedate_CommitsFlagScopesTargets(t *testing.T) {
	repo, commits := redateScopedTestRepo(t)
	c2, c3 := commits[1], commits[2]
	loc := config.Default().Location()
	compressDay := c3.OrigAuthorDate.In(loc).Format("2006-01-02")

	var out bytes.Buffer
	ctx := &RunContext{Repo: repo, Cfg: config.Default(), Stdout: &out, Stderr: &out, Now: farFutureNow}
	a := parseRedate(t, "--strategy=Compress", "--compress-day="+compressDay, "--commits="+c2.Hash)
	if err := runRedate(ctx, a); err != nil {
		t.Fatalf("runRedate: %v", err)
	}

	changes := parseDateChanges(t, out.String())
	for i, c := range commits {
		got, ok := changes[Short(c.Hash)]
		if i < 1 {
			// Older than the first target: frozen, so never listed or rewritten.
			if ok {
				t.Errorf("commit %s precedes the first target and should be frozen:\n%s", Short(c.Hash), out.String())
			}
			continue
		}
		if !ok {
			t.Fatalf("no date-plan line found for %s:\n%s", Short(c.Hash), out.String())
		}
		want := c.Hash == c2.Hash
		if got != want {
			t.Errorf("commit %s: date changed=%v, want %v", Short(c.Hash), got, want)
		}
	}
}

// TestRedate_PickOutsideWindowsFails proves a --pick day outside every
// available segment window is a hard usage error naming the segment and its
// window bounds: headless has no interactive eviction to fall back on, so
// the caller needs the bounds printed back to correct the flag.
func TestRedate_PickOutsideWindowsFails(t *testing.T) {
	repo, commits := redateScopedTestRepo(t)
	c1, c2, c3 := commits[0], commits[1], commits[2]
	loc := config.Default().Location()
	c1Day := c1.OrigAuthorDate.In(loc).Format("2006-01-02")
	c3Day := c3.OrigAuthorDate.In(loc).Format("2006-01-02")
	outside := c3.OrigAuthorDate.In(loc).AddDate(0, 6, 0).Format("2006-01-02")

	ctx := &RunContext{
		Repo: repo, Cfg: config.Default(), Stdout: io.Discard, Stderr: io.Discard, Now: farFutureNow,
	}
	err := runRedate(ctx,
		parseRedate(t, "--strategy=Picked-Days", "--commits="+c2.Hash, "--pick="+outside))
	if ExitCode(err) != 1 {
		t.Fatalf("expected a usage error (exit 1), got err=%v code=%d", err, ExitCode(err))
	}
	if !strings.Contains(err.Error(), "segment 1") {
		t.Errorf("error should name the segment, got: %v", err)
	}
	if !strings.Contains(err.Error(), c1Day) || !strings.Contains(err.Error(), c3Day) {
		t.Errorf("error should include the window bounds %s..%s, got: %v", c1Day, c3Day, err)
	}
}

// TestRedate_PickOverCapacityFails proves two --pick days that both land
// inside a single-commit segment's window are refused: the segment (capacity
// 1, since --commits selects only c2) can only absorb one of them.
func TestRedate_PickOverCapacityFails(t *testing.T) {
	repo, commits := redateScopedTestRepo(t)
	c1, c2 := commits[0], commits[1]
	loc := config.Default().Location()
	day1 := c1.OrigAuthorDate.In(loc).Format("2006-01-02") // segment's floor day
	day2 := c2.OrigAuthorDate.In(loc).Format("2006-01-02") // segment's own day

	ctx := &RunContext{
		Repo: repo, Cfg: config.Default(), Stdout: io.Discard, Stderr: io.Discard, Now: farFutureNow,
	}
	args := "--pick=" + day1 + "," + day2
	err := runRedate(ctx, parseRedate(t, "--strategy=Picked-Days", "--commits="+c2.Hash, args))
	if ExitCode(err) != 1 {
		t.Fatalf("expected a usage error (exit 1), got err=%v code=%d", err, ExitCode(err))
	}
	if !strings.Contains(err.Error(), "segment 1") {
		t.Errorf("error should name the segment, got: %v", err)
	}
	if !strings.Contains(err.Error(), "1 commit(s)") {
		t.Errorf("error should name the segment's capacity, got: %v", err)
	}
}

// TestRedate_FrozenCommitInCommitsFails proves --commits cannot reach behind
// --from: naming a frozen commit is a usage error, not a silent no-op.
func TestRedate_FrozenCommitInCommitsFails(t *testing.T) {
	repo, commits := redateScopedTestRepo(t)
	c1, c3 := commits[0], commits[2]

	ctx := &RunContext{
		Repo: repo, Cfg: config.Default(), Stdout: io.Discard, Stderr: io.Discard, Now: farFutureNow,
	}
	err := runRedate(ctx, parseRedate(t, "--strategy=Even-Spread",
		"--start=2026-01-01", "--end=2026-03-01", "--from="+c3.Hash, "--commits="+c1.Hash))
	if ExitCode(err) != 1 {
		t.Fatalf("expected a usage error (exit 1), got err=%v code=%d", err, ExitCode(err))
	}
	if !strings.Contains(err.Error(), "frozen") {
		t.Errorf("error should mention \"frozen\", got: %v", err)
	}
}
