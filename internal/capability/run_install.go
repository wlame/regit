package capability

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// gitShimName is the executable name git's external-command protocol looks up
// when the user types `git re`: git searches $PATH for `git-<subcommand>`.
// On Windows both git and exec.LookPath resolve executables by extension, so
// the shim must carry .exe there — an extensionless link would install fine
// but be invisible to every later lookup (including our own uninstall).
var gitShimName = func() string {
	if runtime.GOOS == "windows" {
		return "git-re.exe"
	}
	return "git-re"
}()

// installEnv abstracts the process and $PATH lookups the installer performs, so
// unit tests can point it at temp directories instead of the real environment.
type installEnv struct {
	executable func() (string, error)       // path of the running binary (os.Executable)
	lookPath   func(string) (string, error) // $PATH search (exec.LookPath)
}

// defaultInstallEnv is the production wiring used by the registry Runs.
var defaultInstallEnv = installEnv{executable: os.Executable, lookPath: exec.LookPath}

// runInstall registers regit as `git re`. Unlike history-rewriting verbs it is
// exempt from the --execute convention: creating one symlink is idempotent and
// trivially reversible (regit uninstall), so it applies immediately.
func runInstall(ctx *RunContext, a ParsedArgs) error {
	return runInstallEnv(defaultInstallEnv, ctx, a)
}

// resolvedSelf returns the canonical path of the running binary. When regit is
// launched through the git-re symlink, /proc/self/exe (and the macOS
// equivalent) already resolves to the real file; EvalSymlinks covers the rest.
func resolvedSelf(env installEnv) (string, error) {
	self, err := env.executable()
	if err != nil {
		return "", runtimeErr("locate running binary: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(self)
	if err != nil {
		return "", runtimeErr("resolve running binary %s: %v", self, err)
	}
	return resolved, nil
}

// shimStateID classifies what currently occupies the git-re path.
type shimStateID int

const (
	shimMissing shimStateID = iota // nothing there
	shimOurs                       // resolves to the regit binary we would link
	shimForeign                    // a different program, plain file, or dangling link
)

// shimState inspects link and reports whether it is absent, already the shim we
// would create (resolves to resolvedTarget), or something we must not touch.
// A dangling symlink fails EvalSymlinks and is deliberately treated as foreign:
// we cannot prove it was ours, so replacing it requires --force.
func shimState(link, resolvedTarget string) shimStateID {
	if _, err := os.Lstat(link); err != nil {
		return shimMissing
	}
	resolved, err := filepath.EvalSymlinks(link)
	if err != nil {
		return shimForeign
	}
	if resolved == resolvedTarget {
		return shimOurs
	}
	return shimForeign
}

// runInstallEnv is runInstall with an injectable environment (see installEnv).
func runInstallEnv(env installEnv, ctx *RunContext, a ParsedArgs) error {
	self, err := resolvedSelf(env)
	if err != nil {
		return err
	}

	// The symlink is created NEXT TO the PATH-resolved regit binary: that
	// directory is on $PATH by construction, so `git re` works immediately
	// without us guessing at (or verifying) a separate install location.
	pathBin, err := env.lookPath("regit")
	if err != nil {
		return runtimeErr(
			"regit is not on your $PATH; put the binary on PATH first " +
				"(e.g. `just install`), then re-run `regit install`")
	}
	pathBin, err = filepath.Abs(pathBin)
	if err != nil {
		return runtimeErr("resolve %s: %v", pathBin, err)
	}
	resolvedPath, err := filepath.EvalSymlinks(pathBin)
	if err != nil {
		return runtimeErr("resolve %s: %v", pathBin, err)
	}
	if resolvedPath != self {
		fmt.Fprintf(ctx.Stderr,
			"warning: the regit on PATH (%s) is not the running binary (%s); linking the PATH copy\n",
			resolvedPath, self)
	}

	link := filepath.Join(filepath.Dir(pathBin), gitShimName)
	switch shimState(link, resolvedPath) {
	case shimOurs:
		fmt.Fprintf(ctx.Stdout, "Already installed: %s → %s\n", link, pathBin)
		return nil
	case shimForeign:
		if !a.Bool("force") {
			return runtimeErr(
				"%s already exists and does not point at regit; re-run with --force to replace it",
				link)
		}
		if err := os.Remove(link); err != nil {
			return runtimeErr("replace %s: %v", link, err)
		}
	}

	if err := os.Symlink(pathBin, link); err != nil {
		return runtimeErr(
			"create %s: %v (is %s writable? try sudo, or move regit to a user-writable PATH directory)",
			link, err, filepath.Dir(pathBin))
	}

	// A different git-re earlier on $PATH would shadow the link we just made.
	if found, lookErr := env.lookPath(gitShimName); lookErr == nil {
		if rf, resolveErr := filepath.EvalSymlinks(found); resolveErr != nil || rf != resolvedPath {
			fmt.Fprintf(ctx.Stderr, "warning: a different %s at %s shadows the new link on $PATH\n",
				gitShimName, found)
		}
	}

	fmt.Fprintf(ctx.Stdout, "Installed: %s → %s\ntry: git re -h\n", link, pathBin)
	return nil
}
