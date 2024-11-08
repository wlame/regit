// internal/split/subcommits_test.go
package split

import (
	"testing"
	"time"

	"github.com/wlame/regit/internal/model"
)

func TestBuildSubCommits_datesIncrementBySecond(t *testing.T) {
	base := time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC)
	c := model.Commit{
		AuthorName: "a", AuthorEmail: "a@e", CommitterName: "a", CommitterEmail: "a@e",
		OrigAuthorDate: base, OrigCommitDate: base,
	}
	groups := []model.HunkGroup{
		{Order: 1, Subject: "first"},
		{Order: 2, Subject: "second", WIP: true},
	}
	subs := BuildSubCommits(c, groups, []string{"tree1", "tree2"}, "WIP: ")
	if len(subs) != 2 {
		t.Fatalf("want 2 subs, got %d", len(subs))
	}
	if !subs[0].AuthorDate.Equal(base) {
		t.Errorf("sub0 author date = %v, want %v", subs[0].AuthorDate, base)
	}
	if !subs[1].CommitDate.Equal(base.Add(time.Second)) {
		t.Errorf("sub1 commit date = %v, want +1s", subs[1].CommitDate)
	}
	if subs[1].Subject != "WIP: second" {
		t.Errorf("WIP subject = %q, want \"WIP: second\"", subs[1].Subject)
	}
	if subs[0].Tree != "tree1" || subs[1].Tree != "tree2" {
		t.Errorf("trees mismatched: %q %q", subs[0].Tree, subs[1].Tree)
	}
}

// TestBuildSubCommits_panicOnLengthMismatch asserts that passing a trees slice
// whose length differs from groups causes a panic with a descriptive message.
func TestBuildSubCommits_panicOnLengthMismatch(t *testing.T) {
	panicked := false
	func() {
		defer func() {
			if recover() != nil {
				panicked = true
			}
		}()
		BuildSubCommits(model.Commit{}, []model.HunkGroup{{Subject: "x"}}, []string{}, "")
	}()
	if !panicked {
		t.Fatal("expected BuildSubCommits to panic on trees/groups length mismatch")
	}
}
