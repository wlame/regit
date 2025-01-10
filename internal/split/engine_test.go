package split

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/llm"
	"github.com/wlame/regit/internal/model"
)

// fixedBackend implements llm.Backend, always returning the same proposal.
// The proposal's Hunks field is overwritten with the request's hunks so that
// coverage validation (every hunk assigned exactly once) can pass.
type fixedBackend struct{ p model.SplitProposal }

func (f fixedBackend) Name() string { return "fixed" }
func (f fixedBackend) Propose(_ context.Context, req llm.SplitRequest, _ []byte) (model.SplitProposal, error) {
	// Attach the request's hunk list so the caller sees the right IDs.
	f.p.Hunks = req.Hunks
	return f.p, nil
}

// backendReturning wraps a fixed proposal in a llm.Backend.
func backendReturning(p model.SplitProposal) llm.Backend { return fixedBackend{p: p} }

// countingBackend tracks how many times Propose is called.
// It fails the test if Propose is invoked.
type countingBackend struct {
	t *testing.T
}

func (c countingBackend) Name() string { return "counting" }
func (c countingBackend) Propose(_ context.Context, _ llm.SplitRequest, _ []byte) (model.SplitProposal, error) {
	c.t.Fatal("Propose should not be called when skipping small commits")
	return model.SplitProposal{}, nil
}

// idsFrom extracts hunk IDs from a slice of Hunks.
func idsFrom(hs []model.Hunk) []string {
	out := make([]string, len(hs))
	for i, h := range hs {
		out[i] = h.ID
	}
	return out
}

