package gitio

import (
	"fmt"
	"os"
	"strings"
)

// Worktree is a throwaway, detached git worktree used to build and test
// intermediate split states WITHOUT touching the user's working tree or index.
// It has its own index and checked-out files but shares the repository's object
// database, so trees and commits created here are visible from the main repo.
type Worktree struct {
	Dir  string
	repo *Repo
}

// NewWorktree creates a detached worktree checked out at the given object in a
// fresh temp directory. `at` may be a commit-ish (the common case) or a raw
// tree hash — the latter is useful when callers like BuildTreeFromHunks receive
// a tree hash directly from Hunks/Repo.Hunks rather than a commit ref. Always
// pair with defer wt.Close().
func (r *Repo) NewWorktree(at string) (*Worktree, error) {
	dir, err := os.MkdirTemp("", "regit-split-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}

	// git worktree add only accepts a commit-ish, not a raw tree hash. When `at`
	// is a tree object, create the worktree with --no-checkout and then populate
	// it via read-tree so the index and working files match the requested tree.
	objType, err := r.git("cat-file", "-t", at)
	if err != nil {
		os.RemoveAll(dir)
		return nil, fmt.Errorf("resolve object type for %s: %w", at, err)
	}
	if objType == "tree" {
		// Create an empty detached worktree (files are NOT populated yet).
		if _, err := r.git("worktree", "add", "--no-checkout", "--detach", "--quiet", dir, "HEAD"); err != nil {
			os.RemoveAll(dir)
			return nil, fmt.Errorf("git worktree add (no-checkout): %w", err)
		}
		wt := &Worktree{Dir: dir, repo: r}
		// Populate index + working tree from the requested tree object.
		if _, err := wt.git("read-tree", "--reset", "-u", at); err != nil {
			wt.Close()
			return nil, fmt.Errorf("read-tree for tree hash %s: %w", at, err)
		}
		return wt, nil
	}

	if _, err := r.git("worktree", "add", "--detach", "--quiet", dir, at); err != nil {
		os.RemoveAll(dir)
		return nil, fmt.Errorf("git worktree add: %w", err)
	}
	return &Worktree{Dir: dir, repo: r}, nil
}

// git runs a git command with its working directory set to the worktree, so the
// command sees the worktree's own index and files.
func (w *Worktree) git(args ...string) (string, error) {
	out, err := runGit(w.Dir, nil, nil, args...)
	return strings.TrimSpace(out), err
}

// gitWithEnvStdin runs git in the worktree with extra env and a stdin payload
// (used to feed patches to `git apply`).
func (w *Worktree) gitWithEnvStdin(env []string, stdin string, args ...string) (string, error) {
	out, err := runGit(w.Dir, env, []byte(stdin), args...)
	return strings.TrimSpace(out), err
}

// Close removes the worktree registration and its directory. --force is needed
// because the detached worktree may have a dirty index from in-progress split
// operations. Safe to call once; subsequent calls are no-ops.
func (w *Worktree) Close() error {
	if w.Dir == "" {
		return nil
	}
	_, err := w.repo.git("worktree", "remove", "--force", w.Dir)
	os.RemoveAll(w.Dir) // belt and suspenders if git left anything
	w.Dir = ""
	return err
}

// EmptyTree returns the hash of git's empty tree object, used as the baseline
// when splitting a root commit (which has no parent).
func (r *Repo) EmptyTree() (string, error) {
	// empty stdin → git's empty tree object
	return r.gitWithEnvStdin(nil, "", "mktree")
}
