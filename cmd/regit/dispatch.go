package main

import (
	"fmt"
	"os"
	"time"

	"github.com/wlame/regit/internal/capability"
	"github.com/wlame/regit/internal/config"
	"github.com/wlame/regit/internal/gitio"
)

// dispatch routes a registry verb (other than the Phase-1-special-cased split
// and reword) through the capability registry. It loads config + repo, builds a
// RunContext, parses the flags via capability.Parse, and calls c.Run.
//
// In Phase 1 no command has a non-nil Run yet, so this path is unreachable for
// any currently-registered verb. The nil guard returns a clear error rather than
// a nil-dereference panic.
func dispatch(verb string, args []string) error {
	c, _ := capability.Lookup(verb)

	a, err := capability.Parse(c, args)
	if err != nil {
		return err
	}

	repoDir := a.String("repo")
	if repoDir == "" {
		repoDir = "."
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	repo, err := gitio.Open(repoDir)
	if err != nil {
		return err
	}

	// Guard: no verb has a Run wired up in Phase 1.
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
