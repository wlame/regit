package capability

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/wlame/regit/internal/config"
	"github.com/wlame/regit/internal/llm"
	"github.com/wlame/regit/internal/reword"
)

// stubBackend returns a fixed structured payload so reword.Engine.Generate yields
// one variant without any network call. The Generate signature must match
// llm.Backend.Generate exactly (schema []byte, not any).
type stubBackend struct{ payload string }

func (s stubBackend) Generate(_ context.Context, _, _ string, _ []byte, _ func(string) error) (string, error) {
	return s.payload, nil
}

func (s stubBackend) Name() string { return "stub" }

func TestRewordDryRunGeneratesOneVariantNoApply(t *testing.T) {
	_, repo := newTestRepo(t, 2)
	var out bytes.Buffer
	ctx := &RunContext{
		Repo: repo, Cfg: config.Default(), Stdout: &out, Stderr: &out,
		Now: time.Unix(1000, 0),
		// Inject a stub backend matching reword's schema (one variant).
		Backend: stubBackend{payload: `{"variants":[{"title":"New title.","body":""}]}`},
	}
	a := newParsedArgs(map[string]any{"no-cache": true}, []string{"HEAD"})
	before, _ := repo.ReadState()
	if err := runReword(ctx, a); err != nil {
		t.Fatalf("runReword: %v", err)
	}
	after, _ := repo.ReadState()
	if before.HeadHash != after.HeadHash {
		t.Error("dry-run reword moved HEAD")
	}
	if !bytes.Contains(out.Bytes(), []byte("New title.")) {
		t.Errorf("expected the proposed title in output:\n%s", out.String())
	}
	_ = llm.Backend(stubBackend{}) // ensure stub satisfies the interface
	_ = reword.DefaultSelection()
}
