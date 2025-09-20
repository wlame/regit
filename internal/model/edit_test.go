package model

import "testing"

func sp(s string) *string { return &s }

func TestEffectiveIdentity_overrideOrOriginal(t *testing.T) {
	c := Commit{
		AuthorName: "old", AuthorEmail: "old@x",
		CommitterName: "oldc", CommitterEmail: "oldc@x",
	}
	// No overrides: originals flow through.
	var e Edit
	if got := e.EffectiveAuthorName(c); got != "old" {
		t.Errorf("author name: got %q, want original", got)
	}
	if got := e.EffectiveCommitterEmail(c); got != "oldc@x" {
		t.Errorf("committer email: got %q, want original", got)
	}
	// Overrides win.
	e = Edit{
		NewAuthorName: sp("new"), NewAuthorEmail: sp("new@x"),
		NewCommitterName: sp("newc"), NewCommitterEmail: sp("newc@x"),
	}
	if got := e.EffectiveAuthorName(c); got != "new" {
		t.Errorf("author name override: got %q", got)
	}
	if got := e.EffectiveAuthorEmail(c); got != "new@x" {
		t.Errorf("author email override: got %q", got)
	}
	if got := e.EffectiveCommitterName(c); got != "newc" {
		t.Errorf("committer name override: got %q", got)
	}
	if got := e.EffectiveCommitterEmail(c); got != "newc@x" {
		t.Errorf("committer email override: got %q", got)
	}
}

func TestIsEmpty_identityOverridesCount(t *testing.T) {
	if !(Edit{}).IsEmpty() {
		t.Error("zero Edit must be empty")
	}
	for name, e := range map[string]Edit{
		"author name":     {NewAuthorName: sp("x")},
		"author email":    {NewAuthorEmail: sp("x")},
		"committer name":  {NewCommitterName: sp("x")},
		"committer email": {NewCommitterEmail: sp("x")},
	} {
		if e.IsEmpty() {
			t.Errorf("%s: Edit with an identity override must not be empty", name)
		}
	}
}

func TestNewIdentityEdit_setsOnlyChangedFields(t *testing.T) {
	c := Commit{
		AuthorName: "old", AuthorEmail: "old@x",
		CommitterName: "old", CommitterEmail: "old@x",
	}
	// Both fields change, committer mirrored.
	e := NewIdentityEdit(c, "new", "new@x", true)
	if e.NewAuthorName == nil || *e.NewAuthorName != "new" || e.NewAuthorEmail == nil {
		t.Error("author name/email overrides should be set")
	}
	if e.NewCommitterName == nil || e.NewCommitterEmail == nil {
		t.Error("committer should be mirrored when committer=true")
	}
	// committer=false leaves committer untouched.
	e = NewIdentityEdit(c, "new", "new@x", false)
	if e.NewCommitterName != nil || e.NewCommitterEmail != nil {
		t.Error("committer must be nil when committer=false")
	}
	// Email-only: name stays nil, email set; committer email mirrored, name nil.
	e = NewIdentityEdit(c, "", "new@x", true)
	if e.NewAuthorName != nil {
		t.Error("empty name must leave author name unchanged")
	}
	if e.NewAuthorEmail == nil || e.NewCommitterEmail == nil {
		t.Error("email-only should set author + committer email")
	}
	// No-op: requesting the current identity yields an empty Edit.
	if got := NewIdentityEdit(c, "old", "old@x", true); !got.IsEmpty() {
		t.Errorf("requesting the existing identity must yield an empty Edit, got %+v", got)
	}
}
