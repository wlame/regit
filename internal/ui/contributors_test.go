package ui

import (
	"testing"
	"time"

	"github.com/wlame/regit/internal/model"
)

// contribCommit builds a minimal commit with the given author name/email.
func contribCommit(hash, name, email string) model.Commit {
	return model.Commit{
		Hash: hash, AuthorName: name, AuthorEmail: email,
		OrigAuthorDate: time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC),
	}
}

func TestBuildContributors_dedupesByEmailCaseInsensitively(t *testing.T) {
	commits := []model.Commit{
		contribCommit("h1", "Alice", "Alice@Example.com"),
		contribCommit("h2", "alice", "alice@example.com"),
		contribCommit("h3", "Bob", "bob@example.com"),
	}
	list, idx := buildContributors(commits)
	if len(list) != 2 {
		t.Fatalf("want 2 contributors, got %d: %+v", len(list), list)
	}
	// Alice (2 commits) ranks before Bob (1) — count desc.
	if list[0].Label != "Alice" || list[0].Count != 2 {
		t.Errorf("rank 0 = %+v, want Alice count 2", list[0])
	}
	if list[1].Count != 1 {
		t.Errorf("rank 1 count = %d, want 1", list[1].Count)
	}
	// The index maps both spellings of Alice's email to the same contributor.
	if idx["alice@example.com"] != 0 {
		t.Errorf("idx[alice] = %d, want 0", idx["alice@example.com"])
	}
	if idx[contributorKey(commits[0])] != idx[contributorKey(commits[1])] {
		t.Error("both Alice spellings must resolve to one index")
	}
}

func TestBuildContributors_blankEmailFallsBackToLabel(t *testing.T) {
	commits := []model.Commit{contribCommit("h1", "Nameless", "")}
	list, idx := buildContributors(commits)
	if len(list) != 1 || list[0].Key != "Nameless" {
		t.Fatalf("blank email should key on authorLabel, got %+v", list)
	}
	if idx["Nameless"] != 0 {
		t.Errorf("idx should contain the label key, got %v", idx)
	}
}

func TestBuildContributors_orderIsDeterministicByCountThenLabel(t *testing.T) {
	// Two contributors with equal counts must order by label asc.
	commits := []model.Commit{
		contribCommit("h1", "Zoe", "zoe@x.com"),
		contribCommit("h2", "Amy", "amy@x.com"),
	}
	list, _ := buildContributors(commits)
	if list[0].Label != "Amy" || list[1].Label != "Zoe" {
		t.Errorf("equal counts should sort by label asc, got %s then %s",
			list[0].Label, list[1].Label)
	}
}

func TestBuildContributors_paletteCyclesPastEnd(t *testing.T) {
	// One more contributor than the palette has colors, each with a distinct
	// count (n-i commits) so ranking is total and color-by-rank is deterministic.
	n := len(contributorPalette) + 1
	var commits []model.Commit
	for i := 0; i < n; i++ {
		name := "u" + string(rune('a'+i))
		email := name + "@x.com"
		for k := 0; k < n-i; k++ { // contributor i gets n-i commits → rank i
			commits = append(commits, contribCommit(name, name, email))
		}
	}
	list, _ := buildContributors(commits)
	if len(list) != n {
		t.Fatalf("want %d contributors, got %d", n, len(list))
	}
	// The (len(palette)+1)-th contributor (rank len(palette)) wraps back to
	// palette[0], while the top-ranked contributor gets palette[0] outright — two
	// genuinely distinct ranks, not the same slice element asserted twice.
	if list[0].Color != contributorPalette[0] {
		t.Errorf("rank 0 color = %v, want palette[0]", list[0].Color)
	}
	if list[len(contributorPalette)].Color != contributorPalette[0] {
		t.Errorf("rank %d must wrap back to palette[0]", len(contributorPalette))
	}
}

func TestBuildContributors_colorFollowsRankNotInsertionOrder(t *testing.T) {
	// A low-count author is SEEN FIRST and a high-count author LAST, so the
	// count-desc sort permutes them: insertion order (Low, High) becomes rank
	// order (High, Low). This guards the must-hold property that Color is assigned
	// by FINAL rank, not by the accumulation (insertion) order — it fails if the
	// color were set inside the counting loop before the sort.
	commits := []model.Commit{
		contribCommit("h1", "Low", "low@x.com"), // inserted first, 1 commit
		contribCommit("h2", "High", "high@x.com"),
		contribCommit("h3", "High", "high@x.com"), // inserted last, 2 commits
	}
	list, _ := buildContributors(commits)
	if len(list) != 2 {
		t.Fatalf("want 2 contributors, got %d: %+v", len(list), list)
	}
	// High (2 commits) outranks Low (1) despite being seen later.
	if list[0].Label != "High" || list[0].Count != 2 {
		t.Errorf("rank 0 = %+v, want High count 2", list[0])
	}
	// Rank 0 gets palette[0]; had color been assigned in insertion order, Low
	// (inserted first) would hold palette[0] and High would hold palette[1].
	if list[0].Color != contributorPalette[0] {
		t.Errorf("rank 0 color = %v, want palette[0] (by rank, not insertion)", list[0].Color)
	}
	if list[1].Label != "Low" || list[1].Color != contributorPalette[1] {
		t.Errorf("rank 1 = %+v, want Low with palette[1]", list[1])
	}
}
