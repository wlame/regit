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
	"fmt"
	"os/exec"
	"strings"
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

// runGit is the single choke point for executing git. Keeping it in one place
// makes the command surface easy to audit and to stub in tests if ever needed.
func runGit(dir string, extraEnv []string, stdin []byte, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if extraEnv != nil {
		// Inherit the ambient environment, then append overrides so callers can
		// set GIT_* date/identity variables without dropping PATH, HOME, etc.
		cmd.Env = append(cmd.Environ(), extraEnv...)
	}
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
