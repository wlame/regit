// internal/capability/run_squash_test.go
package capability

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wlame/regit/internal/config"
	"github.com/wlame/regit/internal/gitio"
)

// squashTestRepo builds a linear repo of n commits and returns the repo plus its
// commits (root-first). Used by the capability squash tests.
func squashTestRepo(t *testing.T, n int) (*gitio.Repo, []string) {
	t.Helper()
	dir := t.TempDir()
	run := func(env []string, args ...string) {
		c := exec.Command("git", args...)
		c.Dir = dir
		if env != nil {
			c.Env = append(os.Environ(), env...)
		}
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run(nil, "init", "-q")
	run(nil, "symbolic-ref", "HEAD", "refs/heads/main")
	run(nil, "config", "user.name", "wlame")
	run(nil, "config", "user.email", "vvlame@gmail.com")
	run(nil, "config", "commit.gpgsign", "false")
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		name := "f" + string(rune('0'+i)) + ".txt"
		os.WriteFile(filepath.Join(dir, name), []byte("x"+name+"\n"), 0o644)
		run(nil, "add", ".")
		d := base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339)
		run([]string{"GIT_AUTHOR_DATE=" + d, "GIT_COMMITTER_DATE=" + d}, "commit", "-q", "-m", "commit "+name)
	}
	repo, err := gitio.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	hashes := make([]string, len(commits))
	for i, c := range commits {
		hashes[i] = c.Hash
	}
	return repo, hashes
}

func TestParseSquashSpec_gitSemanticsExcludeA(t *testing.T) {
	repo, h := squashTestRepo(t, 4) // h[0]..h[3] root-first
	commits, _ := repo.ReadCommits()
	// h0..h2 means commits AFTER h0 through h2 => {h1, h2}, NOT h0.
	sel, err := parseSquashSpec(repo, commits, h[0]+".."+h[2])
	if err != nil {
		t.Fatal(err)
	}
	if sel[h[0]] {
		t.Error("A..B must EXCLUDE A (git semantics)")
	}
	if !sel[h[1]] || !sel[h[2]] {
		t.Error("A..B must include the commits after A through B")
	}
}

func TestParseSquashSpec_caretIncludesA(t *testing.T) {
	repo, h := squashTestRepo(t, 4)
	commits, _ := repo.ReadCommits()
	sel, err := parseSquashSpec(repo, commits, h[1]+"^.."+h[2]) // include h1
	if err != nil {
		t.Fatal(err)
	}
	if !sel[h[1]] || !sel[h[2]] {
		t.Error("A^..B should include A")
	}
}

