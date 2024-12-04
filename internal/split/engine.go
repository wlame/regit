package split

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/llm"
	"github.com/wlame/regit/internal/model"
)

// Options tunes one split run. Now is injected so the package never reads the
// clock (keeps runs deterministic and testable).
type Options struct {
	MaxCommits     int
	LinesPerCommit int
	MinSplitLines  int
	RepairAttempts int // 0 means never attempt repair — a failing stage is immediately marked WIP.
	WIPPrefix      string
	UserHint       string
	// ContextKey is mixed into the cache fingerprint (e.g. "readme") so that
	// a change to the contextual input causes a cache miss.
	ContextKey string
	TestCmd    []string
	Now        time.Time
}

// Engine produces validated split proposals with their built trees.
type Engine struct {
	repo    *gitio.Repo
	backend llm.Backend
	cache   *Cache
}

// NewEngine constructs an Engine that uses repo for git operations, backend for
// LLM grouping, and cache to avoid redundant backend calls.
func NewEngine(repo *gitio.Repo, backend llm.Backend, cache *Cache) *Engine {
	return &Engine{repo: repo, backend: backend, cache: cache}
}

// Propose extracts hunks, asks the backend to group them (honoring the size
// clamp and skip guards), builds the cumulative intermediate trees, runs the
// validate-and-repair loop, verifies the final tree, and returns the proposal
// plus the per-group tree hashes (in group order). It does NOT move any ref.
//
// When ShouldSkipSplit is true for the extracted hunks, it returns a proposal
// with ShouldSplit=false and a nil trees slice immediately (no LLM call, no
// tree building).
//
// The hard invariant: trees[len(trees)-1] == commit.TreeHash. If that fails,
// Propose returns an error rather than a corrupt result.
func (e *Engine) Propose(ctx context.Context, commit model.Commit, parentTree, readme string, opts Options) (model.SplitProposal, []string, error) {
	hunks, err := e.repo.Hunks(parentTree, commit.TreeHash)
	if err != nil {
		return model.SplitProposal{}, nil, err
	}

	// Short-circuit: too small, single-hunk, or binary-only.
	if skip, reason := ShouldSkipSplit(hunks, opts.MinSplitLines); skip {
		return model.SplitProposal{
			CommitHash:  commit.Hash,
			Hunks:       hunks,
			ShouldSplit: false,
			Reason:      reason,
		}, nil, nil
	}

	maxK := EffectiveMaxCommits(hunks, opts.MaxCommits, opts.LinesPerCommit)
	fp := Fingerprint(commit.TreeHash, e.backend.Name(), opts.ContextKey, opts.UserHint, maxK)

	// Try the cache before hitting the backend.
	prop, hit := e.cache.Get(commit.Hash, fp)
	if !hit {
		prop, err = e.backend.Propose(ctx, llm.SplitRequest{
			CommitMessage: commit.Message,
			Hunks:         hunks,
			Readme:        readme,
			MaxCommits:    maxK,
			UserHint:      opts.UserHint,
		}, llm.SchemaJSON())
		if err != nil {
			return model.SplitProposal{}, nil, err
		}
	}

	// Always stamp the proposal with the live commit identity and hunk list,
	// even when the result came from cache (the cache stores the proposal
	// without tying it to a specific commit/parent hash pair).
	prop.CommitHash = commit.Hash
	prop.ParentHash = firstParent(commit)
	prop.Hunks = hunks

	if !prop.ShouldSplit || len(prop.Groups) < 2 {
		return prop, nil, nil
	}

	// Build cumulative trees and run the optional test command per stage.
	trees, err := e.buildAndValidate(ctx, commit, parentTree, &prop, opts)
	if err != nil {
		return model.SplitProposal{}, nil, err
	}

	// Hard invariant: the last built tree must equal the original commit tree.
	// If it doesn't, the split is incomplete or corrupt — never return it.
	if trees[len(trees)-1] != commit.TreeHash {
		return model.SplitProposal{}, nil, fmt.Errorf(
			"split invariant violated: rebuilt tree %s != original commit tree %s",
			trees[len(trees)-1], commit.TreeHash,
		)
	}

	// Cache the validated proposal so the next call with identical inputs skips
	// the backend. Use opts.Now as CreatedAt so the engine never reads the clock.
	// Cache write failure is non-fatal; the next run simply re-requests the proposal.
	_ = e.cache.Put(CacheEntry{
		CommitHash:  commit.Hash,
		Fingerprint: fp,
		Proposal:    prop,
		CreatedAt:   opts.Now,
	})

	return prop, trees, nil
}

