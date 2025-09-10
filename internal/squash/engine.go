// internal/squash/engine.go
package squash

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/llm"
	"github.com/wlame/regit/internal/model"
)

// Options tune one Summarize call.
type Options struct {
	WithDiffs bool      // include each member's diff in the prompt
	Prompt    string    // short user guidance (affects the fingerprint)
	NoCache   bool      // ignore any cached summary
	Now       time.Time // stamped onto a written cache entry (never time.Now())
}

// Engine summarizes a chain of commits into a single message via an LLM backend,
// caching the result. It is the inverse-of-split sibling of split.Engine.
type Engine struct {
	repo    *gitio.Repo
	backend llm.Backend
	cache   *Cache
}

// NewEngine wires an Engine. repo may be nil for messages-only use (no diffs).
func NewEngine(repo *gitio.Repo, backend llm.Backend, cache *Cache) *Engine {
	return &Engine{repo: repo, backend: backend, cache: cache}
}

// Summarize returns a combined commit message for the chain. It consults the
// cache (unless NoCache), otherwise calls the backend and caches the result.
func (e *Engine) Summarize(ctx context.Context, ch model.SquashChain, opts Options) (Proposal, error) {
	fp := Fingerprint(chainKey(ch), e.backend.Name(), fingerprintExtra(opts))
	if !opts.NoCache {
		if p, ok := e.cache.Get(ch.Last().Hash, fp); ok {
			return p, nil
		}
	}

	var diffs []string
	if opts.WithDiffs {
		var err error
		if diffs, err = e.memberDiffs(ch); err != nil {
			return Proposal{}, err
		}
	}

	system, user := BuildMessages(ch, diffs)
	if opts.Prompt != "" {
		user += "\nGuidance: " + opts.Prompt + "\n"
	}
	raw, err := e.backend.Generate(ctx, system, user, SchemaJSON(), validateProposalJSON)
	if err != nil {
		return Proposal{}, err
	}
	p, err := ParseProposal(raw)
	if err != nil {
		return Proposal{}, err
	}
	// Cache writes are non-fatal: a miss next run is the worst outcome.
	_ = e.cache.Put(CacheEntry{CommitHash: ch.Last().Hash, Fingerprint: fp, Proposal: p, CreatedAt: opts.Now})
	return p, nil
}

// memberDiffs returns the patch text for each member (member k diffed against its
// own parent tree). Requires a non-nil repo.
func (e *Engine) memberDiffs(ch model.SquashChain) ([]string, error) {
	out := make([]string, len(ch.Members))
	parentTree, err := e.parentTreeOf(ch.First())
	if err != nil {
		return nil, err
	}
	prevTree := parentTree
	for i, m := range ch.Members {
		hunks, err := e.repo.Hunks(prevTree, m.TreeHash)
		if err != nil {
			return nil, fmt.Errorf("diff %s: %w", m.Hash, err)
		}
		var b strings.Builder
		for _, h := range hunks {
			b.WriteString(h.Patch())
		}
		out[i] = b.String()
		prevTree = m.TreeHash
	}
	return out, nil
}

// parentTreeOf returns the tree of c's first parent, or the empty-tree hash when
// c is the root commit.
func (e *Engine) parentTreeOf(c model.Commit) (string, error) {
	if len(c.ParentHashes) == 0 {
		return e.repo.EmptyTree()
	}
	return e.repo.ResolveRef(c.ParentHashes[0] + "^{tree}")
}

// chainKey hashes the ordered member hashes so the cache key changes if the chain
// composition changes.
func chainKey(ch model.SquashChain) string {
	h := sha256.New()
	for _, m := range ch.Members {
		fmt.Fprintf(h, "%s\x1f", m.Hash)
	}
	return fmt.Sprintf("%x", h.Sum(nil))[:32]
}

// fingerprintExtra folds the non-chain inputs that affect the result.
func fingerprintExtra(opts Options) string {
	return fmt.Sprintf("%t\x1f%s", opts.WithDiffs, opts.Prompt)
}
