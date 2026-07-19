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

// octopusRepo builds: main c1 — feat-a (A1) and feat-b (B1, then a nested
// --no-ff merge of feat-b-sub's Sub1) — an octopus merge of feat-a and feat-b
// into main — a follow-up commit on main. Exercises SideCommits' "including
// nested merges" claim and ApplyLinearize's fold-to-single-parent behavior
// on a merge with more than two parents. Returns the repo and the octopus
// merge hash.
func octopusRepo(t *testing.T) (*Repo, string) {
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
	commit := func(msg, date string, extraEnv ...string) {
		run(nil, "add", "-A")
		env := append([]string{
			"GIT_AUTHOR_DATE=" + date,
			"GIT_COMMITTER_DATE=" + date,
		}, extraEnv...)
		run(env, "commit", "-q", "--allow-empty", "-m", msg)
	}
	merge := func(date string, args ...string) string {
		env := []string{"GIT_AUTHOR_DATE=" + date, "GIT_COMMITTER_DATE=" + date}
		run(env, args...)
		return run(nil, "rev-parse", "HEAD")
	}
	run(nil, "init", "-q")
	run(nil, "symbolic-ref", "HEAD", "refs/heads/main")
	run(nil, "config", "user.name", "Integrator")
	run(nil, "config", "user.email", "int@example.com")
	run(nil, "config", "commit.gpgsign", "false")
	commit("Base commit.", "2026-06-05T10:00:00+00:00")

	run(nil, "checkout", "-q", "-b", "feat-a")
	commit("Add A1.", "2026-06-05T11:00:00+00:00")

	run(nil, "checkout", "-q", "main")
	run(nil, "checkout", "-q", "-b", "feat-b")
	commit("Add B1.", "2026-06-05T11:30:00+00:00")

	run(nil, "checkout", "-q", "-b", "feat-b-sub")
	commit("Add Sub1.", "2026-06-05T11:45:00+00:00")

	run(nil, "checkout", "-q", "feat-b")
	merge("2026-06-05T12:00:00+00:00",
		"merge", "-q", "--no-ff", "feat-b-sub", "-m", "Merge branch 'feat-b-sub'")

	run(nil, "checkout", "-q", "main")
	octopusHash := merge("2026-06-05T13:00:00+00:00",
		"merge", "-q", "--no-ff", "feat-a", "feat-b",
		"-m", "Merge branches 'feat-a' and 'feat-b'")

	commit("Post.", "2026-06-05T14:00:00+00:00")

	repo, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return repo, octopusHash
}

// TestSideCommits_octopusAndNestedMerges pins the doc comment's "including
// nested merges" claim: an octopus merge's side set must contain every commit
// unique to EACH non-first parent, and a nested --no-ff merge inside one of
// those branches must contribute both its own merge commit and the sub-branch
// commit it merged in, while the octopus merge itself is excluded.
func TestSideCommits_octopusAndNestedMerges(t *testing.T) {
	repo, octopusHash := octopusRepo(t)
	side, err := repo.SideCommits(octopusHash)
	if err != nil {
		t.Fatal(err)
	}
	if len(side) != 4 {
		t.Fatalf("side = %d commits, want 4: %+v", len(side), side)
	}
	wantSubjects := map[string]bool{
		"Add A1.":                   false,
		"Add B1.":                   false,
		"Add Sub1.":                 false,
		"Merge branch 'feat-b-sub'": false,
	}
	for _, s := range side {
		if s.Hash == octopusHash {
			t.Fatal("the octopus merge itself must be excluded from its side set")
		}
		if _, ok := wantSubjects[s.Subject]; !ok {
			t.Fatalf("unexpected side commit %+v", s)
		}
		wantSubjects[s.Subject] = true
	}
	for subj, found := range wantSubjects {
		if !found {
			t.Fatalf("side set missing expected commit %q: %+v", subj, side)
		}
	}
}

// TestApplyLinearize_octopusFoldsToSingleParent folds an octopus merge (three
// parents) into an ordinary commit and pins the same guarantees the two-way
// case gets: the fold has exactly one parent, its tree matches the octopus
// merge's tree, and the resulting chain has no merges left.
func TestApplyLinearize_octopusFoldsToSingleParent(t *testing.T) {
	repo, octopusHash := octopusRepo(t)
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	side, err := repo.SideCommits(octopusHash)
	if err != nil {
		t.Fatal(err)
	}
	plan := model.BuildLinearizePlan(commits, map[string][]model.SideCommit{octopusHash: side})

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

	fold := remap.Folded[octopusHash]
	if fold == "" {
		t.Fatalf("remap missing fold for octopus merge: %+v", remap)
	}
	parents := strings.Fields(git("log", "-1", "--format=%P", fold))
	if len(parents) != 1 {
		t.Fatalf("fold %s has %d parent(s) %v, want exactly 1", short(fold), len(parents), parents)
	}
	if git("rev-parse", fold+"^{tree}") != git("rev-parse", octopusHash+"^{tree}") {
		t.Fatal("fold's tree must equal the octopus merge's tree")
	}
	if got := git("rev-list", "--min-parents=2", "--count", newHead); got != "0" {
		t.Fatalf("new chain still has %s merge(s)", got)
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