func TestParseSquashSpec_commaListAndSingleRef(t *testing.T) {
	repo, h := squashTestRepo(t, 5)
	commits, _ := repo.ReadCommits()
	// Use h[1]^..h[2] (h[1]^=h[0], so selects {h1,h2}) plus singleton h[4].
	// h[0]^..h[1] would be the natural choice but h[0] is the root (no parent).
	spec := h[1] + "^.." + h[2] + " , " + h[4] // chain {h1,h2} plus singleton h4
	sel, err := parseSquashSpec(repo, commits, spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{h[1], h[2], h[4]} {
		if !sel[want] {
			t.Errorf("expected %s selected", want)
		}
	}
	if sel[h[0]] || sel[h[3]] {
		t.Error("unlisted commits must not be selected")
	}
}

func TestParseSquashSpec_badRefErrors(t *testing.T) {
	repo, _ := squashTestRepo(t, 2)
	commits, _ := repo.ReadCommits()
	if _, err := parseSquashSpec(repo, commits, "deadbeefdeadbeef"); err == nil {
		t.Error("an unresolvable ref must error")
	}
}

func TestRunSquash_dryRunNoLLMUsesFirstCommitMessage(t *testing.T) {
	repo, h := squashTestRepo(t, 4)
	var out bytes.Buffer
	ctx := &RunContext{Repo: repo, Cfg: config.Config{}, Stdout: &out, Stderr: &out, Now: time.Unix(1717689600, 0)}
	// Squash h2,h3 (include h2 via caret). No --execute => dry-run.
	a := newParsedArgs(map[string]any{}, []string{h[1] + "^.." + h[3]})
	if err := runSquash(ctx, a); err != nil {
		t.Fatalf("runSquash: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "into 1") && !strings.Contains(s, "1 commit") {
		t.Errorf("expected a 1-chain summary, got:\n%s", s)
	}
	if !strings.Contains(s, "dry run") {
		t.Errorf("dry-run notice missing:\n%s", s)
	}
	// History unchanged after a dry run.
	commits, _ := repo.ReadCommits()
	if len(commits) != 4 {
		t.Fatalf("dry run must not change history; got %d commits", len(commits))
	}
}

func TestRunSquash_executeCollapsesChain(t *testing.T) {
	repo, h := squashTestRepo(t, 4)
	var out bytes.Buffer
	ctx := &RunContext{Repo: repo, Cfg: config.Config{}, Stdout: &out, Stderr: &out, Now: time.Unix(1717689600, 0)}
	a := newParsedArgs(map[string]any{"execute": true}, []string{h[2] + "^.." + h[3]})
	if err := runSquash(ctx, a); err != nil {
		t.Fatalf("runSquash --execute: %v", err)
	}
	commits, _ := repo.ReadCommits()
	if len(commits) != 3 {
		t.Fatalf("expected 3 commits after squash, got %d", len(commits))
	}
}

func TestRunSquash_noChainsIsNoOp(t *testing.T) {
	repo, h := squashTestRepo(t, 4)
	var out bytes.Buffer
	ctx := &RunContext{Repo: repo, Cfg: config.Config{}, Stdout: &out, Stderr: &out, Now: time.Unix(1, 0)}
	// Two non-adjacent singletons => no chains.
	a := newParsedArgs(map[string]any{}, []string{h[0] + "," + h[2]})
	if err := runSquash(ctx, a); err != nil {
		t.Fatalf("runSquash: %v", err)
	}
	if !strings.Contains(out.String(), "Nothing to squash") {
		t.Errorf("expected no-op message, got:\n%s", out.String())
	}
}

func TestRunSquash_missingSpecIsUsageError(t *testing.T) {
	repo, _ := squashTestRepo(t, 2)
	var out bytes.Buffer
	ctx := &RunContext{Repo: repo, Cfg: config.Config{}, Stdout: &out, Stderr: &out, Now: time.Unix(1, 0)}
	err := runSquash(ctx, newParsedArgs(map[string]any{}, nil))
	if err == nil || ExitCode(err) != 1 {
		t.Errorf("missing spec should be a usage error (exit 1), got %v (code %d)", err, ExitCode(err))
	}
}

// fakeSquashBackend is a deterministic llm.Backend for prefill tests.
type fakeSquashBackend struct{ resp string }

func (f fakeSquashBackend) Generate(ctx context.Context, system, user string, schema []byte, validate func(string) error) (string, error) {
	if err := validate(f.resp); err != nil {
		return "", err
	}
	return f.resp, nil
}
func (f fakeSquashBackend) Name() string { return "fake:squash" }

func TestRunSquash_llmPrefillOverridesMessage(t *testing.T) {
	repo, h := squashTestRepo(t, 4)
	var out bytes.Buffer
	ctx := &RunContext{
		Repo: repo, Cfg: config.Config{}, Stdout: &out, Stderr: &out,
		Now:     time.Unix(1717689600, 0),
		Backend: fakeSquashBackend{resp: `{"subject":"Combined summary","body":""}`},
	}
	a := newParsedArgs(map[string]any{"llm": "ollama/x"}, []string{h[2] + "^.." + h[3]})
	if err := runSquash(ctx, a); err != nil {
		t.Fatalf("runSquash: %v", err)
	}
	if !strings.Contains(out.String(), "Combined summary") {
		t.Errorf("LLM subject should appear in the preview, got:\n%s", out.String())
	}
}
