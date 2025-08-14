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
