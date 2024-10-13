package gitio

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wlame/ghist/internal/model"
)

// testRepo builds a throwaway git repository with three commits, all stamped at
// the same instant (the "clustered commits" situation ghist exists to fix), each
// adding a distinct file so we can later prove the files survive a rewrite.
func testRepo(t *testing.T) (*Repo, string) {
	t.Helper()
	dir := t.TempDir()

	run := func(env []string, args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), env...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}

	run(nil, "init", "-q")
	run(nil, "symbolic-ref", "HEAD", "refs/heads/main")
	run(nil, "config", "user.name", "wlame")
	run(nil, "config", "user.email", "vvlame@gmail.com")
	run(nil, "config", "commit.gpgsign", "false")

	clustered := "2026-06-05T14:00:00+00:00"
	dateEnv := []string{
		"GIT_AUTHOR_DATE=" + clustered,
		"GIT_COMMITTER_DATE=" + clustered,
	}
	for i, name := range []string{"alpha", "beta", "gamma"} {
		path := filepath.Join(dir, name+".txt")
		if err := os.WriteFile(path, []byte(name+" contents\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		run(nil, "add", name+".txt")
		run(dateEnv, "commit", "-q", "-m", "commit "+name, "-m", "body of commit number "+string(rune('0'+i)))
	}

	repo, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return repo, dir
}

func mustParse(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return tm
}

func TestReadCommits_returnsRootFirstWithMetadata(t *testing.T) {
	repo, _ := testRepo(t)
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 3 {
		t.Fatalf("expected 3 commits, got %d", len(commits))
	}
	// Root-first: oldest commit ("alpha") is index 0.
	if commits[0].Subject != "commit alpha" {
		t.Errorf("expected root-first order, got first subject %q", commits[0].Subject)
	}
	if commits[2].Subject != "commit gamma" {
		t.Errorf("expected newest last, got %q", commits[2].Subject)
	}
	// Parent linkage: each commit's first parent is the previous one's hash.
	for i := 1; i < len(commits); i++ {
		if commits[i].ParentHashes[0] != commits[i-1].Hash {
			t.Errorf("commit %d parent mismatch", i)
		}
	}
	if !commits[0].HasBody() {
		t.Error("expected commit to have a body")
	}
}

func TestReplay_changesDatesButNotFiles(t *testing.T) {
	repo, dir := testRepo(t)
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	oldHead, _ := repo.ResolveRef("HEAD")

	// Spread the three commits across three consecutive days.
	plan := model.NewPlan(commits)
	dates := []string{
		"2026-03-01T10:00:00+00:00",
		"2026-03-02T11:00:00+00:00",
		"2026-03-03T12:00:00+00:00",
	}
	for i, c := range commits {
		d := mustParse(t, dates[i])
		plan.SetEdit(c.Hash, model.Edit{NewAuthorDate: &d, NewCommitDate: &d})
	}

	newHead, err := repo.Replay(plan, DropSignatures)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}

	// The crucial guarantee: no file content changed.
	if err := repo.VerifyTreesUnchanged(oldHead, newHead); err != nil {
		t.Fatalf("trees changed during rewrite: %v", err)
	}

	// Move the branch and re-read to confirm the new dates stuck.
	if err := repo.UpdateRef(BranchRef("main"), newHead, oldHead); err != nil {
		t.Fatalf("UpdateRef: %v", err)
	}
	rewritten, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	for i, c := range rewritten {
		want := mustParse(t, dates[i])
		if !c.OrigAuthorDate.Equal(want) {
			t.Errorf("commit %d author date = %s, want %s", i, c.OrigAuthorDate, want)
		}
	}

	// Files must still exist with original contents on disk after checkout.
	for _, name := range []string{"alpha", "beta", "gamma"} {
		data, err := os.ReadFile(filepath.Join(dir, name+".txt"))
		if err != nil {
			t.Errorf("file %s missing after rewrite: %v", name, err)
			continue
		}
		if string(data) != name+" contents\n" {
			t.Errorf("file %s contents changed: %q", name, data)
		}
	}
}

func TestReplay_dropBodyKeepsSubject(t *testing.T) {
	repo, _ := testRepo(t)
	commits, _ := repo.ReadCommits()
	oldHead, _ := repo.ResolveRef("HEAD")

	plan := model.NewPlan(commits)
	// Drop the body of the first commit only.
	plan.SetEdit(commits[0].Hash, model.Edit{DropBody: true})

	newHead, err := repo.Replay(plan, DropSignatures)
	if err != nil {
		t.Fatal(err)
	}
	repo.UpdateRef(BranchRef("main"), newHead, oldHead)

	rewritten, _ := repo.ReadCommits()
	if rewritten[0].Subject != "commit alpha" {
		t.Errorf("subject should be preserved, got %q", rewritten[0].Subject)
	}
	if rewritten[0].Body != "" {
		t.Errorf("body should be dropped, got %q", rewritten[0].Body)
	}
}

func TestReadState_reportsCleanRepo(t *testing.T) {
	repo, _ := testRepo(t)
	st, err := repo.ReadState()
	if err != nil {
		t.Fatal(err)
	}
	if st.Branch != "main" {
		t.Errorf("branch = %q, want main", st.Branch)
	}
	if st.IsDirty {
		t.Error("fresh repo should not be dirty")
	}
	if st.HasUpstream {
		t.Error("local-only repo should have no upstream")
	}
}

func TestReadState_detectsDirtyWorktree(t *testing.T) {
	repo, dir := testRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "alpha.txt"), []byte("modified\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := repo.ReadState()
	if err != nil {
		t.Fatal(err)
	}
	if !st.IsDirty {
		t.Error("expected dirty worktree to be detected")
	}
}

func TestBackupRefs_createListResolve(t *testing.T) {
	repo, _ := testRepo(t)
	head, _ := repo.ResolveRef("HEAD")

	ref := BackupRefPrefix + "1717689600"
	if err := repo.UpdateRef(ref, head, ""); err != nil {
		t.Fatal(err)
	}
	refs, err := repo.ListBackupRefs()
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].Hash != head {
		t.Fatalf("backup ref not listed correctly: %+v", refs)
	}
	if err := repo.DeleteRef(ref); err != nil {
		t.Fatal(err)
	}
	if refs, _ := repo.ListBackupRefs(); len(refs) != 0 {
		t.Error("backup ref should be deleted")
	}
}

func TestOpen_failsOutsideRepo(t *testing.T) {
	if _, err := Open(t.TempDir()); err == nil {
		t.Error("expected Open to fail outside a git repository")
	}
}
