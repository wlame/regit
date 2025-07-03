package ui

import (
	"testing"

	"github.com/wlame/regit/internal/model"
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
