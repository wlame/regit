package model

import "testing"

func selSet(hashes ...string) map[string]bool {
	m := map[string]bool{}
	for _, h := range hashes {
		m[h] = true
	}
	return m
}

// commits builds a root-first linear history with the given hashes (no metadata
// needed for detection).
func chainCommits(hashes ...string) []Commit {
	out := make([]Commit, len(hashes))
	for i, h := range hashes {
		out[i] = Commit{Hash: h}
		if i > 0 {
			out[i].ParentHashes = []string{hashes[i-1]}
		}
	}
	return out
}

func TestDetectChains_findsMaximalRunsAndDropsSingletons(t *testing.T) {
	commits := chainCommits("a", "b", "c", "d", "e", "f")
	// Select a,b (run), d (singleton), e,f (run). c is unselected.
	chains, ignored := DetectChains(commits, selSet("a", "b", "d", "e", "f"))

	if len(chains) != 2 {
		t.Fatalf("want 2 chains, got %d", len(chains))
	}
	if got := chains[0].First().Hash + chains[0].Last().Hash; got != "ab" {
		t.Errorf("chain0 = %s, want first=a last=b", got)
	}
	if got := chains[1].First().Hash + chains[1].Last().Hash; got != "ef" {
		t.Errorf("chain1 = %s, want first=e last=f", got)
	}
	if len(ignored) != 1 || ignored[0] != "d" {
		t.Errorf("ignored = %v, want [d]", ignored)
	}
}

func TestDetectChains_emptySelectionYieldsNothing(t *testing.T) {
	commits := chainCommits("a", "b", "c")
	chains, ignored := DetectChains(commits, selSet())
	if len(chains) != 0 || len(ignored) != 0 {
		t.Fatalf("want no chains/ignored, got %d/%d", len(chains), len(ignored))
	}
}

func TestDetectChains_membersAreRootFirst(t *testing.T) {
	commits := chainCommits("a", "b", "c")
	chains, _ := DetectChains(commits, selSet("a", "b", "c"))
	if len(chains) != 1 || len(chains[0].Members) != 3 {
		t.Fatalf("want one 3-member chain, got %+v", chains)
	}
	if chains[0].Members[0].Hash != "a" || chains[0].Members[2].Hash != "c" {
		t.Errorf("members not root-first: %v", chains[0].Members)
	}
}
