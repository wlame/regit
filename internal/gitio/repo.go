// Package gitio is the only package that talks to git. It reads the commit graph
// of a repository and applies a rewrite by replaying history with git plumbing,
// reusing each commit's original tree object so file contents can never change.
//
// Everything here shells out to the `git` binary via os/exec rather than using a
// pure-Go git library: it matches git's own behavior exactly (config, hooks,
// signing), needs no extra dependencies, and keeps the rewrite auditable.
package gitio

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Repo represents an open git repository rooted at an absolute path. All git
// commands run with their working directory set to this root.
type Repo struct {
	root string
}

// Open locates the git repository that contains startDir and returns a Repo for
// it. It fails if startDir is not inside a git work tree.
func Open(startDir string) (*Repo, error) {
	out, err := runGit(startDir, nil, nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("not a git repository (%s): %w", startDir, err)
	}
	root := strings.TrimSpace(out)
	if root == "" {
		return nil, fmt.Errorf("could not determine repository root for %s", startDir)
	}
	return &Repo{root: root}, nil
}

// Root returns the absolute path to the repository root.
func (r *Repo) Root() string { return r.root }

// GitDir returns the absolute path to the .git directory (or the bare repo
// directory). It runs `git rev-parse --absolute-git-dir`, which works in both
// regular and worktree checkouts and always returns an absolute path.
func (r *Repo) GitDir() (string, error) {
	return r.git("rev-parse", "--absolute-git-dir")
}

// HasSigningKey reports whether the repo has a commit-signing key configured
// (user.signingkey set — required for both GPG and SSH signing). Used to fail
// --sign fast with a clear message instead of letting `commit-tree -S` error
// deep inside a replay.
func (r *Repo) HasSigningKey() bool {
	key, err := r.git("config", "--get", "user.signingkey")
	if err == nil && strings.TrimSpace(key) != "" {
		return true
	}
	return false
}

// git runs a git command in the repo root and returns trimmed stdout.
func (r *Repo) git(args ...string) (string, error) {
	out, err := runGit(r.root, nil, nil, args...)
	return strings.TrimSpace(out), err
}

// gitRaw runs a git command and returns stdout WITHOUT trimming, which matters
// when parsing record-separated log output where trailing bytes are meaningful.
func (r *Repo) gitRaw(args ...string) (string, error) {
	return runGit(r.root, nil, nil, args...)
}

// gitWithEnvStdin runs git with extra environment variables and a stdin payload.
// Used by the rewrite engine to inject GIT_AUTHOR_DATE / GIT_COMMITTER_DATE and
// feed the commit message on stdin (avoiding shell-escaping pitfalls).
func (r *Repo) gitWithEnvStdin(env []string, stdin string, args ...string) (string, error) {
	out, err := runGit(r.root, env, []byte(stdin), args...)
	return strings.TrimSpace(out), err
}

// gitLocationEnv lists the GIT_* variables that redirect git to a different
// repository, index, or object store. regit pins every operation with cmd.Dir and
// runs all intermediate work in its own isolated worktrees, so an inherited value
// here is never needed and only ever a footgun: if the caller's shell exported
// GIT_INDEX_FILE (hooks and some scripts do), our worktree `read-tree --reset -u`
// would operate on the USER'S real index and clobber it; a stale GIT_DIR would make
// every command target the wrong repository despite cmd.Dir. We strip them before
// running git. Note we do NOT strip GIT_CONFIG_GLOBAL/SYSTEM or GIT_AUTHOR_*/
// GIT_COMMITTER_*: tests rely on the former for isolation, and callers append the
// latter as explicit overrides after this scrub.
var gitLocationEnv = map[string]bool{
	"GIT_DIR":                          true,
	"GIT_WORK_TREE":                    true,
	"GIT_INDEX_FILE":                   true,
	"GIT_OBJECT_DIRECTORY":             true,
	"GIT_COMMON_DIR":                   true,
	"GIT_ALTERNATE_OBJECT_DIRECTORIES": true,
	"GIT_NAMESPACE":                    true,
}

// scrubGitEnv returns env with the gitLocationEnv variables removed. It allocates
// a fresh slice so the caller's environment is never mutated.
func scrubGitEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if i := strings.IndexByte(kv, '='); i >= 0 && gitLocationEnv[kv[:i]] {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// gitTimeout bounds any single git invocation so a wedged git — a credential
// or gpg helper waiting for input that never comes, a hung network filesystem —
// cannot block regit forever. Ten minutes is far beyond any legitimate single
// command, even on a huge repository; the ceiling exists to turn "hangs
// forever" into a clear error. It is a variable so tests can shorten it.
var gitTimeout = 10 * time.Minute

// runGit is the single choke point for executing git. Keeping it in one place
// makes the command surface easy to audit and to stub in tests if ever needed.
func runGit(dir string, extraEnv []string, stdin []byte, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	// Always run against a scrubbed copy of the ambient environment (see
	// gitLocationEnv), then append any caller overrides (GIT_AUTHOR_DATE etc.) so
	// they win over anything left in the environment. This runs even when extraEnv
	// is nil so a read command cannot be steered by a leaked GIT_DIR/GIT_INDEX_FILE.
	cmd.Env = append(scrubGitEnv(cmd.Environ()), extraEnv...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	// After the deadline kills git, give any helper children (gpg, ssh) a short
	// grace period before the wait is abandoned, so a wedged helper is still
	// reaped instead of leaking (mirrors the agent subprocess handling).
	cmd.WaitDelay = 10 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("git %s: timed out after %s", strings.Join(args, " "), gitTimeout)
		}
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
