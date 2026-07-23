package safety

import (
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/gittest"
	"github.com/wlame/regit/internal/model"
)

// repoShape selects which history feature a generated fixture must exhibit.
// The property harness needs different shapes for different operations: a
// linear history cannot exercise linearize, and single-hunk commits cannot
// exercise split.
type repoShape int

const (
	shapeLinear    repoShape = iota // plain first-parent chain
	shapeMerges                     // contains at least one real merge commit
	shapeMultiHunk                  // contains at least one commit with 2+ diff hunks
)

// genRepo builds a repository with a pseudo-random (but seed-deterministic)
// linear history: each commit performs 1–3 random file operations (create,
// overwrite, delete) over a small file pool and carries a message drawn from a
// set that includes multi-line bodies, wrapped paragraphs, and unicode. shape
// then layers on the topology or diff-shape features that shapeLinear alone
// cannot produce.
func genRepo(t *testing.T, rng *rand.Rand, shape repoShape) *gitio.Repo {
	t.Helper()
	dir := t.TempDir()
	run := func(env []string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = gittest.Env(dir, env...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run(nil, "init", "-q")
	run(nil, "symbolic-ref", "HEAD", "refs/heads/main")
	run(nil, "config", "commit.gpgsign", "false")

	messages := []string{
		"Simple subject.",
		"Subject line.\n\nA body paragraph\nspanning two lines.",
		"Wrapped first paragraph\nthat continues without a blank line.",
		"Unicode: caffè ☕ commit.",
		"Title.\n\n\nBody after two blank lines.",
	}
	files := []string{"a.txt", "b.txt", "c.txt", "d.txt"}
	existing := map[string]bool{}

	nCommits := 3 + rng.Intn(5)
	for i := 0; i < nCommits; i++ {
		ops := 1 + rng.Intn(3)
		changed := false
		for o := 0; o < ops; o++ {
			f := files[rng.Intn(len(files))]
			if existing[f] && rng.Intn(4) == 0 {
				if err := os.Remove(filepath.Join(dir, f)); err != nil {
					t.Fatal(err)
				}
				delete(existing, f)
				changed = true
				continue
			}
			content := fmt.Sprintf("content %d-%d seedline %d\n", i, o, rng.Int63())
			if err := os.WriteFile(filepath.Join(dir, f), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			existing[f] = true
			changed = true
		}
		if !changed {
			continue
		}
		date := fmt.Sprintf("GIT_AUTHOR_DATE=2026-06-05T%02d:00:00+00:00", 8+i)
		cdate := strings.Replace(date, "AUTHOR", "COMMITTER", 1)
		run(nil, "add", "-A")
		run([]string{date, cdate}, "commit", "-q", "--allow-empty", "-m", messages[rng.Intn(len(messages))])
	}

	switch shape {
	case shapeMerges:
		// A side branch off HEAD~1 merged back with --no-ff, so the merge is a
		// real two-parent commit rather than a fast-forward. nCommits is always
		// 3 + rng.Intn(5), so HEAD~1 always exists.
		run(nil, "checkout", "-q", "-b", "side", "HEAD~1")
		if err := os.WriteFile(filepath.Join(dir, "side.txt"),
			[]byte(fmt.Sprintf("side %d\n", rng.Int63())), 0o644); err != nil {
			t.Fatal(err)
		}
		run(nil, "add", "-A")
		run([]string{"GIT_AUTHOR_DATE=2026-06-06T09:00:00+00:00",
			"GIT_COMMITTER_DATE=2026-06-06T09:00:00+00:00"}, "commit", "-q", "-m", "Side work.")
		run(nil, "checkout", "-q", "main")
		run([]string{"GIT_AUTHOR_DATE=2026-06-06T10:00:00+00:00",
			"GIT_COMMITTER_DATE=2026-06-06T10:00:00+00:00"},
			"merge", "-q", "--no-ff", "side", "-m", "Merge side.")

	case shapeMultiHunk:
		// One commit touching several widely separated regions of one file, so
		// its diff against the parent yields multiple independent hunks: with
		// -U3 context, edits more than 6 lines apart cannot merge into one hunk.
		var wide strings.Builder
		for i := 0; i < 60; i++ {
			wide.WriteString(fmt.Sprintf("line %d\n", i))
		}
		if err := os.WriteFile(filepath.Join(dir, "wide.txt"), []byte(wide.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		run(nil, "add", "-A")
		run(nil, "commit", "-q", "-m", "Add a wide file.")

		body := strings.Split(wide.String(), "\n")
		for _, i := range []int{3, 25, 50} { // far apart so hunks cannot coalesce
			body[i] = fmt.Sprintf("edited %d seed %d", i, rng.Int63())
		}
		if err := os.WriteFile(filepath.Join(dir, "wide.txt"),
			[]byte(strings.Join(body, "\n")), 0o644); err != nil {
			t.Fatal(err)
		}
		run(nil, "add", "-A")
		run(nil, "commit", "-q", "-m", "Edit three regions.")
	}

	repo, err := gitio.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

// genPlan attaches pseudo-random edits: monotonically increasing dates (so the
// plan stays valid), occasional subject rewrites and body drops.
func genPlan(t *testing.T, rng *rand.Rand, commits []model.Commit) *model.Plan {
	t.Helper()
	plan := model.NewPlan(commits)
	cursor := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	for _, c := range commits {
		var e model.Edit
		cursor = cursor.Add(time.Duration(1+rng.Intn(48)) * time.Hour)
		if rng.Float64() < 0.8 {
			d := cursor
			e.NewAuthorDate, e.NewCommitDate = &d, &d
		}
		if rng.Float64() < 0.3 {
			s := fmt.Sprintf("Rewritten subject %d.", rng.Intn(1000))
			e.NewSubject = &s
		}
		if rng.Float64() < 0.2 {
			e.DropBody = true
		}
		if e.NewAuthorDate != nil || e.NewSubject != nil || e.DropBody {
			plan.SetEdit(c.Hash, e)
		}
	}
	return plan
}

// TestProperty_replayPreservesEveryTreeOID is the generalized form of the
// fixture-based apply tests: over seed-deterministic random repos and random
// edit plans, a successful apply must preserve every commit's tree OID and the
// commit count — file content can NEVER change, whatever the history shape or
// the mix of date/subject/body edits.
func TestProperty_replayPreservesEveryTreeOID(t *testing.T) {
	for seed := int64(1); seed <= 6; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			repo := genRepo(t, rng, shapeLinear)
			before, err := repo.ReadCommits()
			if err != nil {
				t.Fatal(err)
			}
			plan := genPlan(t, rng, before)
			if !plan.HasChanges() {
				t.Skip("this seed generated no edits")
			}

			state, _ := repo.ReadState()
			if _, err := NewManager(repo).Apply(plan, state, ApplyOptions{
				UnixTimestamp: seed, ExpectedHead: state.HeadHash,
			}); err != nil {
				t.Fatalf("Apply: %v", err)
			}

			after, err := repo.ReadCommits()
			if err != nil {
				t.Fatal(err)
			}
			if len(after) != len(before) {
				t.Fatalf("commit count changed: %d -> %d", len(before), len(after))
			}
			for i := range before {
				if after[i].TreeHash != before[i].TreeHash {
					t.Errorf("commit %d tree OID changed: %s -> %s (subject %q)",
						i, shortHash(before[i].TreeHash), shortHash(after[i].TreeHash), before[i].Subject)
				}
			}
		})
	}
}

// TestGenRepo_shapesProduceTheirDefiningFeature guards the generators
// themselves. A property test that silently generated only linear history would
// pass while proving nothing about linearize, and a single-hunk generator would
// prove nothing about split — the test would be green and useless.
func TestGenRepo_shapesProduceTheirDefiningFeature(t *testing.T) {
	rng := rand.New(rand.NewSource(1))

	repo := genRepo(t, rng, shapeMerges)
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	var merges int
	for _, c := range commits {
		if c.IsMerge {
			merges++
		}
	}
	if merges == 0 {
		t.Error("shapeMerges generated no merge commits")
	}

	repo = genRepo(t, rng, shapeMultiHunk)
	commits, err = repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	// A splittable commit needs at least two hunks in its diff against its
	// parent. ReadCommits returns the first-parent chain root-first, so
	// commits[i-1].TreeHash is always commits[i]'s parent tree.
	var best int
	for i := 1; i < len(commits); i++ {
		hunks, err := repo.Hunks(commits[i-1].TreeHash, commits[i].TreeHash)
		if err != nil {
			continue
		}
		if len(hunks) > best {
			best = len(hunks)
		}
	}
	if best < 2 {
		t.Errorf("shapeMultiHunk best commit had %d hunk(s), want >= 2", best)
	}
}
