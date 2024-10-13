package gitio

import (
	"strings"
	"testing"

	"github.com/wlame/regit/internal/model"
)

func strptr(s string) *string { return &s }

func TestBuildMessage(t *testing.T) {
	c := model.Commit{
		Message: "Fix bug\n\ndetails here",
		Subject: "Fix bug",
		Body:    "details here",
	}
	// A message whose comment lines follow the title without a blank line:
	// untouched edits must replay it verbatim, never re-structured.
	tricky := model.Commit{
		Message: "Fix bug\nfirst comment line\nsecond comment line",
		Subject: "Fix bug",
		Body:    "first comment line\nsecond comment line",
	}

	cases := []struct {
		name   string
		commit model.Commit
		edit   model.Edit
		want   string
	}{
		{"unchanged", c, model.Edit{}, "Fix bug\n\ndetails here\n"},
		{"unchanged tricky message is verbatim", tricky, model.Edit{},
			"Fix bug\nfirst comment line\nsecond comment line\n"},
		{"drop body", c, model.Edit{DropBody: true}, "Fix bug\n"},
		{"new subject", c, model.Edit{NewSubject: strptr("New title")}, "New title\n\ndetails here\n"},
		{"new body", c, model.Edit{NewBody: strptr("rewritten")}, "Fix bug\n\nrewritten\n"},
		{"drop wins over new body", c, model.Edit{DropBody: true, NewBody: strptr("ignored")}, "Fix bug\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := buildMessage(tc.commit, tc.edit); got != tc.want {
				t.Errorf("buildMessage = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBuildDateEnv_preservesIdentityOverridesDates(t *testing.T) {
	c := model.Commit{
		AuthorName: "wlame", AuthorEmail: "vvlame@gmail.com",
		CommitterName: "wlame", CommitterEmail: "vvlame@gmail.com",
	}
	d := mustParse(t, "2026-03-01T10:00:00+00:00")
	env := buildDateEnv(c, model.Edit{NewAuthorDate: &d, NewCommitDate: &d})

	joined := strings.Join(env, "\n")
	for _, want := range []string{
		"GIT_AUTHOR_NAME=wlame",
		"GIT_AUTHOR_EMAIL=vvlame@gmail.com",
		"GIT_AUTHOR_DATE=2026-03-01T10:00:00Z",
		"GIT_COMMITTER_DATE=2026-03-01T10:00:00Z",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("env missing %q; got:\n%s", want, joined)
		}
	}
}

func TestRemapParents(t *testing.T) {
	remap := map[string]string{"old1": "new1", "old2": "new2"}

	if got := remapParents(nil, remap); got != nil {
		t.Errorf("root commit should have nil parents, got %v", got)
	}

	// Linear: first parent remapped.
	got := remapParents([]string{"old1"}, remap)
	if len(got) != 1 || got[0] != "new1" {
		t.Errorf("linear remap = %v, want [new1]", got)
	}

	// Merge: first parent remapped, side parent left alone.
	got = remapParents([]string{"old1", "sideX"}, remap)
	if got[0] != "new1" || got[1] != "sideX" {
		t.Errorf("merge remap = %v, want [new1 sideX]", got)
	}
}

func TestSignatureStatus(t *testing.T) {
	for _, code := range []string{"G", "U", "X", "Y", "R", "E"} {
		if !signatureStatus(code) {
			t.Errorf("code %q should indicate a signature", code)
		}
	}
	for _, code := range []string{"N", "", " "} {
		if signatureStatus(code) {
			t.Errorf("code %q should indicate no signature", code)
		}
	}
}

func TestShortAndBranchRef(t *testing.T) {
	if short("abcdef1234567890") != "abcdef12" {
		t.Errorf("short hash wrong: %s", short("abcdef1234567890"))
	}
	if short("abc") != "abc" {
		t.Errorf("short of tiny hash should pass through")
	}
	if BranchRef("main") != "refs/heads/main" {
		t.Errorf("BranchRef wrong: %s", BranchRef("main"))
	}
}

func TestReplay_emptyPlanErrors(t *testing.T) {
	repo, _ := testRepo(t)
	if _, err := repo.Replay(model.NewPlan(nil), DropSignatures); err == nil {
		t.Error("expected error replaying empty plan")
	}
}
