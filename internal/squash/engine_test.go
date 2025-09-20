// internal/squash/engine_test.go
package squash

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/wlame/regit/internal/model"
)

// stubBackend returns a fixed response and records the last prompts it saw.
type stubBackend struct {
	resp       string
	lastSystem string
	lastUser   string
	calls      int
}

func (s *stubBackend) Generate(ctx context.Context, system, user string, schema []byte, validate func(string) error) (string, error) {
	s.calls++
	s.lastSystem, s.lastUser = system, user
	if err := validate(s.resp); err != nil {
		return "", err
	}
	return s.resp, nil
}
func (s *stubBackend) Name() string { return "stub:test" }

func TestSummarize_usesBackendAndCaches(t *testing.T) {
	dir := t.TempDir()
	backend := &stubBackend{resp: `{"subject":"Add user model and validation","body":"Combined."}`}
	eng := NewEngine(nil, backend, NewCache(dir)) // repo unused for messages-only

	ch := model.SquashChain{Members: []model.Commit{
		{Hash: "a", Subject: "Add user model"},
		{Hash: "b", Subject: "Add validation"},
	}}
	got, err := eng.Summarize(context.Background(), ch, Options{Now: time.Unix(0, 0)})
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if got.Subject != "Add user model and validation" {
		t.Errorf("subject = %q", got.Subject)
	}
	// Second call must hit the cache (no extra backend call).
	if _, err := eng.Summarize(context.Background(), ch, Options{Now: time.Unix(0, 0)}); err != nil {
		t.Fatal(err)
	}
	if backend.calls != 1 {
		t.Errorf("backend called %d times, want 1 (second served from cache)", backend.calls)
	}
}

func TestSummarize_noCacheAlwaysCallsBackend(t *testing.T) {
	dir := t.TempDir()
	backend := &stubBackend{resp: `{"subject":"X"}`}
	eng := NewEngine(nil, backend, NewCache(dir))
	ch := model.SquashChain{Members: []model.Commit{{Hash: "a", Subject: "p"}, {Hash: "b", Subject: "q"}}}
	eng.Summarize(context.Background(), ch, Options{NoCache: true, Now: time.Unix(0, 0)})
	eng.Summarize(context.Background(), ch, Options{NoCache: true, Now: time.Unix(0, 0)})
	if backend.calls != 2 {
		t.Errorf("with NoCache, backend should be called every time, got %d", backend.calls)
	}
}

// TestSummarize_promptAppendsGuidanceToUserMessage verifies that a non-empty
// Prompt option is appended to the user message sent to the backend, so the
// model can tailor the combined message to the user's request.
func TestSummarize_promptAppendsGuidanceToUserMessage(t *testing.T) {
	dir := t.TempDir()
	backend := &stubBackend{resp: `{"subject":"Add feature","body":""}`}
	eng := NewEngine(nil, backend, NewCache(dir))

	ch := model.SquashChain{Members: []model.Commit{
		{Hash: "c", Subject: "wip"},
		{Hash: "d", Subject: "done"},
	}}
	_, err := eng.Summarize(context.Background(), ch, Options{
		NoCache: true,
		Prompt:  "keep it concise",
		Now:     time.Unix(0, 0),
	})
	if err != nil {
		t.Fatalf("Summarize with Prompt: %v", err)
	}
	if !strings.Contains(backend.lastUser, "keep it concise") {
		t.Errorf("user message should contain the Prompt guidance; got:\n%s", backend.lastUser)
	}
}
