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

func TestPlan_FirstUnpushedIndex(t *testing.T) {
	mk := func(pushed ...bool) *Plan {
		cs := make([]Commit, len(pushed))
		for i, p := range pushed {
			cs[i] = Commit{Hash: string(rune('a' + i)), IsPushed: p}
		}
		return NewPlan(cs)
	}

	cases := []struct {
		name   string
		plan   *Plan
		want   int
		wantOK bool
	}{
		// Storage is root-first; pushed commits form the older prefix.
		{"some unpushed", mk(true, true, false, false), 2, true},
		{"none pushed", mk(false, false), 0, true},
		{"all pushed", mk(true, true), 0, false},
		{"empty", NewPlan(nil), 0, false},
		{"only newest unpushed", mk(true, true, false), 2, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := tc.plan.FirstUnpushedIndex()
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("FirstUnpushedIndex() = (%d, %v), want (%d, %v)", got, ok, tc.want, tc.wantOK)
			}
		})
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
	// Pushed-commit detection is retired from RepoState entirely (HasPushedCommits
	// is gone): it is now the per-commit model.AnyPushed predicate, exercised in
	// pushed_test.go, since only the commits a rewrite actually touches matter.
}

// TestPlan_SetBase verifies that SetBase clamps the boundary within [0, last],
// updates the Base field, and drops any pending edits on frozen commits.
func TestPlan_SetBase(t *testing.T) {
	commits := []Commit{{Hash: "a"}, {Hash: "b"}, {Hash: "c"}}
	p := NewPlan(commits)
	p.Edits["a"] = Edit{}
	p.Edits["b"] = Edit{}

	p.SetBase(1) // freeze commit a
	if p.Base != 1 {
		t.Errorf("Base = %d, want 1", p.Base)
	}
	if _, ok := p.Edits["a"]; ok {
		t.Error("frozen commit a's edit should be removed")
	}
	if _, ok := p.Edits["b"]; !ok {
		t.Error("editable commit b's edit should remain")
	}

	// Clamping: negative index → 0, index > last → last.
	p.SetBase(-5)
	if p.Base != 0 {
		t.Errorf("negative SetBase should clamp to 0, got %d", p.Base)
	}
	p.SetBase(100)
	if p.Base != 2 {
		t.Errorf("out-of-range SetBase should clamp to last (%d), got %d", len(commits)-1, p.Base)
	}
}

// TestPlan_SetBase_EmptyPlan verifies that SetBase on a plan with no commits
// (an empty branch) keeps Base at 0 instead of panicking — the old clamp set
// Base to -1 and Commits[:Base] blew up.
func TestPlan_SetBase_EmptyPlan(t *testing.T) {
	p := NewPlan(nil)
	p.SetBase(0)
	if p.Base != 0 {
		t.Errorf("SetBase on empty plan: Base = %d, want 0", p.Base)
	}
	p.SetBase(5)
	if p.Base != 0 {
		t.Errorf("SetBase(5) on empty plan: Base = %d, want 0", p.Base)
	}
	if got := p.Active(); len(got) != 0 {
		t.Errorf("Active() on empty plan = %d commits, want 0", len(got))
	}
}

// TestPlan_ActiveAndIsFrozen verifies that Active returns only commits from
// Base onward and that IsFrozen correctly identifies frozen positions.
func TestPlan_ActiveAndIsFrozen(t *testing.T) {
	commits := []Commit{{Hash: "a"}, {Hash: "b"}, {Hash: "c"}}
	p := NewPlan(commits)
	p.Base = 1

	active := p.Active()
	if len(active) != 2 || active[0].Hash != "b" || active[1].Hash != "c" {
		t.Errorf("Active() = %v, want [{b} {c}]", active)
	}
	if !p.IsFrozen(0) {
		t.Error("index 0 (before Base=1) should be frozen")
	}
	if p.IsFrozen(1) {
		t.Error("index 1 (== Base) should not be frozen")
	}
}

// TestPlan_EffectiveSubject verifies that EffectiveSubject returns the edited
// subject when an edit is present, and falls back to the original otherwise.
func TestPlan_EffectiveSubject(t *testing.T) {
	c := Commit{Hash: "x", Subject: "original"}
	p := NewPlan([]Commit{c})

	if got := p.EffectiveSubject(c); got != "original" {
		t.Errorf("no edit: EffectiveSubject = %q, want %q", got, "original")
	}

	newSubj := "edited"
	p.SetEdit("x", Edit{NewSubject: &newSubj})
	if got := p.EffectiveSubject(c); got != "edited" {
		t.Errorf("with edit: EffectiveSubject = %q, want %q", got, "edited")
	}
}

// TestPlan_EditFor verifies that EditFor returns the stored edit and true when
// present, and the zero Edit with false when absent.
func TestPlan_EditFor(t *testing.T) {
	p := NewPlan([]Commit{{Hash: "a"}})
	if _, ok := p.EditFor("a"); ok {
		t.Error("EditFor should return false when no edit exists")
	}
	newSubj := "s"
	p.SetEdit("a", Edit{NewSubject: &newSubj})
	e, ok := p.EditFor("a")
	if !ok {
		t.Error("EditFor should return true after SetEdit")
	}
	if e.NewSubject == nil || *e.NewSubject != "s" {
		t.Errorf("EditFor returned wrong edit: %+v", e)
	}
}

// TestCommit_SplitMessage verifies that SplitMessage splits a raw git commit
// message at the first blank line: the first line becomes the subject and the
// rest (without the leading newlines) becomes the body.
func TestCommit_SplitMessage(t *testing.T) {
	cases := []struct {
		raw     string
		subject string
		body    string
	}{
		{"subject only", "subject only", ""},
		{"subject\n\nbody text", "subject", "body text"},
		{"subject\n\nline1\nline2", "subject", "line1\nline2"},
		{"trailing newline\n", "trailing newline", ""},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			subj, body := SplitMessage(tc.raw)
			if subj != tc.subject {
				t.Errorf("subject = %q, want %q", subj, tc.subject)
			}
			if body != tc.body {
				t.Errorf("body = %q, want %q", body, tc.body)
			}
		})
	}
}

// TestEdit_EffectiveCommitDate verifies that EffectiveCommitDate returns the
// override when set and falls back to the original committer date otherwise.
func TestEdit_EffectiveCommitDate(t *testing.T) {
	orig := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	override := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	c := Commit{OrigCommitDate: orig}

	e := Edit{}
	if got := e.EffectiveCommitDate(c); got != orig {
		t.Errorf("no override: EffectiveCommitDate = %v, want %v", got, orig)
	}

	e.NewCommitDate = &override
	if got := e.EffectiveCommitDate(c); got != override {
		t.Errorf("with override: EffectiveCommitDate = %v, want %v", got, override)
	}
}
