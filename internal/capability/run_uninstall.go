package capability

import (
	"fmt"
	"os"
	"path/filepath"
)

// runUninstall removes the `git re` shim if — and only if — it points at regit.
func runUninstall(ctx *RunContext, a ParsedArgs) error {
	return runUninstallEnv(defaultInstallEnv, ctx, a)
}

// runUninstallEnv is runUninstall with an injectable environment.
func runUninstallEnv(env installEnv, ctx *RunContext, _ ParsedArgs) error {
	self, err := resolvedSelf(env)
	if err != nil {
		return err
	}
	link, err := env.lookPath(gitShimName)
	if err != nil {
		fmt.Fprintf(ctx.Stdout, "Not installed: no %s on $PATH.\n", gitShimName)
		return nil
	}
	// Only ever delete a git-re that provably resolves to THIS regit binary; a
	// foreign file (or dangling link) is reported and left alone.
	resolved, err := filepath.EvalSymlinks(link)
	if err != nil || resolved != self {
		return runtimeErr("%s does not point at this regit binary; not removing it", link)
	}
	if err := os.Remove(link); err != nil {
		return runtimeErr("remove %s: %v", link, err)
	}
	fmt.Fprintf(ctx.Stdout, "Removed %s\n", link)
	return nil
}