// gitE runs a git command in dir and fails the test on error, returning trimmed stdout.
func gitE(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e",
		"GIT_AUTHOR_DATE=2026-01-01T00:00:00+00:00",
		"GIT_COMMITTER_DATE=2026-01-01T00:00:00+00:00",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// newSplittableRepo creates a throwaway git repo with a parent commit and a
// child commit whose diff spans two separate files (producing ≥ 2 hunks).
// It returns the repo, the parent tree hash, and the child tree hash.
func newSplittableRepo(t *testing.T) (*gitio.Repo, string, string) {
	t.Helper()
	dir := t.TempDir()

	gitE(t, dir, "init", "-q")
	gitE(t, dir, "config", "commit.gpgsign", "false")
	gitE(t, dir, "symbolic-ref", "HEAD", "refs/heads/main")

	// Parent commit: two files.
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("foo\nbar\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitE(t, dir, "add", "-A")
	gitE(t, dir, "commit", "-q", "-m", "parent")
	parent := gitE(t, dir, "rev-parse", "HEAD^{tree}")

	// Child commit: modify both files in distinct ways → at least 2 hunks.
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("HELLO\nworld\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("foo\nBAR\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitE(t, dir, "add", "-A")
	gitE(t, dir, "commit", "-q", "-m", "child: update a and b")
	child := gitE(t, dir, "rev-parse", "HEAD^{tree}")

	repo, err := gitio.Open(dir)
	if err != nil {
		t.Fatalf("gitio.Open: %v", err)
	}
	return repo, parent, child
}

func TestEngine_proposeBuildsTreesAndVerifiesFinal(t *testing.T) {
	repo, parent, child := newSplittableRepo(t)
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	// ReadCommits returns root-first; the child is index 1.
	childCommit := commits[1]

	hs, err := repo.Hunks(parent, child)
	if err != nil {
		t.Fatal(err)
	}
	if len(hs) < 2 {
		t.Fatalf("need ≥ 2 hunks for a meaningful split test, got %d", len(hs))
	}

	// Build a proposal: first hunk in group 1, the rest in group 2.
	prop := model.SplitProposal{
		ShouldSplit: true,
		Hunks:       hs,
		Groups: []model.HunkGroup{
			{Order: 1, HunkIDs: []string{hs[0].ID}, Subject: "first"},
			{Order: 2, HunkIDs: idsFrom(hs[1:]), Subject: "second"},
		},
	}

	eng := NewEngine(repo, backendReturning(prop), NewCache(t.TempDir()))
	opts := Options{
		MaxCommits:     4,
		LinesPerCommit: 1,
		MinSplitLines:  1,
		RepairAttempts: 2,
		WIPPrefix:      "WIP: ",
		Now:            time.Unix(0, 0),
	}

	got, trees, err := eng.Propose(context.Background(), childCommit, parent, "", opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(trees) != 2 {
		t.Fatalf("want 2 built trees, got %d", len(trees))
	}
	// The LAST built tree must equal the original commit tree (the hard invariant).
	if trees[len(trees)-1] != childCommit.TreeHash {
		t.Fatalf("last tree %s != original tree %s", trees[len(trees)-1], childCommit.TreeHash)
	}
	if !got.ShouldSplit || len(got.Groups) != 2 {
		t.Fatalf("unexpected proposal: %+v", got)
	}
}

func TestEngine_proposeSkipsSmallCommit(t *testing.T) {
	repo, parent, _ := newSplittableRepo(t)
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	childCommit := commits[1]

	// Set MinSplitLines very high so the commit is skipped.
	eng := NewEngine(repo, countingBackend{t: t}, NewCache(t.TempDir()))
	opts := Options{
		MaxCommits:     4,
		LinesPerCommit: 1,
		MinSplitLines:  100000, // Much larger than the 2-line child commit.
		RepairAttempts: 2,
		WIPPrefix:      "WIP: ",
		Now:            time.Unix(0, 0),
	}

	got, trees, err := eng.Propose(context.Background(), childCommit, parent, "", opts)
	if err != nil {
		t.Fatal(err)
	}

	// Should skip: ShouldSplit is false, trees slice is nil, no backend call.
	if got.ShouldSplit {
		t.Fatal("expected ShouldSplit=false for small commit")
	}
	if trees != nil {
		t.Fatalf("expected nil trees for skipped proposal, got %v", trees)
	}
	if got.Reason == "" {
		t.Fatal("expected Reason to be set for skipped proposal")
	}
}

// sequentialBackend consumes proposals one per Propose call from a slice, so
// the first call returns proposals[0], the second returns proposals[1], etc.
// It tracks the total call count in `calls`.
type sequentialBackend struct {
	proposals []model.SplitProposal
	calls     int
}

func (s *sequentialBackend) Name() string { return "sequential" }

// Propose returns the next proposal in the slice. The hunk list from the
// request is NOT attached automatically — the caller's proposals must already
// reference the correct IDs.  If the slice is exhausted, Propose returns an
// error so a runaway repair loop fails loudly rather than silently looping.
func (s *sequentialBackend) Propose(_ context.Context, _ llm.SplitRequest, _ []byte) (model.SplitProposal, error) {
	idx := s.calls
	s.calls++
	if idx >= len(s.proposals) {
		return model.SplitProposal{}, fmt.Errorf("sequentialBackend exhausted after %d call(s)", idx)
	}
	return s.proposals[idx], nil
}

// TestEngine_baselineFail verifies that when the test command fails even on the
// full child-commit state (the baseline check), Propose returns an error
// containing "test command fails on the original commit".
func TestEngine_baselineFail(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX sh not available on Windows")
	}

	repo, parent, child := newSplittableRepo(t)
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	childCommit := commits[1]

	hs, err := repo.Hunks(parent, child)
	if err != nil {
		t.Fatal(err)
	}
	if len(hs) < 2 {
		t.Fatalf("need ≥2 hunks, got %d", len(hs))
	}

	// Script that always fails — the baseline will never pass.
	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "fail.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	prop := model.SplitProposal{
		ShouldSplit: true,
		Groups: []model.HunkGroup{
			{Order: 1, HunkIDs: []string{hs[0].ID}, Subject: "first"},
			{Order: 2, HunkIDs: idsFrom(hs[1:]), Subject: "second"},
		},
	}
	backend := &sequentialBackend{proposals: []model.SplitProposal{prop}}
	eng := NewEngine(repo, backend, NewCache(t.TempDir()))
	opts := Options{
		MaxCommits:     4,
		LinesPerCommit: 1,
		MinSplitLines:  1,
		RepairAttempts: 2,
		TestCmd:        []string{"sh", scriptPath},
		Now:            time.Unix(0, 0),
	}

	_, _, err = eng.Propose(context.Background(), childCommit, parent, "", opts)
	if err == nil {
		t.Fatal("expected error when baseline test fails, got nil")
	}
	if !strings.Contains(err.Error(), "test command fails on the original commit") {
		t.Fatalf("error should mention baseline failure, got: %v", err)
	}
}

// TestEngine_regroupThenSucceed verifies the validate-and-repair loop: the
// initial grouping puts the a.txt hunk first, causing the stage test to fail
// (b.txt unchanged → grep for BAR fails).  The repair backend returns a
// regrouped proposal that puts the b.txt hunk first; both stages then pass.
func TestEngine_regroupThenSucceed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX sh not available on Windows")
	}

	repo, parent, child := newSplittableRepo(t)
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	childCommit := commits[1]

	hs, err := repo.Hunks(parent, child)
	if err != nil {
		t.Fatal(err)
	}
	if len(hs) < 2 {
		t.Fatalf("need ≥2 hunks, got %d", len(hs))
	}

	// Script: passes when b.txt contains "BAR" (the full child state and any
	// cumulative state that includes the b.txt hunk), fails otherwise.
	// Baseline passes (both hunks applied → b.txt=BAR).
	// Initial group-1 (only hs[0], a.txt hunk) → b.txt still "bar" → fails.
	// After regroup: group-1 gets hs[1] (b.txt→BAR) → passes; group-2 (both) → passes.
	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "check.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\ngrep -q BAR b.txt\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// hs[0] modifies a.txt, hs[1] modifies b.txt (see newSplittableRepo).
	// Initial proposal: a.txt hunk first (will fail stage 1 — b.txt not yet BAR).
	initialProp := model.SplitProposal{
		ShouldSplit: true,
		Groups: []model.HunkGroup{
			{Order: 1, HunkIDs: []string{hs[0].ID}, Subject: "first"},
			{Order: 2, HunkIDs: idsFrom(hs[1:]), Subject: "second"},
		},
	}
	// Regroup proposal: b.txt hunk first (stage 1 passes, stage 2 passes).
	regroupProp := model.SplitProposal{
		ShouldSplit: true,
		Groups: []model.HunkGroup{
			{Order: 1, HunkIDs: idsFrom(hs[1:]), Subject: "first-regrouped"},
			{Order: 2, HunkIDs: []string{hs[0].ID}, Subject: "second-regrouped"},
		},
	}

	backend := &sequentialBackend{proposals: []model.SplitProposal{initialProp, regroupProp}}
	eng := NewEngine(repo, backend, NewCache(t.TempDir()))
	opts := Options{
		MaxCommits:     4,
		LinesPerCommit: 1,
		MinSplitLines:  1,
		RepairAttempts: 2,
		TestCmd:        []string{"sh", scriptPath},
		Now:            time.Unix(0, 0),
	}

	got, trees, err := eng.Propose(context.Background(), childCommit, parent, "", opts)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if !got.ShouldSplit {
		t.Fatal("expected ShouldSplit=true")
	}
	if len(trees) != 2 {
		t.Fatalf("want 2 trees, got %d", len(trees))
	}
	if trees[len(trees)-1] != child {
		t.Fatalf("last tree %s != original child tree %s", trees[len(trees)-1], child)
	}
	// Backend should have been called at least twice: once for the initial
	// proposal and once for the repair regroup.
	if backend.calls < 2 {
		t.Fatalf("expected ≥2 backend calls, got %d", backend.calls)
	}
}

