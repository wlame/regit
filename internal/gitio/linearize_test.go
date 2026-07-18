package gitio

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/wlame/regit/internal/gittest"
	"github.com/wlame/regit/internal/model"
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

func TestApplyLinearize_foldsMergePreservingEveryTree(t *testing.T) {
	repo, mergeHash := mergeRepo(t)
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	// commits is root-first first-parent: [c1, merge, c2].
	if len(commits) != 3 || !commits[1].IsMerge {
		t.Fatalf("unexpected chain: %+v", commits)
	}
	side, err := repo.SideCommits(mergeHash)
	if err != nil {
		t.Fatal(err)
	}
	plan := model.BuildLinearizePlan(commits, map[string][]model.SideCommit{mergeHash: side})

	newHead, remap, err := repo.ApplyLinearize(commits, plan, DropSignatures)
	if err != nil {
		t.Fatal(err)
	}

	git := func(args ...string) string {
		out, err := repo.git(args...)
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return strings.TrimSpace(out)
	}
	// No ref moved: the branch still points at the original head.
	if git("rev-parse", "main") != commits[2].Hash {
		t.Fatal("ApplyLinearize must not move any ref")
	}
	// The new chain has no merges and the same length.
	if got := git("rev-list", "--min-parents=2", "--count", newHead); got != "0" {
		t.Fatalf("new chain still has %s merge(s)", got)
	}
	if got := git("rev-list", "--count", newHead); got != "3" {
		t.Fatalf("new chain length = %s, want 3 (1:1 rewrite)", got)
	}
	// Tree preservation, per commit: c1 untouched, fold and c2 tree-identical.
	if remap.Passthrough[commits[2].Hash] == "" || remap.Folded[mergeHash] == "" {
		t.Fatalf("remap incomplete: %+v", remap)
	}
	for old, new := range remap.Folded {
		if git("rev-parse", old+"^{tree}") != git("rev-parse", new+"^{tree}") {
			t.Fatalf("fold %s changed the tree", short(old))
		}
	}
	for old, new := range remap.Passthrough {
		if git("rev-parse", old+"^{tree}") != git("rev-parse", new+"^{tree}") {
			t.Fatalf("passthrough %s changed the tree", short(old))
		}
	}
	// c1 (before the first fold) keeps its original hash.
	if git("rev-parse", newHead+"~2") != commits[0].Hash {
		t.Fatal("commits before the first fold must keep their hashes")
	}
	// The folded commit carries the synthesized message with trailers.
	foldMsg := git("log", "-1", "--format=%B", remap.Folded[mergeHash])
	if !strings.Contains(foldMsg, "login: Add login form.") ||
		!strings.Contains(foldMsg, "Co-authored-by: Alice <alice@example.com>") {
		t.Fatalf("fold message wrong:\n%s", foldMsg)
	}
	// The fold keeps all merge metadata: author/committer identities and dates.
	merged := remap.Folded[mergeHash]
	for _, fmt := range []string{"%an", "%ae", "%cn", "%ce", "%cI", "%aI"} {
		if git("log", "-1", "--format="+fmt, merged) !=
			git("log", "-1", "--format="+fmt, mergeHash) {
			t.Fatalf("fold %s: metadata %s differs from merge", short(merged), fmt)
		}
	}
}

func TestApplyLinearize_twoFoldsThreadParents(t *testing.T) {
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
	commit := func(msg string, extraEnv ...string) {
		run(nil, "add", "-A")
		env := append([]string{
			"GIT_AUTHOR_DATE=2026-06-05T14:00:00+00:00",
			"GIT_COMMITTER_DATE=2026-06-05T14:00:00+00:00",
		}, extraEnv...)
		run(env, "commit", "-q", "--allow-empty", "-m", msg)
	}
	run(nil, "init", "-q")
	run(nil, "symbolic-ref", "HEAD", "refs/heads/main")
	run(nil, "config", "user.name", "Int")
	run(nil, "config", "user.email", "int@ex.com")
	run(nil, "config", "commit.gpgsign", "false")
	commit("Base.")
	// First side branch and merge.
	run(nil, "checkout", "-q", "-b", "feat-a")
	commit("A1.")
	run(nil, "checkout", "-q", "main")
	run([]string{"GIT_AUTHOR_DATE=2026-06-05T15:00:00+00:00",
		"GIT_COMMITTER_DATE=2026-06-05T15:00:00+00:00"},
		"merge", "-q", "--no-ff", "feat-a", "-m", "Merge feat-a")
	m1 := run(nil, "rev-parse", "HEAD")
	// Second side branch and merge.
	run(nil, "checkout", "-q", "-b", "feat-b")
	commit("B1.")
	run(nil, "checkout", "-q", "main")
	run([]string{"GIT_AUTHOR_DATE=2026-06-05T16:00:00+00:00",
		"GIT_COMMITTER_DATE=2026-06-05T16:00:00+00:00"},
		"merge", "-q", "--no-ff", "feat-b", "-m", "Merge feat-b")
	m2 := run(nil, "rev-parse", "HEAD")
	// Follow-up on main.
	commit("Post.")
	repo, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) string {
		out, err := repo.git(args...)
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return strings.TrimSpace(out)
	}
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	// commits is [Base, M1, M2, Post]; find side commits for both merges.
	side1, err := repo.SideCommits(m1)
	if err != nil {
		t.Fatal(err)
	}
	side2, err := repo.SideCommits(m2)
	if err != nil {
		t.Fatal(err)
	}
	plan := model.BuildLinearizePlan(commits, map[string][]model.SideCommit{
		m1: side1,
		m2: side2,
	})
	newHead, remap, err := repo.ApplyLinearize(commits, plan, DropSignatures)
	if err != nil {
		t.Fatal(err)
	}
	// No merges in the new chain.
	if got := git("rev-list", "--min-parents=2", "--count", newHead); got != "0" {
		t.Fatalf("new chain has %s merge(s), want 0", got)
	}
	// Second fold's parent is first fold's new hash.
	if git("rev-parse", remap.Folded[m2]+"^") != remap.Folded[m1] {
		t.Fatalf("second fold's parent %s != first fold %s",
			git("rev-parse", remap.Folded[m2]+"^"), remap.Folded[m1])
	}
	// Both folds preserve trees.
	for _, mergeHash := range []string{m1, m2} {
		if git("rev-parse", mergeHash+"^{tree}") != git("rev-parse", remap.Folded[mergeHash]+"^{tree}") {
			t.Fatalf("fold %s changed tree", short(mergeHash))
		}
	}
}

func TestApplyLinearize_errorBranches(t *testing.T) {
	repo, _ := mergeRepo(t)
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	// Empty plan → error.
	_, _, err = repo.ApplyLinearize(commits, model.LinearizePlan{}, DropSignatures)
	if err == nil || !strings.Contains(err.Error(), "no folds") {
		t.Fatalf("empty plan error = %v, want 'no folds'", err)
	}
	// Plan with unknown merge hash → error.
	badPlan := model.LinearizePlan{
		Folds: []model.LinearizeFold{{
			Merge:    model.Commit{Hash: "deadbeef", IsMerge: true},
			NewTitle: "Fold",
			NewBody:  "",
		}},
	}
	_, _, err = repo.ApplyLinearize(commits, badPlan, DropSignatures)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("unknown merge error = %v, want 'not found'", err)
	}
}
