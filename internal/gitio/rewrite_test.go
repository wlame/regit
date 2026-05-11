package gitio

import (
	"strings"
	"testing"
	"time"

	"github.com/wlame/regit/internal/model"
)

// buildRedatePlan builds a plan that redates every commit by one hour with no
// message edit — the minimal plan shape shared by tests that only care that
// Replay produces one new commit object per input commit (the encoding
// round-trip test and the signing round-trip test).
func buildRedatePlan(t *testing.T, commits []model.Commit) *model.Plan {
	t.Helper()
	plan := model.NewPlan(commits)
	for _, c := range commits {
		nd := c.OrigAuthorDate.Add(time.Hour)
		plan.SetEdit(c.Hash, model.Edit{NewAuthorDate: &nd, NewCommitDate: &nd})
	}
	return plan
}

// TestReplay_preservesNonUTF8Encoding proves that Replay round-trips a commit's
// `encoding` header. Replay always feeds the ORIGINAL raw message bytes back to
// git on stdin (see buildMessage), so if the source commit declared those bytes
// to be e.g. ISO-8859-1, the rebuilt commit must declare the same encoding —
// otherwise git would re-tag the identical bytes as UTF-8 and every reader ends
// up rendering mojibake for a commit whose content never actually changed.
func TestReplay_preservesNonUTF8Encoding(t *testing.T) {
	dir, r := initRepo(t)
	gitT(t, dir, "config", "i18n.commitEncoding", "ISO-8859-1")
	snapTree(t, dir, "café résumé") // message stored under an ISO-8859-1 header
	gitT(t, dir, "config", "--unset", "i18n.commitEncoding")

	commits, err := r.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}

	// Build a plan that redates every commit (no message edit).
	plan := buildRedatePlan(t, commits)

	newHead, _, err := r.Replay(plan, DropSignatures)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}

	// The replayed HEAD must still declare ISO-8859-1 in its raw object header.
	raw := gitT(t, dir, "cat-file", "commit", newHead)
	if !strings.Contains(raw, "encoding ISO-8859-1") {
		t.Errorf("replayed commit lost its encoding header:\n%s", raw)
	}
}
