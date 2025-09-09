package capability

import (
	"bytes"
	"testing"
	"time"

	"github.com/wlame/regit/internal/config"
)

func TestRedateEvenSpreadDryRun(t *testing.T) {
	_, repo := newTestRepo(t, 4)
	var out bytes.Buffer
	ctx := &RunContext{Repo: repo, Cfg: config.Default(), Stdout: &out, Stderr: &out, Now: time.Unix(1_700_000_000, 0)}
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
	if !bytes.Contains(out.Bytes(), []byte("->")) {
		t.Errorf("expected a date plan:\n%s", out.String())
	}
}

func TestRedateManualPinRequiresPin(t *testing.T) {
	if _, err := Parse(mustLookup(t, "redate"), []string{"--strategy=Manual-Pin"}); err == nil {
		t.Error("Manual-Pin without --pin should error")
	}
}

// TestRedateParams_jitterZeroCannotOverrideConfig documents that --jitter-min=0 is
// treated as absent and cannot zero a configured jitter (a.Int 0 == unset; a zero
// jitter is a no-op anyway).
func TestRedateParams_jitterZeroCannotOverrideConfig(t *testing.T) {
	cfg := config.Default()
	cfg.JitterMinutes = 15
	ctx := &RunContext{Cfg: cfg, Now: time.Unix(1000, 0)}
	p, err := redateParams(ctx, newParsedArgs(map[string]any{"jitter-min": 0}, nil))
	if err != nil {
		t.Fatalf("redateParams: %v", err)
	}
	if p.JitterMin != 15 {
		t.Errorf("JitterMin = %d, want 15 (--jitter-min=0 must not override config)", p.JitterMin)
	}
}

// TestRedateParams_invalidLunchClockIsSilentlyDisabled documents that an invalid
// lunch clock is swallowed (the gap is disabled), unlike workday-start/end which
// propagate the parse error.
func TestRedateParams_invalidLunchClockIsSilentlyDisabled(t *testing.T) {
	ctx := &RunContext{Cfg: config.Default(), Now: time.Unix(1000, 0)}
	p, err := redateParams(ctx, newParsedArgs(map[string]any{"lunch-start": "99:99"}, nil))
	if err != nil {
		t.Fatalf("redateParams should not error on a bad lunch clock: %v", err)
	}
	if p.LunchStart != 0 {
		t.Errorf("LunchStart = %d, want 0 (invalid lunch clock should disable the gap)", p.LunchStart)
	}
}
