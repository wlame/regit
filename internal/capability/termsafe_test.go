package capability

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wlame/regit/internal/config"
	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/gittest"
)

// hostileName carries a CSI colour sequence and an OSC window-title write —
// both fully attacker-controlled, since a commit author name is arbitrary bytes.
const hostileName = "Ev\x1b[31mil\x1b]0;PWNED\x07"

// hostileRepo builds a two-commit repository whose author and committer
// identity carry terminal escapes. A commit identity is arbitrary bytes chosen
// by whoever made the commit, so this is ordinary untrusted input.
func hostileRepo(t *testing.T) *gitio.Repo {
	t.Helper()
	dir := t.TempDir()
	run := func(extra []string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(gittest.Env(dir), extra...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	ident := []string{
		"GIT_AUTHOR_NAME=" + hostileName, "GIT_AUTHOR_EMAIL=e@x.y",
		"GIT_COMMITTER_NAME=" + hostileName, "GIT_COMMITTER_EMAIL=e@x.y",
	}
	run(nil, "init", "-q")
	run(nil, "symbolic-ref", "HEAD", "refs/heads/main")
	for i := 1; i <= 2; i++ {
		name := filepath.Join(dir, fmt.Sprintf("f%d.txt", i))
		if err := os.WriteFile(name, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		run(nil, "add", "-A")
		run(ident, "commit", "-q", "-m", fmt.Sprintf("Commit %d.", i))
	}
	repo, err := gitio.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

// TestPreviewWritersEmitNoEscapeBytes pins the invariant AGENTS.md states:
// every render site that emits a commit subject, author, path, body, or LLM
// subject routes through internal/termsafe. It was NOT holding for author
// identity — a crafted author name reached stdout intact and could rewrite the
// terminal title or drive an OSC-52 clipboard write.
//
// Asserting on the raw ESC BYTE rather than on a specific call site is
// deliberate: a future edit that swaps %q for %s at any of these sites reopens
// the hole silently, and this test closes on the OUTPUT, not the mechanism.
func TestPreviewWritersEmitNoEscapeBytes(t *testing.T) {
	repo := hostileRepo(t)
	var out bytes.Buffer
	ctx := &RunContext{
		Repo: repo, Cfg: config.Default(),
		Stdout: &out, Stderr: &out, Now: time.Unix(1000, 0),
	}

	a, err := Parse(mustLookup(t, "reauthor"), []string{"--name=Safe", "--email=s@x.y"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := runReauthor(ctx, a); err != nil {
		t.Fatalf("runReauthor: %v", err)
	}

	got := out.String()
	if strings.ContainsRune(got, 0x1b) {
		t.Errorf("reauthor preview emitted a raw ESC byte:\n%q", got)
	}
	if strings.Contains(got, "\x07") {
		t.Errorf("reauthor preview emitted a BEL (OSC terminator):\n%q", got)
	}
	// The identity must still be RECOGNISABLE — sanitizing must strip the
	// escapes, not blank the field, or the preview stops being useful.
	if !strings.Contains(got, "Evil") {
		t.Errorf("reauthor preview lost the author name entirely:\n%q", got)
	}
}
