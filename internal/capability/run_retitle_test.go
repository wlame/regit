package capability

import (
	"bytes"
	"testing"
	"time"

	"github.com/wlame/regit/internal/config"
)

func TestRetitleDryRunRewritesSubjects(t *testing.T) {
	_, repo := newTestRepo(t, 3) // subjects: "commit A/B/C"
	var out bytes.Buffer
	ctx := &RunContext{Repo: repo, Cfg: config.Default(), Stdout: &out, Stderr: &out, Now: time.Unix(1000, 0)}
	a, _ := Parse(mustLookup(t, "retitle"), []string{`--match=^commit `, `--replace=chore: `})
	before, _ := repo.ReadState()
	if err := runRetitle(ctx, a); err != nil {
		t.Fatalf("runRetitle: %v", err)
	}
	after, _ := repo.ReadState()
	if before.HeadHash != after.HeadHash {
		t.Error("dry-run retitle moved HEAD")
	}
	if !bytes.Contains(out.Bytes(), []byte("chore: A")) {
		t.Errorf("expected rewritten subject:\n%s", out.String())
	}
}
