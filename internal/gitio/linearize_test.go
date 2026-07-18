package gitio

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/wlame/regit/internal/gittest"
)

// mergeRepo builds: main c1 — side branch (s1 by Alice, s2 by Bob) — merge
// --no-ff into main — c2 on main. Returns the repo and the merge hash.
func mergeRepo(t *testing.T) (*Repo, string) {
	t.Helper()
	dir := t.TempDir()
	run := func(env []string, args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = gittest.Env(dir, env...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	commit := func(name, msg string, extraEnv ...string) {
		run(nil, "add", "-A")
		env := append([]string{
			"GIT_AUTHOR_DATE=2026-06-05T14:00:00+00:00",
			"GIT_COMMITTER_DATE=2026-06-05T14:00:00+00:00",
		}, extraEnv...)
		run(env, "commit", "-q", "--allow-empty", "-m", msg)
		_ = name
	}
	run(nil, "init", "-q")
	run(nil, "symbolic-ref", "HEAD", "refs/heads/main")
	run(nil, "config", "user.name", "Integrator")
	run(nil, "config", "user.email", "int@example.com")
	run(nil, "config", "commit.gpgsign", "false")
	commit("c1", "Base commit.")
	run(nil, "checkout", "-q", "-b", "feature/login")
	commit("s1", "Add login form.",
		"GIT_AUTHOR_NAME=Alice", "GIT_AUTHOR_EMAIL=alice@example.com")
	commit("s2", "Wire the backend.",
		"GIT_AUTHOR_NAME=Bob", "GIT_AUTHOR_EMAIL=bob@example.com")
	run(nil, "checkout", "-q", "main")
	run([]string{
		"GIT_AUTHOR_DATE=2026-06-05T15:00:00+00:00",
		"GIT_COMMITTER_DATE=2026-06-05T15:00:00+00:00",
	}, "merge", "-q", "--no-ff", "feature/login", "-m", "Merge branch 'feature/login'")
	mergeHash := run(nil, "rev-parse", "HEAD")
	commit("c2", "Follow-up on main.")
	repo, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return repo, mergeHash
}

func TestSideCommits_oldestFirstWithAuthors(t *testing.T) {
	repo, mergeHash := mergeRepo(t)
	side, err := repo.SideCommits(mergeHash)
	if err != nil {
		t.Fatal(err)
	}
	if len(side) != 2 {
		t.Fatalf("side = %d commits, want 2: %+v", len(side), side)
	}
	if side[0].Subject != "Add login form." || side[0].AuthorEmail != "alice@example.com" {
		t.Fatalf("side[0] = %+v, want Alice's oldest commit", side[0])
	}
	if side[1].Subject != "Wire the backend." || side[1].AuthorEmail != "bob@example.com" {
		t.Fatalf("side[1] = %+v, want Bob's newest commit", side[1])
	}
	for _, s := range side {
		if s.Hash == mergeHash {
			t.Fatal("the merge itself must be excluded from its side set")
		}
	}
}

func TestRefsContaining_reportsOtherRefsOnly(t *testing.T) {
	repo, mergeHash := mergeRepo(t)
	side, err := repo.SideCommits(mergeHash)
	if err != nil {
		t.Fatal(err)
	}
	// The side tip is still the feature/login branch head, so that branch
	// contains it; main also contains it but must be excluded.
	refs, err := repo.RefsContaining(side[len(side)-1].Hash, "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0] != "feature/login" {
		t.Fatalf("refs = %v, want [feature/login]", refs)
	}
}
