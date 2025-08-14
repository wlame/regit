// Command regit is a terminal UI for safely rewriting the commit and author
// dates of the current branch's history — spreading clustered commits across a
// calendar, with strategy presets, while guaranteeing file contents never change.
package main

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/wlame/regit/internal/capability"
	"github.com/wlame/regit/internal/config"
	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/safety"
	"github.com/wlame/regit/internal/ui"
)

// version is the build version, stamped at link time via
// -ldflags "-X main.version=...". Plain `go build` leaves it as "dev".
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "regit:", err)
		os.Exit(1)
	}
}

// wantsVersion reports whether the arguments request the version banner. It is
// checked before all other dispatch so `--version` never trips flag parsing.
func wantsVersion(args []string) bool {
	for _, a := range args {
		if a == "--version" || a == "-version" {
			return true
		}
	}
	return false
}

// wantsHelp reports whether args (the args after the verb) contain --help or -h.
func wantsHelp(args []string) bool {
	for _, a := range args {
		if a == "--help" || a == "-h" {
			return true
		}
	}
	return false
}

// extractRepoFlag scans the leading flag tokens of args for --repo=VALUE or
// --repo VALUE, strips that token (and its value argument) out, and returns
// (value, remainingArgs). Scanning stops at the first non-flag token (the verb
// or a positional argument) so that per-subcommand --repo flags are left
// untouched for the verb's own parser. When no top-level --repo is found it
// returns (".", args) unchanged.
func extractRepoFlag(args []string) (string, []string) {
	for i, a := range args {
		// Stop at the first non-flag arg (verb or positional).
		if !strings.HasPrefix(a, "-") {
			break
		}
		if strings.HasPrefix(a, "--repo=") {
			val := strings.TrimPrefix(a, "--repo=")
			rest := make([]string, 0, len(args)-1)
			rest = append(rest, args[:i]...)
			rest = append(rest, args[i+1:]...)
			return val, rest
		}
		if a == "--repo" && i+1 < len(args) {
			val := args[i+1]
			rest := make([]string, 0, len(args)-2)
			rest = append(rest, args[:i]...)
			rest = append(rest, args[i+2:]...)
			return val, rest
		}
	}
	return ".", args
}

// rejectUnknownFlag returns an error if args starts with an unrecognised
// top-level flag (anything beginning with "-" that survived --repo, --help,
// and --version extraction). The error directs the user to --help.
func rejectUnknownFlag(args []string) error {
	if len(args) > 0 && strings.HasPrefix(args[0], "-") {
		return fmt.Errorf("unknown flag %q (try regit --help)", args[0])
	}
	return nil
}

func run() error {
	// Handle `regit --version` before anything else.
	if wantsVersion(os.Args[1:]) {
		fmt.Println("regit", version)
		return nil
	}

	// Extract a top-level --repo flag so that both `regit --repo=/path` (TUI)
	// and `regit --repo=/path <verb> …` (subcommand) route to the right repo.
	// Per-subcommand --repo flags are left in rest and handled by their own parser.
	repoDir, args := extractRepoFlag(os.Args[1:])

	// Subcommand dispatch: the first non-flag token is the verb.
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		verb := args[0]
		rest := args[1:]

		// `regit <verb> --help` prints that verb's flags from the registry.
		if wantsHelp(rest) {
			if c, ok := capability.Lookup(verb); ok {
				fmt.Print(capability.HelpCommand(c))
				return nil
			}
		}

		// Phase 1: split and reword use their existing legacy paths so that
		// apply-by-default, --dry-run, and --yes all continue to work unchanged.
		// Phase 2 will flip them to the new --execute convention.
		switch verb {
		case "split":
			return runSplit(repoDir, rest)
		case "reword":
			return runReword(repoDir, rest)
		default:
			// Any other registry verb is routed through dispatch (no Run set yet
			// in Phase 1, so dispatch will return a "not yet implemented" error).
			if _, ok := capability.Lookup(verb); ok {
				return dispatch(verb, repoDir, rest)
			}
		}
	}

	// `regit --help` (or `-h` with no verb) prints the terse command listing.
	if wantsHelp(args) {
		fmt.Print(capability.HelpTerse())
		return nil
	}

	// Unknown top-level flag: reject clearly rather than silently launching
	// the TUI. Known flags (--repo, --help, --version) have already been
	// handled or extracted above.
	if err := rejectUnknownFlag(args); err != nil {
		return err
	}

	// Everything else: launch the interactive TUI on the requested repo.
	return launchTUI(repoDir)
}

// launchTUI opens the repo at repoDir, loads config, and starts the full-screen
// TUI. It returns when the user quits (bubbletea restores the terminal on exit).
func launchTUI(repoDir string) error {
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
