package model

import (
	"strings"
	"testing"
)

func TestChangeSet_EqualIgnoresInputOrder(t *testing.T) {
	a := NewChangeSet([]PathChange{
		{Path: "b.txt", Status: Modified},
		{Path: "a.txt", Status: Added},
	})
	b := NewChangeSet([]PathChange{
		{Path: "a.txt", Status: Added},
		{Path: "b.txt", Status: Modified},
	})
	if !a.Equal(b) {
		t.Errorf("change-sets with same content in different order should be equal")
	}
}

func TestChangeSet_EmptyMeansIdentical(t *testing.T) {
	var empty ChangeSet
	if !empty.IsEmpty() {
		t.Error("zero-value ChangeSet should be empty")
	}
	if !empty.Equal(ChangeSet{}) {
		t.Error("two empty change-sets should be equal")
	}
}

func TestChangeSet_MismatchReportsUnexpectedAndMissing(t *testing.T) {
	expected := NewChangeSet([]PathChange{
		{Path: "keep.txt", Status: Deleted},
		{Path: "edit.txt", Status: Modified},
	})
	actual := NewChangeSet([]PathChange{
		{Path: "edit.txt", Status: Modified},  // matches
		{Path: "surprise.txt", Status: Added}, // unexpected
		// keep.txt missing -> expected delete did not happen
	})
	diffs := expected.Mismatch(actual)
	joined := strings.Join(diffs, " | ")
	if expected.Equal(actual) {
		t.Fatal("expected and actual should NOT be equal")
	}
	if !strings.Contains(joined, "surprise.txt") {
		t.Errorf("mismatch should flag the unexpected path: %s", joined)
	}
	if !strings.Contains(joined, "keep.txt") {
		t.Errorf("mismatch should flag the missing expected change: %s", joined)
	}
}

func TestChangeSet_MismatchReportsWrongStatus(t *testing.T) {
	expected := NewChangeSet([]PathChange{{Path: "x.txt", Status: Deleted}})
	actual := NewChangeSet([]PathChange{{Path: "x.txt", Status: Modified}})
	if expected.Equal(actual) {
		t.Fatal("differing status should not be equal")
	}
	diffs := expected.Mismatch(actual)
	if len(diffs) != 1 || !strings.Contains(diffs[0], "x.txt") {
		t.Errorf("expected one status-mismatch diff for x.txt, got %v", diffs)
	}
}

func TestVerificationSummary_String(t *testing.T) {
	got := VerificationSummary{Commits: 14, ChangedPaths: 0}.String()
	if got != "verified 14 commits, 0 paths changed" {
		t.Errorf("String() = %q", got)
	}
	one := VerificationSummary{Commits: 1, ChangedPaths: 1}.String()
	if one != "verified 1 commit, 1 path changed" {
		t.Errorf("singular String() = %q", one)
	}
}
