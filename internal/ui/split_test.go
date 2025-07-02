package ui

import (
	"strings"
	"testing"

	"github.com/wlame/regit/internal/model"
)

// sampleProposalForUI returns a fixed two-group SplitProposal for use in UI tests.
// Group 1 passes tests; group 2 is marked WIP so we can assert that the WIP
// marker appears in the rendered review.
func sampleProposalForUI() model.SplitProposal {
	return model.SplitProposal{
		CommitHash:  "abc1234567890000",
		ShouldSplit: true,
		Groups: []model.HunkGroup{
			{
				Order:     1,
				Subject:   "Refactor the first thing",
				Intent:    "cleanup",
				TestsPass: true,
				WIP:       false,
			},
			{
				Order:     2,
				Subject:   "Add the second feature",
				Intent:    "feature",
				TestsPass: false,
				WIP:       true,
			},
		},
	}
}

func TestSplitReview_rendersGroups(t *testing.T) {
	prop := sampleProposalForUI()
	sc := splitChange{commit: model.Commit{Hash: prop.CommitHash}, commitHash: prop.CommitHash, groups: prop.Groups}
	view := strings.Join(sc.preview(newStyles()).lines, "\n")
	if !strings.Contains(view, "first") || !strings.Contains(view, "WIP") {
		t.Fatalf("split review missing expected content:\n%s", view)
	}
}
