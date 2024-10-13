package model

import "testing"

func hunks(ids ...string) []Hunk {
	out := make([]Hunk, len(ids))
	for i, id := range ids {
		out[i] = Hunk{ID: id}
	}
	return out
}

func group(ids ...string) HunkGroup { return HunkGroup{HunkIDs: ids} }

func TestValidateCoverage_allAssignedOnce_ok(t *testing.T) {
	err := ValidateCoverage(hunks("H1", "H2", "H3"),
		[]HunkGroup{group("H1", "H3"), group("H2")})
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestValidateCoverage_missingHunk_errors(t *testing.T) {
	if err := ValidateCoverage(hunks("H1", "H2"), []HunkGroup{group("H1")}); err == nil {
		t.Fatal("expected error for unassigned hunk H2")
	}
}

func TestValidateCoverage_duplicateHunk_errors(t *testing.T) {
	if err := ValidateCoverage(hunks("H1"), []HunkGroup{group("H1"), group("H1")}); err == nil {
		t.Fatal("expected error for hunk H1 assigned twice")
	}
}

func TestValidateCoverage_unknownID_errors(t *testing.T) {
	if err := ValidateCoverage(hunks("H1"), []HunkGroup{group("H1", "H9")}); err == nil {
		t.Fatal("expected error for unknown hunk id H9")
	}
}

func TestPatch_concatenatesHeaderAndBody(t *testing.T) {
	h := Hunk{FileHeader: "diff --git a/x b/x\n--- a/x\n+++ b/x\n", Body: "@@ -1 +1 @@\n-a\n+b\n"}
	if h.Patch() != h.FileHeader+h.Body {
		t.Fatalf("unexpected patch: %q", h.Patch())
	}
}