// buildAndValidate builds a cumulative tree for each ordered group and, when a
// test command is configured, runs it after each stage. A failing stage
// triggers up to RepairAttempts LLM regroups; if the stage still fails the
// failing group is marked WIP and the full set of trees is rebuilt without
// test gating so the caller always receives one tree per group.
func (e *Engine) buildAndValidate(ctx context.Context, commit model.Commit, parentTree string, prop *model.SplitProposal, opts Options) ([]string, error) {
	wt, err := e.repo.NewWorktree(parentTree)
	if err != nil {
		return nil, err
	}
	defer wt.Close()

	// When a test command is configured, verify the original commit passes it
	// first. If it doesn't, the test command itself is mis-configured; bail
	// early with a clear message rather than blaming the split.
	if len(opts.TestCmd) > 0 {
		// Apply ALL hunks to bring the worktree to the full child state.
		// BuildTreeFromHunks resets the worktree to parentTree on each call,
		// so subsequent calls overwrite the baseline state.
		if _, err := e.repo.BuildTreeFromHunks(parentTree, prop.Hunks, wt); err != nil {
			return nil, err
		}
		if err := runTest(ctx, wt.Dir, opts.TestCmd); err != nil {
			return nil, fmt.Errorf("test command fails on the original commit; fix --test-cmd: %w", err)
		}
	}

	for attempt := 0; ; attempt++ {
		ordered := orderGroups(prop.Groups)
		trees := make([]string, len(ordered))
		byID := indexHunks(prop.Hunks)
		failed := -1
		var cumulative []model.Hunk

		for i, g := range ordered {
			// Grow the cumulative subset with this group's hunks, then sort
			// into original hunk order before calling BuildTreeFromHunks —
			// assemblePatch requires file+line order to emit a clean diff.
			cumulative = append(cumulative, hunksFor(g, byID)...)
			sortByOriginalOrder(cumulative, prop.Hunks)

			tree, err := e.repo.BuildTreeFromHunks(parentTree, cumulative, wt)
			if err != nil {
				return nil, err
			}
			trees[i] = tree
			ordered[i].TestsPass = true

			if len(opts.TestCmd) > 0 {
				if err := runTest(ctx, wt.Dir, opts.TestCmd); err != nil {
					ordered[i].TestsPass = false
					failed = i
					break
				}
			}
		}

		if failed < 0 {
			// All stages passed (or there is no test command).
			prop.Groups = ordered
			return trees, nil
		}

		if attempt >= opts.RepairAttempts {
			// Exhausted repair budget: mark the failing group WIP and return
			// the full tree set without test gating.
			ordered[failed].WIP = true
			prop.Groups = ordered
			return e.rebuildAll(parentTree, prop, wt)
		}

		// Ask the backend to regroup, feeding back which stage failed.
		repairMsg := commit.Message + "\n\n(stage \"" + ordered[failed].Subject +
			"\" failed tests; regroup so each stage builds)"
		regrouped, rerr := e.backend.Propose(ctx, llm.SplitRequest{
			CommitMessage: repairMsg,
			Hunks:         prop.Hunks,
			MaxCommits:    len(ordered),
			UserHint:      opts.UserHint,
		}, llm.SchemaJSON())
		if rerr == nil && regrouped.ShouldSplit && len(regrouped.Groups) >= 2 {
			prop.Groups = regrouped.Groups
		} else {
			// Could not regroup; mark WIP and finish.
			ordered[failed].WIP = true
			prop.Groups = ordered
			return e.rebuildAll(parentTree, prop, wt)
		}
	}
}

// rebuildAll builds every cumulative tree without test gating, used when the
// repair loop gives up and marks a group WIP.
func (e *Engine) rebuildAll(parentTree string, prop *model.SplitProposal, wt *gitio.Worktree) ([]string, error) {
	ordered := orderGroups(prop.Groups)
	trees := make([]string, len(ordered))
	byID := indexHunks(prop.Hunks)
	var cumulative []model.Hunk
	for i, g := range ordered {
		cumulative = append(cumulative, hunksFor(g, byID)...)
		sortByOriginalOrder(cumulative, prop.Hunks)
		tree, err := e.repo.BuildTreeFromHunks(parentTree, cumulative, wt)
		if err != nil {
			return nil, err
		}
		trees[i] = tree
	}
	prop.Groups = ordered
	return trees, nil
}

// --- small helpers ---

// firstParent returns the first parent hash of commit c, or "" for root commits.
func firstParent(c model.Commit) string {
	if len(c.ParentHashes) == 0 {
		return ""
	}
	return c.ParentHashes[0]
}

// orderGroups returns a copy of groups sorted ascending by Order.
func orderGroups(groups []model.HunkGroup) []model.HunkGroup {
	out := make([]model.HunkGroup, len(groups))
	copy(out, groups)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Order < out[j].Order })
	return out
}

// indexHunks builds an ID → Hunk lookup table.
func indexHunks(hunks []model.Hunk) map[string]model.Hunk {
	m := make(map[string]model.Hunk, len(hunks))
	for _, h := range hunks {
		m[h.ID] = h
	}
	return m
}

// hunksFor looks up each HunkID in byID and returns the matching Hunk values.
// Unknown IDs are silently skipped (the LLM coverage validator catches them).
func hunksFor(g model.HunkGroup, byID map[string]model.Hunk) []model.Hunk {
	out := make([]model.Hunk, 0, len(g.HunkIDs))
	for _, id := range g.HunkIDs {
		if h, ok := byID[id]; ok {
			out = append(out, h)
		}
	}
	return out
}

// sortByOriginalOrder sorts a cumulative hunk slice into the order the hunks
// appear in `all` (the full Hunks() list). assemblePatch requires file+line
// order to emit a clean unified diff; without this sort, cross-file groups
// would apply in wrong order and git apply would fail.
func sortByOriginalOrder(cumulative []model.Hunk, all []model.Hunk) {
	pos := make(map[string]int, len(all))
	for i, h := range all {
		pos[h.ID] = i
	}
	sort.SliceStable(cumulative, func(i, j int) bool {
		return pos[cumulative[i].ID] < pos[cumulative[j].ID]
	})
}

// runTest executes the configured test command in dir; a non-zero exit is
// returned as an error whose message includes the last 40 lines of output.
func runTest(ctx context.Context, dir string, testCmd []string) error {
	if len(testCmd) == 0 {
		return nil
	}
	cmd := exec.CommandContext(ctx, testCmd[0], testCmd[1:]...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, tail(string(out), 40))
	}
	return nil
}

// tail returns the last n lines of s. Used to include the most relevant part
// of a failing test command's output in the repair prompt.
func tail(s string, n int) string {
	parts := strings.Split(s, "\n")
	if len(parts) <= n {
		return s
	}
	return strings.Join(parts[len(parts)-n:], "\n")
}
