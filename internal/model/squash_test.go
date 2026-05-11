package model

import (
	"testing"
	"time"
)

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

func datedCommit(hash, author, email string, d time.Time) Commit {
	return Commit{Hash: hash, AuthorName: author, AuthorEmail: email, OrigAuthorDate: d}
}

func TestDefaultSquashConfig_identityFromFirstDateFromLast(t *testing.T) {
	t0 := time.Date(2026, 3, 2, 14, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 3, 9, 9, 0, 0, 0, time.UTC)
	ch := SquashChain{Members: []Commit{
		{Hash: "a", Subject: "Add model", Body: "first body", AuthorName: "ann", AuthorEmail: "ann@x", OrigAuthorDate: t0},
		{Hash: "b", Subject: "Add tests", Body: "last body", AuthorName: "wlame", AuthorEmail: "w@x", OrigAuthorDate: t1},
	}}
	got := DefaultSquashConfig(ch)
	if got.Subject != "Add model" || got.Body != "first body" {
		t.Errorf("message should come from first commit, got %q / %q", got.Subject, got.Body)
	}
	if got.AuthorName != "ann" || got.AuthorEmail != "ann@x" {
		t.Errorf("identity should come from first commit, got %s <%s>", got.AuthorName, got.AuthorEmail)
	}
	if !got.AuthorDate.Equal(t1) {
		t.Errorf("date should come from last commit, got %s want %s", got.AuthorDate, t1)
	}
}

func TestChainDateBounds_middleChainBoundedByNeighbors(t *testing.T) {
	d := func(day int) time.Time { return time.Date(2026, 3, day, 12, 0, 0, 0, time.UTC) }
	commits := []Commit{
		datedCommit("p", "x", "x", d(1)),
		datedCommit("a", "x", "x", d(2)),
		datedCommit("b", "x", "x", d(3)),
		datedCommit("n", "x", "x", d(9)),
	}
	ch := SquashChain{Members: []Commit{commits[1], commits[2]}}
	lo, hi, hasLo, hasHi := ChainDateBounds(commits, ch)
	if !hasLo || !lo.Equal(d(1)) {
		t.Errorf("lo = %s (%v), want %s", lo, hasLo, d(1))
	}
	if !hasHi || !hi.Equal(d(9)) {
		t.Errorf("hi = %s (%v), want %s", hi, hasHi, d(9))
	}
}

func TestChainDateBounds_rootAndHeadChainsAreOpen(t *testing.T) {
	d := func(day int) time.Time { return time.Date(2026, 3, day, 12, 0, 0, 0, time.UTC) }
	commits := []Commit{datedCommit("a", "x", "x", d(2)), datedCommit("b", "x", "x", d(3))}
	ch := SquashChain{Members: []Commit{commits[0], commits[1]}} // spans root..HEAD
	_, _, hasLo, hasHi := ChainDateBounds(commits, ch)
	if hasLo {
		t.Error("root chain should have no lower bound")
	}
	if hasHi {
		t.Error("HEAD chain should have no upper bound")
	}
}

func TestValidate_rejectsOutOfWindowDate(t *testing.T) {
	d := func(day int) time.Time { return time.Date(2026, 3, day, 12, 0, 0, 0, time.UTC) }
	commits := []Commit{
		datedCommit("p", "x", "x", d(1)),
		datedCommit("a", "x", "x", d(2)),
		datedCommit("b", "x", "x", d(3)),
		datedCommit("n", "x", "x", d(9)),
	}
	ch := SquashChain{Members: []Commit{commits[1], commits[2]}, AuthorDate: d(20)} // past upper bound
	if err := (SquashPlan{Chains: []SquashChain{ch}}).Validate(commits); err == nil {
		t.Error("expected out-of-window date to be rejected")
	}
	ch.AuthorDate = d(5) // within [1, 9]
	if err := (SquashPlan{Chains: []SquashChain{ch}}).Validate(commits); err != nil {
		t.Errorf("in-window date should validate, got %v", err)
	}
}

