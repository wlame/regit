package reword

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/llm"
	"github.com/wlame/regit/internal/model"
)

// Options tunes one reword run. Now is injected so the package never reads the
// clock directly, keeping cache timestamps deterministic in tests.
type Options struct {
	// Variants is how many ranked message alternatives to request from the
	// backend. Clamped to at least 1.
	Variants int
	// ContextLines controls the -U<n> depth passed to git diff. Defaults to 3.
	ContextLines int
	// Context is a list of optional context keys (e.g. "readme", "filestates")
	// that affect which supplementary information is gathered. Incorporated into
	// the cache fingerprint so that adding or removing a context key invalidates
	// prior results.
	Context []string
	// UserHint is free-text guidance the user typed (e.g. "focus on the why").
	UserHint string
	// AgentScan gates a future agent-scanning path (Task 10). This safe-mode
	// engine ignores it; the flag is reserved here so callers can begin wiring it.
	AgentScan bool
	// Now is the wall-clock time used for cache entry timestamps. Callers must
	// supply this; the engine never calls time.Now() itself.
	Now time.Time
}

// Engine generates ranked commit message variants for one commit at a time.
// It is stateless across commits; the caller is responsible for parallelism or
// pipelines over multiple commits.
type Engine struct {
	repo    *gitio.Repo
	backend llm.Backend
	cache   *Cache
}

// NewEngine returns an Engine ready to generate variants.
//
// repo is used only for DiffText; it is never written.
// backend drives the LLM call.
// cache is the result store; pass NewCache(repo.GitDir()) in production.
func NewEngine(repo *gitio.Repo, backend llm.Backend, cache *Cache) *Engine {
	return &Engine{repo: repo, backend: backend, cache: cache}
}

// Generate returns ranked reword variants for a single commit c.
//
// parentTree is the tree hash of c's parent (pass EmptyTree for a root commit).
// In agent-scan mode (opts.AgentScan == true), parentTree is not used: the
// agent inspects the commit itself via git show, so no diff is gathered here.
//
// readme is the project README contents to include in the prompt; the caller
// reads it once (via ReadREADME) and passes the same string for every commit.
// In scan mode readme is ignored (the agent fetches its own context).
//
// Cache semantics: if a prior result was stored under the same commit hash and
// fingerprint (diff-or-hash + backend + context + hint + variants), it is
// returned immediately without a backend call.
func (e *Engine) Generate(ctx context.Context, c model.Commit, parentTree, readme string, opts Options) (Result, error) {
	// Sort context to make the cache key order-invariant.
	sortedCtx := make([]string, len(opts.Context))
	copy(sortedCtx, opts.Context)
	sort.Strings(sortedCtx)
	contextKey := strings.Join(sortedCtx, ",")

	var system, user string

	if opts.AgentScan {
		// Agent-scan mode: skip DiffText — the agent will call `git show` itself.
		// Use the commit hash as the fingerprint source so the cache is keyed by
		// commit identity rather than a diff hash.
		fp := Fingerprint(c.Hash, e.backend.Name(), contextKey, opts.UserHint, opts.Variants)

		if r, ok := e.cache.Get(c.Hash, fp); ok {
			return r, nil
		}

		system, user = BuildScanMessages(c.Hash, filepath.Base(e.repo.Root()), opts.UserHint, opts.Variants)

		raw, err := e.backend.Generate(ctx, system, user, SchemaJSON(), func(raw string) error {
			_, perr := ParseVariants(raw)
			return perr
		})
		if err != nil {
			return Result{}, fmt.Errorf("reword: backend generate: %w", err)
		}

		r, err := ParseVariants(raw)
		if err != nil {
			return Result{}, fmt.Errorf("reword: parse variants: %w", err)
		}
		r.CommitHash = c.Hash

		_ = e.cache.Put(CacheEntry{
			CommitHash:  c.Hash,
			Fingerprint: fp,
			Result:      r,
			CreatedAt:   opts.Now,
		})

		return r, nil
	}

	// Normal mode: gather the wide-context diff for this commit.
	diff, err := e.repo.DiffText(parentTree, c.TreeHash, opts.ContextLines)
	if err != nil {
		return Result{}, fmt.Errorf("reword: diff %s: %w", c.Hash, err)
	}

	// Build a stable cache key from every input that affects the result.
	fp := Fingerprint(diffHash(diff), e.backend.Name(), contextKey, opts.UserHint, opts.Variants)

	// Return the cached result if it is still valid for these exact inputs.
	if r, ok := e.cache.Get(c.Hash, fp); ok {
		return r, nil
	}

	// Build the prompt request and call the backend.
	req := Request{
		RepoName:   filepath.Base(e.repo.Root()),
		Readme:     readme,
		Diff:       diff,
		OldSubject: c.Subject,
		OldBody:    c.Body,
		UserHint:   opts.UserHint,
		Variants:   opts.Variants,
	}
	system, user = BuildMessages(req)

	raw, err := e.backend.Generate(ctx, system, user, SchemaJSON(), func(raw string) error {
		_, perr := ParseVariants(raw)
		return perr
	})
	if err != nil {
		return Result{}, fmt.Errorf("reword: backend generate: %w", err)
	}

	r, err := ParseVariants(raw)
	if err != nil {
		return Result{}, fmt.Errorf("reword: parse variants: %w", err)
	}
	r.CommitHash = c.Hash

	// Persist to cache; ignore write errors so a read-only .git dir never
	// blocks the user from seeing results.
	_ = e.cache.Put(CacheEntry{
		CommitHash:  c.Hash,
		Fingerprint: fp,
		Result:      r,
		CreatedAt:   opts.Now,
	})

	return r, nil
}

// diffHash returns a 32-character hex fingerprint of a diff string.
// Used as the first component of the cache Fingerprint so that any change to
// the diff (different context lines, amended content) invalidates cached results.
func diffHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", sum[:16])
}
