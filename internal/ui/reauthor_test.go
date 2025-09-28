package ui

import (
	"strings"
	"testing"

	"github.com/wlame/regit/internal/model"
)

// reauthorCommits builds root-first commits with explicit author identity.
func reauthorCommits(hashes ...string) []model.Commit {
	out := make([]model.Commit, len(hashes))
	for i, h := range hashes {
		out[i] = model.Commit{
			Hash: h, Subject: "c " + h,
			AuthorName: "old", AuthorEmail: "old@x",
			CommitterName: "old", CommitterEmail: "old@x",
		}
		if i > 0 {
			out[i].ParentHashes = []string{hashes[i-1]}
		}
	}
	return out
}

func TestReauthorChange_planEditsTargetsOnly(t *testing.T) {
	commits := reauthorCommits("a", "b", "c")
	targets := map[string]bool{"b": true}
	rc := newReauthorChange(commits, targets, "wlame", "new@x", true)

	if _, ok := rc.plan.EditFor("a"); ok {
		t.Error("unselected commit 'a' must not be edited")
	}
	e, ok := rc.plan.EditFor("b")
	if !ok {
		t.Fatal("selected commit 'b' should have an edit")
	}
	if e.EffectiveAuthorName(commits[1]) != "wlame" || e.EffectiveAuthorEmail(commits[1]) != "new@x" {
		t.Error("author identity not applied to 'b'")
	}
	if e.NewCommitterName == nil {
		t.Error("committer should also change when committer=true")
	}
}

func TestReauthorChange_committerFalseLeavesCommitter(t *testing.T) {
	commits := reauthorCommits("a", "b")
	rc := newReauthorChange(commits, map[string]bool{"b": true}, "wlame", "new@x", false)
	e, _ := rc.plan.EditFor("b")
	if e.NewCommitterName != nil || e.NewCommitterEmail != nil {
		t.Error("committer must be untouched when committer=false")
	}
}

func TestReauthorChange_previewShowsOldToNew(t *testing.T) {
	commits := reauthorCommits("a", "b")
	rc := newReauthorChange(commits, map[string]bool{"b": true}, "wlame", "new@x", true)
	pm := rc.preview(newStyles())
	joined := strings.Join(pm.lines, "\n")
	if !strings.Contains(joined, "old@x") || !strings.Contains(joined, "new@x") {
		t.Errorf("preview should show old -> new author:\n%s", joined)
	}
	if rc.title() == "" {
		t.Error("title should be non-empty")
	}
}
