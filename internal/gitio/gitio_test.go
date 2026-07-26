package gitio

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/wlame/regit/internal/gittest"
	"github.com/wlame/regit/internal/model"
)

// testRepo builds a throwaway git repository with three commits, all stamped at
// the same instant (the "clustered commits" situation regit exists to fix), each
// adding a distinct file so we can later prove the files survive a rewrite.
func testRepo(t *testing.T) (*Repo, string) {
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

// TestReadCommits_emptyBranchGivesAPlainMessage pins the error text. A repo with
// no commits made every headless verb print the whole `git log --first-parent
// --reverse -z --format=%H%x00%T%x00...` invocation plus git's own three-line
// "ambiguous argument 'HEAD'" hint. cmd/regit's TUI path already had a clean
// message for exactly this case; the headless path had none.
func TestReadCommits_emptyBranchGivesAPlainMessage(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = dir
	cmd.Env = gittest.Env(dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	repo, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	_, err = repo.ReadCommits()
	if err == nil {
		t.Fatal("ReadCommits succeeded on an empty repository")
	}
	if strings.Contains(err.Error(), "%x00") || strings.Contains(err.Error(), "--first-parent") {
		t.Errorf("error leaks the raw git invocation: %v", err)
	}
	if !strings.Contains(err.Error(), "no commits") {
		t.Errorf("error %q does not explain that the branch has no commits", err)
	}
}

// TestReadCommits_corruptRefGivesARealError proves a corrupt refs/heads/<branch>
// is NOT reported as "this branch has no commits yet". `rev-parse --verify
// --quiet HEAD` fails identically on an unborn branch and on a corrupt ref, so a
// helper gated on that check alone would swallow real repository corruption
// behind the same reassuring message an empty repo gets. Building this fixture
// the same way as the real-world repro (commit, then overwrite the branch ref
// with garbage) is what makes the test non-vacuous against the old code.
func TestReadCommits_corruptRefGivesARealError(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = gittest.Env(dir)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "a.txt")
	run("commit", "-q", "-m", "First.")

	refPath := filepath.Join(dir, ".git", "refs", "heads", "main")
	if err := os.WriteFile(refPath, []byte("not-a-hash\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	repo, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	_, err = repo.ReadCommits()
	if err == nil {
		t.Fatal("ReadCommits succeeded against a corrupt ref")
	}
	if strings.Contains(err.Error(), "no commits") {
		t.Errorf("corrupt ref misreported as an empty branch: %v", err)
	}
}

// TestReadCommits_orphanBranchGivesAPlainMessage proves an orphan branch
// created off a repo whose other branch already has history still gets the
// friendly "no commits yet" message, not the verbose git error. This is the
// defect the rev-list-based fix introduced: `rev-list -n 1 --all` answers a
// repo-wide question ("does ANY ref have commits?") while the message it
// gates claims a per-branch fact ("this branch has no commits yet"). On an
// orphan branch with a sibling branch that has commits, `rev-list --all`
// exits 0 with non-empty output ("has commits" by the old logic) even though
// `git log HEAD` fails with "ambiguous argument 'HEAD'" because the current
// branch itself is unborn.
func TestReadCommits_orphanBranchGivesAPlainMessage(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = gittest.Env(dir)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q", "-b", "main")
	run("config", "user.name", "wlame")
	run("config", "user.email", "vvlame@gmail.com")
	run("config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "a.txt")
	run("commit", "-q", "-m", "First.")

	// main now has a commit; gh-pages is a sibling branch with none.
	run("checkout", "-q", "--orphan", "gh-pages")

	repo, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	_, err = repo.ReadCommits()
	if err == nil {
		t.Fatal("ReadCommits succeeded on an orphan branch")
	}
	if !strings.Contains(err.Error(), "no commits") {
		t.Errorf("error %q does not explain that the branch has no commits: %v", err, err)
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

	newHead, _, err := repo.Replay(plan, DropSignatures)
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

	newHead, _, err := repo.Replay(plan, DropSignatures)
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

// Replay must preserve unedited commit messages byte-for-byte. Reading them
// via %s/%b used to fold multi-line first paragraphs into one subject line
// and swallow comment lines that lacked a blank separator after the title.
func TestReplay_preservesTrickyMessages(t *testing.T) {
	dir := t.TempDir()
	run := func(stdin string, args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = gittest.Env(dir,
			"GIT_AUTHOR_DATE=2026-06-05T14:00:00+00:00",
			"GIT_COMMITTER_DATE=2026-06-05T14:00:00+00:00")
		if stdin != "" {
			cmd.Stdin = strings.NewReader(stdin)
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("", "init", "-q")
	run("", "symbolic-ref", "HEAD", "refs/heads/main")
	run("", "config", "user.name", "wlame")
	run("", "config", "user.email", "vvlame@gmail.com")
	run("", "config", "commit.gpgsign", "false")

	messages := []string{
		"Normal commit\n\nWith a separated body.\n",
		"No blank line after title\nthis comment line is in the first paragraph\nand this one too\n",
		"Multiline first paragraph\ncontinued on a second line\n\nReal body after the break.\n",
	}
	for i, msg := range messages {
		name := filepath.Join(dir, "f"+string(rune('0'+i))+".txt")
		if err := os.WriteFile(name, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		run("", "add", ".")
		run(msg, "commit", "-q", "-F", "-", "--cleanup=verbatim")
	}

	repo, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}

	// The display subject is the first line only — never a folded paragraph.
	if got := commits[1].Subject; got != "No blank line after title" {
		t.Errorf("subject should be the first line, got %q", got)
	}
	if !strings.Contains(commits[1].Body, "and this one too") {
		t.Errorf("comment lines after the title should land in Body, got %q", commits[1].Body)
	}

	// Rewrite all commits with a date-only edit; messages are untouched.
	plan := model.NewPlan(commits)
	for _, c := range commits {
		nd := c.OrigAuthorDate.Add(-time.Hour)
		plan.Edits[c.Hash] = model.Edit{NewAuthorDate: &nd, NewCommitDate: &nd}
	}
	newHead, _, err := repo.Replay(plan, DropSignatures)
	if err != nil {
		t.Fatal(err)
	}

	out, err := repo.gitRaw("log", "--reverse", "--format=%B%x1e", newHead)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range strings.Split(out, "\x1e") {
		if m = strings.Trim(m, "\n"); m != "" {
			got = append(got, m+"\n")
		}
	}
	if len(got) != len(messages) {
		t.Fatalf("expected %d rewritten messages, got %d", len(messages), len(got))
	}
	for i, want := range messages {
		if got[i] != want {
			t.Errorf("message %d corrupted:\nwant %q\ngot  %q", i, want, got[i])
		}
	}
}

// With a base boundary, Replay must not touch the frozen prefix: those
// commits keep their exact hashes, and the first rewritten commit stays
// chained onto the original frozen ancestor.
func TestReplay_baseBoundaryFreezesPriorCommits(t *testing.T) {
	repo, _ := testRepo(t) // alpha, beta, gamma (root-first)
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}

	plan := model.NewPlan(commits)
	plan.SetBase(1) // freeze alpha
	for _, c := range plan.Active() {
		nd := c.OrigAuthorDate.AddDate(0, 1, 0)
		plan.Edits[c.Hash] = model.Edit{NewAuthorDate: &nd, NewCommitDate: &nd}
	}

	newHead, _, err := repo.Replay(plan, DropSignatures)
	if err != nil {
		t.Fatal(err)
	}

	out, err := repo.gitRaw("rev-list", "--reverse", newHead)
	if err != nil {
		t.Fatal(err)
	}
	hashes := strings.Fields(out)
	if len(hashes) != 3 {
		t.Fatalf("rewritten history should still have 3 commits, got %d", len(hashes))
	}
	if hashes[0] != commits[0].Hash {
		t.Errorf("frozen root must keep its hash: want %s, got %s", commits[0].Hash, hashes[0])
	}
	if hashes[1] == commits[1].Hash || hashes[2] == commits[2].Hash {
		t.Error("active commits should have been rewritten to new hashes")
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

func TestReplay_returnsOldToNewRemap(t *testing.T) {
	repo, _ := testRepo(t)
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	plan := model.NewPlan(commits)
	d := mustParse(t, "2026-03-01T10:00:00+00:00")
	for _, c := range commits {
		plan.SetEdit(c.Hash, model.Edit{NewAuthorDate: &d, NewCommitDate: &d})
	}
	newHead, remap, err := repo.Replay(plan, DropSignatures)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(remap) != len(commits) {
		t.Fatalf("remap has %d entries, want %d", len(remap), len(commits))
	}
	// The newest commit's remapped hash must be the returned newHead.
	if remap[commits[len(commits)-1].Hash] != newHead {
		t.Errorf("remap[newest] = %s, want newHead %s", remap[commits[len(commits)-1].Hash], newHead)
	}
	// Every new hash must differ from its original (dates changed).
	for old, newHash := range remap {
		if old == newHash {
			t.Errorf("commit %s was not rewritten", old)
		}
	}
}

func TestOpen_failsOutsideRepo(t *testing.T) {
	if _, err := Open(t.TempDir()); err == nil {
		t.Error("expected Open to fail outside a git repository")
	}
}

// newRefsTestRepo builds a one-commit repo on main for backup-ref tests.
func newRefsTestRepo(t *testing.T) *Repo {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = gittest.Env(dir,
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("symbolic-ref", "HEAD", "refs/heads/main")
	run("config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "f.txt")
	run("commit", "-q", "-m", "one")
	repo, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

// TestCreateBackupRef_collisionBumpsSeq: two backups in the same second must
// both survive — the second gets a bumped -<seq> suffix (H2).
func TestCreateBackupRef_collisionBumpsSeq(t *testing.T) {
	repo := newRefsTestRepo(t) // helper per note above; returns *Repo with 1 commit on main
	head, err := repo.ResolveRef("HEAD")
	if err != nil {
		t.Fatal(err)
	}
	ref1, err := repo.CreateBackupRef("main", 1717689600, head)
	if err != nil {
		t.Fatalf("first CreateBackupRef: %v", err)
	}
	ref2, err := repo.CreateBackupRef("main", 1717689600, head)
	if err != nil {
		t.Fatalf("second CreateBackupRef: %v", err)
	}
	if ref1 == ref2 {
		t.Fatalf("collision not avoided: both = %s", ref1)
	}
	if ref1 != "refs/regit/backup/main/1717689600-0" || ref2 != "refs/regit/backup/main/1717689600-1" {
		t.Errorf("unexpected names: %s, %s", ref1, ref2)
	}
	for _, ref := range []string{ref1, ref2} {
		if got, err := repo.ResolveRef(ref); err != nil || got != head {
			t.Errorf("%s resolves to %q (err %v), want %s", ref, got, err, head)
		}
	}
}

// TestListBackupRefs_parsesBranchAndTimestamp: new-style refs carry Branch and
// Timestamp (branch names may contain '/'); legacy flat refs parse as Branch "".
func TestListBackupRefs_parsesBranchAndTimestamp(t *testing.T) {
	repo := newRefsTestRepo(t)
	head, _ := repo.ResolveRef("HEAD")
	if _, err := repo.CreateBackupRef("fix/audit-quick-wins", 1717689601, head); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateRef("refs/regit/backup/1717689600", head, ""); err != nil { // legacy shape
		t.Fatal(err)
	}
	refs, err := repo.ListBackupRefs()
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 {
		t.Fatalf("want 2 refs, got %d: %+v", len(refs), refs)
	}
	// Sorted by Timestamp desc: the new-style ref (…601) first.
	if refs[0].Branch != "fix/audit-quick-wins" || refs[0].Timestamp != 1717689601 {
		t.Errorf("refs[0] = %+v, want branch fix/audit-quick-wins ts 1717689601", refs[0])
	}
	if refs[1].Branch != "" || refs[1].Timestamp != 1717689600 {
		t.Errorf("refs[1] = %+v, want legacy (branch \"\") ts 1717689600", refs[1])
	}
}

// TestListBackupRefs_sameTimestampOrdersByNameDescending: two backups on the
// same branch at the SAME unix second collide on seq and produce "-0"/"-1".
// ListBackupRefs must return both, and — since their Timestamps are equal —
// the sort's tiebreak (Name descending) must decide the order: "...-1" sorts
// after "...-0" lexically, so it is returned FIRST.
func TestListBackupRefs_sameTimestampOrdersByNameDescending(t *testing.T) {
	repo := newRefsTestRepo(t)
	head, err := repo.ResolveRef("HEAD")
	if err != nil {
		t.Fatal(err)
	}
	ref0, err := repo.CreateBackupRef("main", 1717689600, head)
	if err != nil {
		t.Fatalf("first CreateBackupRef: %v", err)
	}
	ref1, err := repo.CreateBackupRef("main", 1717689600, head)
	if err != nil {
		t.Fatalf("second CreateBackupRef: %v", err)
	}
	if ref0 == ref1 {
		t.Fatalf("collision not avoided: both = %s", ref0)
	}

	refs, err := repo.ListBackupRefs()
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 {
		t.Fatalf("want 2 refs, got %d: %+v", len(refs), refs)
	}
	if refs[0].Timestamp != refs[1].Timestamp {
		t.Fatalf("both refs should share a timestamp, got %d and %d", refs[0].Timestamp, refs[1].Timestamp)
	}
	// Name-descending tiebreak: "...-1" > "...-0", so ref1 comes first.
	if refs[0].Name != ref1 || refs[1].Name != ref0 {
		t.Errorf("expected same-timestamp order [%s, %s] (Name descending), got [%s, %s]",
			ref1, ref0, refs[0].Name, refs[1].Name)
	}
}

// TestCreateBackupRef_invalidBranchNameSurfacesError: a branch name containing
// ".." is an illegal git ref component. The guarded create fails, the
// re-resolve also fails (the ref was never created), so refs.go's
// disambiguation must classify this as a real error rather than retry it as a
// same-second collision.
func TestCreateBackupRef_invalidBranchNameSurfacesError(t *testing.T) {
	repo := newRefsTestRepo(t)
	head, err := repo.ResolveRef("HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateBackupRef("bad..name", 1717689600, head); err == nil {
		t.Fatal("expected an error for an invalid ref name, got nil")
	}
}

func TestGitDir_returnsAbsolutePath(t *testing.T) {
	repo, dir := testRepo(t)
	gitDir, err := repo.GitDir()
	if err != nil {
		t.Fatalf("GitDir: %v", err)
	}
	// The .git dir must be absolute and end with ".git" for a non-bare repo.
	if !filepath.IsAbs(gitDir) {
		t.Errorf("GitDir = %q, want an absolute path", gitDir)
	}
	// Compare by directory identity, not string equality: on Windows git
	// reports forward slashes and long names while the temp dir may carry
	// backslashes and 8.3 short names (RUNNER~1) for the same location.
	want := filepath.Join(dir, ".git")
	gotInfo, gotErr := os.Stat(gitDir)
	wantInfo, wantErr := os.Stat(want)
	if gotErr != nil || wantErr != nil || !os.SameFile(gotInfo, wantInfo) {
		t.Errorf("GitDir = %q, want the same directory as %q (stat errs: %v, %v)", gitDir, want, gotErr, wantErr)
	}
}

// TestRunGit_timesOutOnHungGit verifies the per-invocation ceiling: a git that
// never returns (simulated by a fake `git` on PATH that sleeps) is killed with
// a clear timeout error instead of blocking regit forever.
func TestRunGit_timesOutOnHungGit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("relies on a sh-script fake git")
	}
	fakeDir := t.TempDir()
	script := "#!/bin/sh\nexec sleep 60\n" // exec: the kill lands on the sleeper itself, not a wrapper
	if err := os.WriteFile(filepath.Join(fakeDir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	old := gitTimeout
	gitTimeout = 200 * time.Millisecond
	defer func() { gitTimeout = old }()

	start := time.Now()
	_, err := runGit(t.TempDir(), nil, nil, "status")
	if err == nil {
		t.Fatal("hung git should return a timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("want a timeout error, got: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("timeout took %s; the process was not killed promptly", elapsed)
	}
}