func TestValidate_rejectsOverlappingChains(t *testing.T) {
	commits := chainCommits("a", "b", "c")
	ch1 := SquashChain{Members: []Commit{commits[0], commits[1]}, AuthorDate: time.Time{}}
	ch2 := SquashChain{Members: []Commit{commits[1], commits[2]}, AuthorDate: time.Time{}}
	if err := (SquashPlan{Chains: []SquashChain{ch1, ch2}}).Validate(commits); err == nil {
		t.Error("expected overlapping chains to be rejected")
	}
}

func TestClampChainDate_pullsDefaultIntoWindow(t *testing.T) {
	d := func(day int) time.Time { return time.Date(2026, 3, day, 12, 0, 0, 0, time.UTC) }
	// Non-monotonic: the chain's last member (day 20) is later than the next commit (day 9).
	commits := []Commit{
		datedCommit("p", "x", "x", d(1)),
		datedCommit("a", "x", "x", d(2)),
		datedCommit("b", "x", "x", d(20)),
		datedCommit("n", "x", "x", d(9)),
	}
	ch := SquashChain{Members: []Commit{commits[1], commits[2]}, AuthorDate: d(20)}
	clamped := ClampChainDate(commits, ch)
	if !clamped.AuthorDate.Equal(d(9)) {
		t.Errorf("date should clamp to upper bound d(9), got %s", clamped.AuthorDate)
	}
	if err := (SquashPlan{Chains: []SquashChain{clamped}}).Validate(commits); err != nil {
		t.Errorf("clamped chain should validate, got %v", err)
	}
}

// TestRewrittenCommits_startsAtEarliestChain verifies that RewrittenCommits
// returns only the suffix from the earliest chain's first member onward, NOT
// the full commits slice — commits before that point are never re-emitted by
// gitio.Repo.ApplySquash, so pushed-detection must not see them.
func TestRewrittenCommits_startsAtEarliestChain(t *testing.T) {
	commits := chainCommits("a", "b", "c", "d", "e")
	// One chain over {c, d}: only c, d, e are ever rewritten; a, b are untouched.
	plan := SquashPlan{Chains: []SquashChain{
		{Members: []Commit{commits[2], commits[3]}},
	}}
	got := plan.RewrittenCommits(commits)
	if len(got) != 3 || got[0].Hash != "c" || got[2].Hash != "e" {
		t.Errorf("RewrittenCommits = %v, want [c d e]", hashesOf(got))
	}
}

// TestRewrittenCommits_multipleChainsUsesEarliestFirst verifies that with
// several chains, the suffix starts at the EARLIEST chain's first member, so a
// later chain's rewrite doesn't hide an earlier one's.
func TestRewrittenCommits_multipleChainsUsesEarliestFirst(t *testing.T) {
	commits := chainCommits("a", "b", "c", "d", "e", "f")
	plan := SquashPlan{Chains: []SquashChain{
		{Members: []Commit{commits[3], commits[4]}}, // d,e (later chain, listed first)
		{Members: []Commit{commits[0], commits[1]}}, // a,b (earlier chain)
	}}
	got := plan.RewrittenCommits(commits)
	if len(got) != 6 || got[0].Hash != "a" {
		t.Errorf("RewrittenCommits = %v, want the full history starting at a", hashesOf(got))
	}
}

// TestRewrittenCommits_chainNotFoundReturnsNil covers the defensive branch: if
// no chain's first member resolves in commits, RewrittenCommits returns nil
// rather than the full slice.
func TestRewrittenCommits_chainNotFoundReturnsNil(t *testing.T) {
	commits := chainCommits("a", "b")
	plan := SquashPlan{Chains: []SquashChain{
		{Members: []Commit{{Hash: "missing"}, {Hash: "also-missing"}}},
	}}
	if got := plan.RewrittenCommits(commits); got != nil {
		t.Errorf("RewrittenCommits with no resolvable chain = %v, want nil", hashesOf(got))
	}
}

// hashesOf is a small test helper for readable failure messages.
func hashesOf(commits []Commit) []string {
	out := make([]string, len(commits))
	for i, c := range commits {
		out[i] = c.Hash
	}
	return out
}
