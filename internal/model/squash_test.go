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
	commits := chainCommits("a", "b", "c", "d", "e", "f", "g")
	// Select a,b (run); d alone (c and e unselected → singleton); f,g (run).
	chains, ignored := DetectChains(commits, selSet("a", "b", "d", "f", "g"))

	if len(chains) != 2 {
		t.Fatalf("want 2 chains, got %d", len(chains))
	}
	if got := chains[0].First().Hash + chains[0].Last().Hash; got != "ab" {
		t.Errorf("chain0 = %s, want first=a last=b", got)
	}
	if got := chains[1].First().Hash + chains[1].Last().Hash; got != "fg" {
		t.Errorf("chain1 = %s, want first=f last=g", got)
	}
	if len(ignored) != 1 || ignored[0] != "d" {
		t.Errorf("ignored = %v, want [d]", ignored)
	}
}

// Regression guard: a chain that does NOT start at the root must keep its first
// (oldest) member. Its parent being unselected is normal — that parent is the
// anchor the squash sits on — so it must never cause the first member to be
// dropped.
func TestDetectChains_chainNotStartingAtRootKeepsFirstMember(t *testing.T) {
	commits := chainCommits("a", "b", "c", "d") // a is the root
	// Select b,c,d; a (root) is unselected. The chain must be {b,c,d}, not {c,d}.
	chains, ignored := DetectChains(commits, selSet("b", "c", "d"))
	if len(chains) != 1 || len(chains[0].Members) != 3 {
		t.Fatalf("want one 3-member chain {b,c,d}, got %+v (ignored %v)", chains, ignored)
	}
	if chains[0].First().Hash != "b" {
		t.Errorf("chain must keep its first member b (parent a is the squash anchor), got first=%s", chains[0].First().Hash)
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
