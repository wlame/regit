// internal/llm/backend.go
package llm

import (
	"context"

	"github.com/wlame/regit/internal/model"
)

// FewShot is one worked tangled-diff → grouping exemplar used for CoT prompting.
type FewShot struct {
	CommitMessage string
	HunkSummary   string // compact "H1: a.go +3/-1" style listing
	GroupingJSON  string // the ideal SplitPlan JSON for that example
}

// SplitRequest is everything a backend needs to produce a SplitProposal.
type SplitRequest struct {
	CommitMessage string
	Hunks         []model.Hunk
	Readme        string
	FileStates    map[string]string // reserved for future context (e.g. file mode, binary flag); not yet rendered into prompts
	MaxCommits    int
	UserHint      string
	Examples      []FewShot
}

// Backend is the single interface every LLM provider implements.
// Generate runs one prompt against the provider and returns the raw model
// text (expected to contain JSON matching schema). It retries up to a small
// cap when validate(raw) returns an error, strengthening the instruction each
// retry. Feature-agnostic: callers own their prompt, schema, and parsing.
type Backend interface {
	Generate(ctx context.Context, system, user string, schema []byte, validate func(raw string) error) (string, error)
	// Name returns a human-readable identifier used in logs and UI, e.g. "http:llama3.2".
	Name() string
}
