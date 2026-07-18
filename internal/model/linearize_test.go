package model

import (
	"strings"
	"testing"
)

func mergeCommit(hash, subject, authorName, authorEmail string) Commit {
	return Commit{
		Hash: hash, Subject: subject, IsMerge: true,
		ParentHashes: []string{"p1" + hash, "p2" + hash},
		AuthorName:   authorName, AuthorEmail: authorEmail,
	}
}

func TestBuildLinearizePlan_titlePrecedence(t *testing.T) {
	cases := []struct {
		name         string
		mergeSubject string
		side         []SideCommit
		wantTitle    string
	}{
		{
			name:         "single folded commit uses its subject",
			mergeSubject: "Merge branch 'feature/login'",
			side:         []SideCommit{{Hash: "s1", Subject: "Add login form."}},
			wantTitle:    "Add login form.",
		},
		{
			name:         "branch name plus oldest subject",
			mergeSubject: "Merge branch 'feature/login'",
			side: []SideCommit{
				{Hash: "s1", Subject: "Add login form."},
				{Hash: "s2", Subject: "Wire the backend."},
			},
			wantTitle: "login: Add login form.",
		},
		{
			name:         "pull request pattern",
			mergeSubject: "Merge pull request #42 from wlame/fix/csrf-token",
			side: []SideCommit{
				{Hash: "s1", Subject: "Rotate the CSRF token."},
				{Hash: "s2", Subject: "Add a regression test."},
			},
			wantTitle: "csrf-token: Rotate the CSRF token.",
		},
		{
			name:         "remote-tracking pattern",
			mergeSubject: "Merge remote-tracking branch 'origin/chore/deps'",
			side: []SideCommit{
				{Hash: "s1", Subject: "Bump bubbletea."},
				{Hash: "s2", Subject: "Bump lipgloss."},
			},
			wantTitle: "deps: Bump bubbletea.",
		},
		{
			name:         "unparseable subject falls back to the merge subject",
			mergeSubject: "Integrate the payments work",
			side: []SideCommit{
				{Hash: "s1", Subject: "Add invoices."},
				{Hash: "s2", Subject: "Add refunds."},
			},
			wantTitle: "Integrate the payments work",
		},
		{
			name:         "empty side falls back to the merge subject",
			mergeSubject: "Merge branch 'feature/ghost'",
			side:         nil,
			wantTitle:    "Merge branch 'feature/ghost'",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := mergeCommit("m1", tc.mergeSubject, "Integrator", "int@example.com")
			plan := BuildLinearizePlan([]Commit{m}, map[string][]SideCommit{"m1": tc.side})
			if len(plan.Folds) != 1 {
				t.Fatalf("folds = %d, want 1", len(plan.Folds))
			}
			if got := plan.Folds[0].NewTitle; got != tc.wantTitle {
				t.Fatalf("title = %q, want %q", got, tc.wantTitle)
			}
		})
	}
}

func TestBuildLinearizePlan_bodyListsFoldedSubjectsOldestFirst(t *testing.T) {
	m := mergeCommit("m1", "Merge branch 'feature/x'", "Integrator", "int@example.com")
	side := []SideCommit{
		{Hash: "s1", Subject: "Oldest.", AuthorName: "Alice", AuthorEmail: "alice@example.com"},
		{Hash: "s2", Subject: "Newest.", AuthorName: "Bob", AuthorEmail: "bob@example.com"},
	}
	plan := BuildLinearizePlan([]Commit{m}, map[string][]SideCommit{"m1": side})
	body := plan.Folds[0].NewBody
	iOld := strings.Index(body, "- Oldest.")
	iNew := strings.Index(body, "- Newest.")
	if iOld == -1 || iNew == -1 || iOld > iNew {
		t.Fatalf("bullets missing or out of order:\n%s", body)
	}
	if !strings.Contains(body, "Co-authored-by: Alice <alice@example.com>") ||
		!strings.Contains(body, "Co-authored-by: Bob <bob@example.com>") {
		t.Fatalf("missing co-author trailers:\n%s", body)
	}
	// Trailers come after the bullets, separated by a blank line.
	if !(strings.Index(body, "Co-authored-by:") > iNew) {
		t.Fatalf("trailers must follow the bullet list:\n%s", body)
	}
}

func TestBuildLinearizePlan_coAuthorsDedupedAndMergeAuthorExcluded(t *testing.T) {
	m := mergeCommit("m1", "Merge branch 'x'", "Integrator", "int@example.com")
	side := []SideCommit{
		{Hash: "s1", Subject: "A.", AuthorName: "Alice", AuthorEmail: "alice@example.com"},
		{Hash: "s2", Subject: "B.", AuthorName: "Alice", AuthorEmail: "alice@example.com"},
		{Hash: "s3", Subject: "C.", AuthorName: "Integrator", AuthorEmail: "int@example.com"},
	}
	plan := BuildLinearizePlan([]Commit{m}, map[string][]SideCommit{"m1": side})
	f := plan.Folds[0]
	if len(f.CoAuthors) != 1 || f.CoAuthors[0].Email != "alice@example.com" {
		t.Fatalf("co-authors = %+v, want exactly Alice", f.CoAuthors)
	}
	if strings.Count(f.NewBody, "Co-authored-by:") != 1 {
		t.Fatalf("body must carry exactly one trailer:\n%s", f.NewBody)
	}
	if strings.Contains(f.NewBody, "int@example.com") {
		t.Fatalf("merge author must not be a co-author:\n%s", f.NewBody)
	}
}

func TestBuildLinearizePlan_skipsNonMerges(t *testing.T) {
	plain := Commit{Hash: "c1", Subject: "Plain.", ParentHashes: []string{"p"}}
	m := mergeCommit("m1", "Merge branch 'x'", "I", "i@example.com")
	plan := BuildLinearizePlan([]Commit{plain, m},
		map[string][]SideCommit{"m1": {{Hash: "s1", Subject: "S."}}})
	if len(plan.Folds) != 1 || plan.Folds[0].Merge.Hash != "m1" {
		t.Fatalf("plan = %+v, want one fold for m1", plan.Folds)
	}
}

func TestLinearizePlan_RewrittenCommits_suffixFromFirstFold(t *testing.T) {
	c1 := Commit{Hash: "c1"}
	m1 := mergeCommit("m1", "Merge branch 'x'", "I", "i@example.com")
	c2 := Commit{Hash: "c2"}
	commits := []Commit{c1, m1, c2}
	plan := BuildLinearizePlan(commits, map[string][]SideCommit{"m1": nil})
	got := plan.RewrittenCommits(commits)
	if len(got) != 2 || got[0].Hash != "m1" || got[1].Hash != "c2" {
		t.Fatalf("rewritten = %+v, want [m1 c2]", got)
	}
	if empty := (LinearizePlan{}).RewrittenCommits(commits); empty != nil {
		t.Fatalf("empty plan must rewrite nothing, got %+v", empty)
	}
}
