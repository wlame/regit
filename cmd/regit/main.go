// Command regit is a terminal UI for safely rewriting the commit and author
// dates of the current branch's history — spreading clustered commits across a
// calendar, with strategy presets, while guaranteeing file contents never change.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
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
		os.Exit(capability.ExitCode(err))
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

// invokedAsGitRe reports whether the process was launched through the git-re
// shim: git's external-command protocol execs the `git-re` executable for
// `git re`, so argv[0] carries that name (possibly a full path, and .exe on
// Windows). Hints and help then teach the `git re` form the user typed.
func invokedAsGitRe(argv0 string) bool {
	base := strings.TrimSuffix(filepath.Base(argv0), ".exe")
	return base == gitShimBase
}

// gitShimBase mirrors capability's shim name; kept as a constant here because
// main only needs the basename comparison.
const gitShimBase = "git-re"

// wantsHelp reports whether args (the args after the verb) contain --help or -h.
func wantsHelp(args []string) bool {
	for _, a := range args {
		if a == "--help" || a == "-h" {
			return true
		}
	}
	return false
}

// wantsLLMHelp reports whether args contain --llm-help or --llm-help=format,
// returning the format (default "text") and whether the flag is present.
func wantsLLMHelp(args []string) (string, bool) {
	for _, a := range args {
		if a == "--llm-help" {
			return "text", true
		}
		if strings.HasPrefix(a, "--llm-help=") {
			format := strings.TrimPrefix(a, "--llm-help=")
			return format, true
		}
	}
	return "", false
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

// unknownCommand reports the leading verb to reject when it looks like a
// subcommand (a non-flag first token) but is not a registered verb — so a typo
// like `regit redat` errors instead of silently launching the TUI. reject is
// false when there is no leading verb token (a flag, or no args), leaving the
// TUI / flag handling to proceed.
func unknownCommand(args []string) (verb string, reject bool) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return "", false
	}
	if _, known := capability.Lookup(args[0]); known {
		return "", false
	}
	return args[0], true
}

func run() error {
	// Detect launch-through-git first so every surface (hints, help, errors)
	// renders the invocation form the user actually used.
	if invokedAsGitRe(os.Args[0]) {
		capability.SetInvocation("git re")
	}

	// Handle `regit --version` before anything else.
	if wantsVersion(os.Args[1:]) {
		fmt.Println("regit", version)
		return nil
	}

	// Handle `regit --llm-help[=format]` before anything else.
	if format, ok := wantsLLMHelp(os.Args[1:]); ok {
		doc, err := capability.LLMHelp(format)
		if err != nil {
			return err
		}
		w := os.Stdout
		_, _ = io.WriteString(w, doc)
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

		// Route all registry verbs through dispatch (split and reword now have
		// Run wired and use the --execute convention; legacy --yes/--dry-run
		// flags are rewritten by DeprecatedRewrite inside dispatch).
		if _, ok := capability.Lookup(verb); ok {
			return dispatch(verb, repoDir, rest)
		}
	}

	// `regit --help` (or `-h` with no verb) prints the terse command listing.
	if wantsHelp(args) {
		fmt.Print(capability.HelpTerse())
		return nil
	}

	// A verb-shaped first token that is not a registered command is a typo;
	// reject it rather than silently launching the TUI.
	if verb, reject := unknownCommand(args); reject {
		return fmt.Errorf("unknown command %q (try regit --help)", verb)
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