// TestEngine_WIPFallback verifies that when repair attempts are exhausted the
// failing group is marked WIP=true and Propose still returns a full trees slice
// (one entry per group) ending at the original commit tree.
func TestEngine_WIPFallback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX sh not available on Windows")
	}

	repo, parent, child := newSplittableRepo(t)
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	childCommit := commits[1]

	hs, err := repo.Hunks(parent, child)
	if err != nil {
		t.Fatal(err)
	}
	if len(hs) < 2 {
		t.Fatalf("need ≥2 hunks, got %d", len(hs))
	}

	// Same grep-BAR script: baseline (all hunks) passes, stage 1 (only a.txt
	// hunk) fails because b.txt is still "bar".  RepairAttempts=1 so after one
	// failed regroup attempt (which also puts a.txt first) the engine gives up.
	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "check.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\ngrep -q BAR b.txt\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Both the initial and the regroup proposal put a.txt first → stage 1 always fails.
	badProp := model.SplitProposal{
		ShouldSplit: true,
		Groups: []model.HunkGroup{
			{Order: 1, HunkIDs: []string{hs[0].ID}, Subject: "first"},
			{Order: 2, HunkIDs: idsFrom(hs[1:]), Subject: "second"},
		},
	}

	// sequentialBackend: first call returns badProp (initial), second call
	// returns badProp again (regroup) — still fails → WIP.
	backend := &sequentialBackend{proposals: []model.SplitProposal{badProp, badProp}}
	eng := NewEngine(repo, backend, NewCache(t.TempDir()))
	opts := Options{
		MaxCommits:     4,
		LinesPerCommit: 1,
		MinSplitLines:  1,
		RepairAttempts: 1, // one regroup attempt; if that also fails → WIP
		TestCmd:        []string{"sh", scriptPath},
		Now:            time.Unix(0, 0),
	}

	got, trees, err := eng.Propose(context.Background(), childCommit, parent, "", opts)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(trees) != 2 {
		t.Fatalf("want 2 trees (WIP rebuild), got %d", len(trees))
	}
	// The last tree must still equal the original commit tree.
	if trees[len(trees)-1] != child {
		t.Fatalf("last tree %s != original child tree %s", trees[len(trees)-1], child)
	}
	// The failing group (index 0 in order) must be marked WIP.
	wipFound := false
	for _, g := range got.Groups {
		if g.WIP {
			wipFound = true
		}
	}
	if !wipFound {
		t.Fatal("expected at least one group with WIP=true after repair exhausted")
	}
}

