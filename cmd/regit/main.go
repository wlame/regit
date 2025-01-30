// Command regit is a terminal UI for safely rewriting the commit and author
// dates of the current branch's history — spreading clustered commits across a
// calendar, with strategy presets, while guaranteeing file contents never change.
package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/wlame/regit/internal/config"
	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/safety"
	"github.com/wlame/regit/internal/ui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "regit:", err)
		os.Exit(1)
	}
}

func run() error {
	repoPath := flag.String("repo", ".", "path to the git repository to operate on")
	flag.Parse()

	// Resolve the repo directory before dispatching so both TUI and subcommands
	// share the same path regardless of how the flag was positioned.
	repoDir := *repoPath

	// Subcommand dispatch: `regit split` and `regit reword` run headless.
	// All other invocations fall through to the TUI.
	if len(os.Args) > 1 && os.Args[1] == "split" {
		return runSplit(repoDir, os.Args[2:])
	}
	if len(os.Args) > 1 && os.Args[1] == "reword" {
		if err := runReword(repoDir, os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "regit:", err)
			os.Exit(1)
		}
		return nil
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	repo, err := gitio.Open(repoDir)
	if err != nil {
		return err
	}

	state, err := repo.ReadState()
	if err != nil {
		return fmt.Errorf("read repository state: %w", err)
	}

	commits, err := repo.ReadCommits()
	if err != nil {
		return fmt.Errorf("read commits: %w", err)
	}
	if len(commits) == 0 {
		return fmt.Errorf("no commits found on the current branch")
	}

	mgr := safety.NewManager(repo)
	model := ui.New(repo, mgr, cfg, state, commits)

	// Full-screen alternate buffer; bubbletea restores the terminal on exit.
	program := tea.NewProgram(model, tea.WithAltScreen())
	_, err = program.Run()
	return err
}
