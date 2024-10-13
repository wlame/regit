package model

import (
	"testing"
	"time"
)

func tm(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

func TestEdit_IsEmpty(t *testing.T) {
	if !(Edit{}).IsEmpty() {
		t.Error("zero edit should be empty")
	}
	d := tm("2026-03-01T10:00:00Z")
	if (Edit{NewAuthorDate: &d}).IsEmpty() {
		t.Error("edit with a date should not be empty")
	}
	if (Edit{DropBody: true}).IsEmpty() {
		t.Error("edit dropping body should not be empty")
	}
}

func TestEdit_EffectiveDatesFallBackToOriginal(t *testing.T) {
	c := Commit{
		OrigAuthorDate: tm("2026-01-01T08:00:00Z"),
		OrigCommitDate: tm("2026-01-01T09:00:00Z"),
	}
	// No overrides -> originals.
	if !(Edit{}).EffectiveAuthorDate(c).Equal(c.OrigAuthorDate) {
		t.Error("empty edit should yield original author date")
	}
	if !(Edit{}).EffectiveCommitDate(c).Equal(c.OrigCommitDate) {
		t.Error("empty edit should yield original commit date")
	}
	// Override wins.
	d := tm("2026-05-05T05:05:00Z")
	if !(Edit{NewAuthorDate: &d}).EffectiveAuthorDate(c).Equal(d) {
		t.Error("override author date should win")
	}
}

func TestPlan_SetEditRemovesEmptyEdits(t *testing.T) {
	c := Commit{Hash: "a"}
	p := NewPlan([]Commit{c})

	d := tm("2026-03-01T10:00:00Z")
	p.SetEdit("a", Edit{NewAuthorDate: &d})
	if !p.HasChanges() {
		t.Fatal("expected an edit after SetEdit")
	}
	// Setting an empty edit should delete it.
	p.SetEdit("a", Edit{})
	if p.HasChanges() {
		t.Error("empty edit should be removed, leaving no changes")
	}
}

func TestPlan_EffectiveAuthorDateUsesEdit(t *testing.T) {
	c := Commit{Hash: "a", OrigAuthorDate: tm("2026-01-01T00:00:00Z")}
	p := NewPlan([]Commit{c})
	d := tm("2026-09-09T09:09:00Z")
	p.SetEdit("a", Edit{NewAuthorDate: &d})
	if !p.EffectiveAuthorDate(c).Equal(d) {
		t.Error("plan should reflect the edited author date")
	}
}

func TestPlan_Validity(t *testing.T) {
	p := NewPlan(nil)
	if !p.IsValid() {
		t.Error("plan with no violations should be valid")
	}
	p.Violations = []SequenceViolation{{ChildHash: "b", ParentHash: "a"}}
	if p.IsValid() {
		t.Error("plan with violations should be invalid")
	}
}

func TestCommit_HasBody(t *testing.T) {
	if (Commit{}).HasBody() {
		t.Error("empty body should report false")
	}
	if !(Commit{Body: "x"}).HasBody() {
		t.Error("non-empty body should report true")
	}
}

func TestRepoState_Guards(t *testing.T) {
	if (RepoState{IsDirty: true}).CanRewriteSafely() {
		t.Error("dirty state should not be safe to rewrite")
	}
	if !(RepoState{}).CanRewriteSafely() {
		t.Error("clean state should be safe")
	}
	pushed := RepoState{HasUpstream: true, UpstreamRef: "origin/main"}
	if !pushed.HasPushedCommits() {
		t.Error("state with upstream should report pushed commits")
	}
}