// TestEngine_clampsTooManyGroups verifies that clampGroups merges trailing
// groups into the last kept group and that ValidateCoverage still passes after
// the merge. This directly tests the clampGroups helper used by Propose.
func TestEngine_clampsTooManyGroups(t *testing.T) {
	// Synthetic proposal: 3 groups, 3 hunks — clamp to 2.
	hunks := []model.Hunk{
		{ID: "H1", File: "a.txt", Additions: 1},
		{ID: "H2", File: "b.txt", Additions: 1},
		{ID: "H3", File: "c.txt", Additions: 1},
	}
	prop := model.SplitProposal{
		ShouldSplit: true,
		Hunks:       hunks,
		Groups: []model.HunkGroup{
			{Order: 1, HunkIDs: []string{"H1"}, Subject: "first"},
			{Order: 2, HunkIDs: []string{"H2"}, Subject: "second"},
			{Order: 3, HunkIDs: []string{"H3"}, Subject: "third"},
		},
	}

	clamped := clampGroups(prop, 2)

	if len(clamped.Groups) != 2 {
		t.Fatalf("expected 2 groups after clamping, got %d", len(clamped.Groups))
	}

	// All three hunk IDs must still appear across the two clamped groups.
	allIDs := make(map[string]bool)
	for _, g := range clamped.Groups {
		for _, id := range g.HunkIDs {
			allIDs[id] = true
		}
	}
	for _, h := range hunks {
		if !allIDs[h.ID] {
			t.Errorf("hunk %s lost after clamping", h.ID)
		}
	}

	// Coverage invariant: every hunk must appear in exactly one group.
	if err := model.ValidateCoverage(clamped.Hunks, clamped.Groups); err != nil {
		t.Errorf("ValidateCoverage after clamp: %v", err)
	}
}
