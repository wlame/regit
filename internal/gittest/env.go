// Package gittest provides the hermetic git environment every test fixture
// runs its git commands with. A fixture using the ambient environment inherits
// the developer's real git config (signing, hooksPath, showSignature, …) and
// any exported GIT_* variable — the historical source of host-dependent test
// flakes. This package existed first as a private helper in the ui tests; it
// is shared so gitio/safety/capability/split/squash/reword fixtures cannot
// drift back to leaky environments.
package gittest

import (
	"os"
	"strings"
)

// Env returns the environment for git invocations against a throwaway repo
// rooted at home: a private HOME, global/system config pinned to os.DevNull,
// and every inherited GIT_* var dropped, so a fixture never reads or writes
// the developer's real git config and can never be pointed at the wrong
// repository. Identity is pinned via env so a missing global config never
// breaks commits. extra carries per-call vars such as GIT_AUTHOR_DATE /
// GIT_COMMITTER_DATE (appended last, so they win over the pinned defaults).
func Env(home string, extra ...string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GIT_") {
			continue // drop inherited GIT_* so a fixture can't be aimed at another repo
		}
		env = append(env, kv)
	}
	env = append(env,
		"HOME="+home,
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_SYSTEM="+os.DevNull,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=wlame", "GIT_AUTHOR_EMAIL=vvlame@gmail.com",
		"GIT_COMMITTER_NAME=wlame", "GIT_COMMITTER_EMAIL=vvlame@gmail.com",
		// Disable background maintenance: git's default maintenance.auto=true
		// spawns a detached process after commits that races with subsequent
		// git invocations in the same fixture, producing "invalid object"
		// errors when building many commits.
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=maintenance.auto",
		"GIT_CONFIG_VALUE_0=false",
	)
	return append(env, extra...)
}
