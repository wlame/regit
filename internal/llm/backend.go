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
	FileStates    map[string]string
	MaxCommits    int
	UserHint      string
	Examples      []FewShot
}

// Backend is the single interface every LLM provider implements.
// Propose sends the split request to the provider and returns an ordered
// grouping of hunks. schema is the JSON schema bytes the provider should
// constrain its output to (passed through to backends that support
// structured output, e.g. OpenAI's response_format).
type Backend interface {
	Propose(ctx context.Context, req SplitRequest, schema []byte) (model.SplitProposal, error)
	// Name returns a human-readable identifier used in logs and UI, e.g. "http:llama3.2".
	Name() string
}
