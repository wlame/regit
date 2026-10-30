package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wlame/regit/internal/config"
	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/model"
	"github.com/wlame/regit/internal/safety"
)

func TestChooseBaseBranch_precedence(t *testing.T) {
	cases := []struct {
		name     string
		state    model.RepoState
		branches []string
		want     string
	}{
		{"upstream wins", model.RepoState{Branch: "feature", HasUpstream: true, UpstreamRef: "origin/main"}, []string{"main", "feature"}, "origin/main"},
		{"falls back to local main", model.RepoState{Branch: "feature"}, []string{"feature", "main"}, "main"},
		{"falls back to master", model.RepoState{Branch: "feature"}, []string{"feature", "master"}, "master"},
		{"current branch is main -> none", model.RepoState{Branch: "main"}, []string{"main"}, ""},
		{"nothing to anchor on -> none", model.RepoState{Branch: "feature"}, []string{"feature"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := chooseBaseBranch(tc.state, tc.branches); got != tc.want {
				t.Errorf("chooseBaseBranch = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBaseIndexForMergeBase(t *testing.T) {
	commits := []model.Commit{{Hash: "aaa"}, {Hash: "bbb"}, {Hash: "ccc"}, {Hash: "ddd"}}
	// Merge-base at storage index 1 (bbb) -> freeze indices 0,1 -> Base = 2.
	if got := baseIndexForMergeBase(commits, "bbb"); got != 2 {
		t.Errorf("base index for bbb = %d, want 2", got)
	}
	// Merge-base at the newest commit -> Base = len (caller's SetBase clamps).
	if got := baseIndexForMergeBase(commits, "ddd"); got != 4 {
		t.Errorf("base index for ddd = %d, want 4", got)
	}
	// Not on this linear history -> freeze nothing.
	if got := baseIndexForMergeBase(commits, "zzz"); got != 0 {
		t.Errorf("base index for unknown hash = %d, want 0", got)
	}
}

func TestStartsUnlocked(t *testing.T) {
	cases := []struct {
		name  string
		state model.RepoState
		want  bool
	}{
		{"trunk in sync -> unlocked", model.RepoState{Branch: "main", HasUpstream: true, UpstreamRef: "origin/main", AheadOfRemote: 0}, true},
		{"master in sync -> unlocked", model.RepoState{Branch: "master", HasUpstream: true, UpstreamRef: "origin/master", AheadOfRemote: 0}, true},
		{"trunk ahead -> locked", model.RepoState{Branch: "main", HasUpstream: true, UpstreamRef: "origin/main", AheadOfRemote: 3}, false},
		{"trunk no upstream -> locked", model.RepoState{Branch: "main"}, false},
		{"feature branch -> locked", model.RepoState{Branch: "feature", HasUpstream: true, UpstreamRef: "origin/feature", AheadOfRemote: 0}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := startsUnlocked(tc.state); got != tc.want {
				t.Errorf("startsUnlocked = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPushedBaseIndex(t *testing.T) {
	pushed, local := model.Commit{IsPushed: true}, model.Commit{}
	cases := []struct {
		name    string
		commits []model.Commit
		want    int
	}{
		{"nothing pushed", []model.Commit{local, local}, 0},
		{"pushed prefix", []model.Commit{pushed, pushed, local}, 2},
		{"everything pushed", []model.Commit{pushed, pushed}, 2},
		{"empty history", nil, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pushedBaseIndex(tc.commits); got != tc.want {
				t.Errorf("pushedBaseIndex = %d, want %d", got, tc.want)
			}
		})
	}
}

// cloneWithOrigin builds a bare "origin" holding five commits on trunk, clones
// it to "work", runs setup there, and returns a Model opened on the clone.
func cloneWithOrigin(t *testing.T, trunk string, setup func(git func(args ...string), commit func(name string))) Model {
	t.Helper()
	fixtureMu.Lock()
	defer fixtureMu.Unlock()
	root := t.TempDir()
	gitIn := func(dir string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = hermeticGitEnv(root)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	commitIn := func(dir, name string) {
		if err := os.WriteFile(filepath.Join(dir, name+".txt"), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
		gitIn(dir, "add", ".")
		gitIn(dir, "-c", "user.name=w", "-c", "user.email=w@example.com", "commit", "-q", "-m", name)
	}
	seed := filepath.Join(root, "seed")
	if err := os.Mkdir(seed, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(seed, "init", "-q", "-b", trunk)
	for i := range 5 {
		commitIn(seed, fmt.Sprintf("s%d", i))
	}
	gitIn(root, "clone", "-q", "--bare", "seed", "origin")
	gitIn(root, "clone", "-q", "origin", "work")
	work := filepath.Join(root, "work")
	setup(func(args ...string) { gitIn(work, args...) }, func(name string) { commitIn(work, name) })

	repo, err := gitio.Open(work)
	if err != nil {
		t.Fatal(err)
	}
	state, err := repo.ReadState()
	if err != nil {
		t.Fatal(err)
	}
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	return New(repo, safety.NewManager(repo), config.Default(), state, commits)
}

func TestApplyAutoBase_freezesEveryPushedCommit(t *testing.T) {
	cases := []struct {
		name, trunk string
		setup       func(git func(args ...string), commit func(name string))
		wantFrozen  int
		wantLabel   string
	}{
		{"trunk without main or master", "develop", func(git func(...string), commit func(string)) {
			git("switch", "-q", "-c", "feat", "--no-track")
			commit("f1")
			commit("f2")
		}, 5, "pushed"},
		{"local main behind origin/main", "main", func(git func(...string), commit func(string)) {
			git("reset", "-q", "--hard", "HEAD~3")
			git("switch", "-q", "-c", "feat", "origin/main", "--no-track")
			commit("f1")
			commit("f2")
		}, 5, "pushed"},
		{"tracks origin/main but also pushed elsewhere", "main", func(git func(...string), commit func(string)) {
			git("switch", "-q", "-c", "feat", "origin/main")
			commit("f1")
			commit("f2")
			git("push", "-q", "origin", "feat")
			commit("f3")
		}, 7, "pushed"},
		{"main ahead of origin/main keeps its upstream label", "main", func(git func(...string), commit func(string)) {
			commit("m1")
		}, 5, "origin/main"},
		{"main in sync with origin stays editable", "main", func(func(...string), func(string)) {}, 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := cloneWithOrigin(t, tc.trunk, tc.setup)
			if m.plan.Base != tc.wantFrozen || m.baseBranch != tc.wantLabel {
				t.Errorf("frozen=%d label=%q, want frozen=%d label=%q",
					m.plan.Base, m.baseBranch, tc.wantFrozen, tc.wantLabel)
			}
		})
	}
}
