package capability

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/wlame/regit/internal/config"
)

// TestRunSplitSingleGroupNoPanic covers the guard that was dropped in an earlier
// refactor: when the LLM returns should_split=true but only 1 group (a
// "cohesive change" verdict), runSplit must print "Not splitting" and return nil
// rather than reaching BuildSubCommits with 0 trees for 1 group (panic).
//
// The test injects a stub backend that returns a valid 1-group proposal whose
// hunk_ids cover all hunks in the commit (so ValidateCoverage passes and the
// engine's own <2-group short-circuit fires, returning nil trees). The guard
// added back to runSplit catches this before printProposal.
func TestRunSplitSingleGroupNoPanic(t *testing.T) {
	_, repo := newSplittableCapabilityRepo(t)

	var out bytes.Buffer

	// MinSplitLines=1 so the small test commit (4 changed lines total) is not
	// skipped by ShouldSkipSplit before even reaching the backend.
	cfg := config.Default()
	cfg.Split.MinSplitLines = 1

	// Resolve the parent tree and fetch actual hunk IDs.
	// The commit produced by newSplittableCapabilityRepo changes two files
	// (a.txt, b.txt), so the diff always has ≥2 hunks.
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	// ReadCommits returns root-first; commits[1] is the child (HEAD).
	child := commits[1]
	parentTree, err := ParentTreeOf(repo, child)
	if err != nil {
		t.Fatal(err)
	}
	hunks, err := repo.Hunks(parentTree, child.TreeHash)
	if err != nil {
		t.Fatal(err)
	}
	if len(hunks) < 2 {
		t.Fatalf("need ≥2 hunks for the split path, got %d", len(hunks))
	}

	// Build the stub payload: should_split=true but only 1 group covering ALL
	// hunk IDs. ParseAndValidate (called by ProposeSplit) runs ValidateCoverage,
	// so the IDs must exactly match the real hunks.
	allIDs := make([]string, len(hunks))
	for i, h := range hunks {
		allIDs[i] = h.ID
	}
	type groupWire struct {
		Order   int      `json:"order"`
		HunkIDs []string `json:"hunk_ids"`
		Subject string   `json:"subject"`
	}
	type wire struct {
		ShouldSplit bool        `json:"should_split"`
		Reason      string      `json:"reason"`
		Groups      []groupWire `json:"groups"`
	}
	raw, _ := json.Marshal(wire{
		ShouldSplit: true,
		Reason:      "cohesive change",
		Groups:      []groupWire{{Order: 1, HunkIDs: allIDs, Subject: "All changes"}},
	})

	ctx := &RunContext{
		Repo:    repo,
		Cfg:     cfg,
		Stdout:  &out,
		Stderr:  &out,
		Now:     time.Unix(1000, 0),
		Backend: stubBackend{payload: string(raw)},
	}

	// --execute is set so that if the guard were absent the code would reach
	// BuildSubCommits (0 trees, 1 group → panic). With the guard restored it
	// must return nil before getting there.
	a := newParsedArgs(map[string]any{"execute": true, "no-cache": true}, []string{"HEAD"})

	before, _ := repo.ReadState()
	if err := runSplit(ctx, a); err != nil {
		t.Fatalf("runSplit: unexpected error: %v", err)
	}
	after, _ := repo.ReadState()

	if !bytes.Contains(out.Bytes(), []byte("Not splitting")) {
		t.Errorf("expected 'Not splitting' in output, got:\n%s", out.String())
	}
	if before.HeadHash != after.HeadHash {
		t.Error("runSplit with single-group proposal moved HEAD")
	}
}
