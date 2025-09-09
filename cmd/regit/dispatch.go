package main

import (
	"fmt"
	"os"
	"time"

	"github.com/wlame/regit/internal/capability"
	"github.com/wlame/regit/internal/config"
	"github.com/wlame/regit/internal/gitio"
)

// dispatch routes a registry verb through the capability registry: it loads
// config + repo, builds a RunContext, parses the flags via capability.Parse, and
// calls the command's Run.
//
// repoDir is the default repository path supplied by the top-level --repo flag
// (or "." when absent). The verb's own --repo flag, when explicitly provided,
// takes precedence.
//
// A registry row whose Run was never wired up is rejected with a clear error
// rather than a nil-dereference panic.
func dispatch(verb string, repoDir string, args []string) error {
	c, _ := capability.Lookup(verb)

	// Rewrite legacy flags (--yes → --execute, drop --dry-run) for split and reword.
	args = capability.DeprecatedRewrite(verb, args, os.Stderr)

	a, err := capability.Parse(c, args)
	if err != nil {
		return err
	}

	// The verb's own --repo flag wins over the top-level default when explicitly set.
	if a.Has("repo") {
		repoDir = a.String("repo")
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	repo, err := gitio.Open(repoDir)
	if err != nil {
		return err
	}

	// Guard against a registry row whose Run was never wired up.
	if c.Run == nil {
		return fmt.Errorf("command %q is not yet implemented", verb)
	}

	ctx := &capability.RunContext{
		Repo:   repo,
		Cfg:    cfg,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Stdin:  os.Stdin,
		Now:    time.Now(),
	}
	return c.Run(ctx, a)
}
