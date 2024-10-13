package strategy

import (
	"testing"
	"time"

	"github.com/wlame/regit/internal/model"
)

// helper: build N commits in root-first order with dummy hashes.
func makeCommits(n int) []model.Commit {
	cs := make([]model.Commit, n)
	for i := 0; i < n; i++ {
		cs[i] = model.Commit{Hash: string(rune('a' + i))}
	}
	return cs
}

func ts(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestFindViolations_detectsOutOfOrderChild(t *testing.T) {
	commits := makeCommits(3)
	times := []time.Time{
		ts("2026-03-01T10:00:00Z"),
		ts("2026-03-01T09:00:00Z"), // earlier than parent -> violation
		ts("2026-03-02T10:00:00Z"),
	}
	v := FindViolations(commits, times)
	if len(v) != 1 {
		t.Fatalf("expected 1 violation, got %d", len(v))
	}
	if v[0].ChildHash != "b" || v[0].ParentHash != "a" {
		t.Errorf("unexpected violation pair: %+v", v[0])
	}
}

func TestFindViolations_cleanChainHasNone(t *testing.T) {
	commits := makeCommits(3)
	times := []time.Time{
		ts("2026-03-01T10:00:00Z"),
		ts("2026-03-01T10:00:00Z"), // equal is allowed (non-decreasing)
		ts("2026-03-02T10:00:00Z"),
	}
	if v := FindViolations(commits, times); len(v) != 0 {
		t.Fatalf("expected no violations, got %d", len(v))
	}
}

func TestEnforceSequential_pushesChildForwardByMinGap(t *testing.T) {
	commits := makeCommits(2)
	times := []time.Time{
		ts("2026-03-01T10:00:00Z"),
		ts("2026-03-01T09:00:00Z"), // before parent
	}
	out := EnforceSequential(commits, times)

	want := ts("2026-03-01T10:00:00Z").Add(minGap)
	if !out[1].Equal(want) {
		t.Errorf("child not pushed to parent+minGap: got %s want %s", out[1], want)
	}
	// Result must be a valid, violation-free chain.
	if v := FindViolations(commits, out); len(v) != 0 {
		t.Errorf("output still has %d violations", len(v))
	}
}

func TestEnforceSequential_cascadesThroughMultipleCommits(t *testing.T) {
	commits := makeCommits(4)
	// Every commit claims an earlier time than the one before -> each must be
	// pushed forward past the (already corrected) previous one.
	times := []time.Time{
		ts("2026-03-01T10:00:00Z"),
		ts("2026-03-01T09:00:00Z"),
		ts("2026-03-01T08:00:00Z"),
		ts("2026-03-01T07:00:00Z"),
	}
	out := EnforceSequential(commits, times)

	for i := 1; i < len(out); i++ {
		if out[i].Before(out[i-1]) {
			t.Fatalf("index %d is before its parent: %s < %s", i, out[i], out[i-1])
		}
	}
	// The first commit must stay anchored where the strategy placed it.
	if !out[0].Equal(times[0]) {
		t.Errorf("first commit moved: got %s want %s", out[0], times[0])
	}
	// The last, after three minGap pushes, should be parent0 + 3*minGap.
	want := times[0].Add(3 * minGap)
	if !out[3].Equal(want) {
		t.Errorf("cascade end: got %s want %s", out[3], want)
	}
}

func TestEnforceSequential_leavesValidChainUntouched(t *testing.T) {
	commits := makeCommits(3)
	times := []time.Time{
		ts("2026-03-01T10:00:00Z"),
		ts("2026-03-02T10:00:00Z"),
		ts("2026-03-03T10:00:00Z"),
	}
	out := EnforceSequential(commits, times)
	for i := range times {
		if !out[i].Equal(times[i]) {
			t.Errorf("valid chain modified at %d: got %s want %s", i, out[i], times[i])
		}
	}
}

func TestEnforceSequential_doesNotMutateInput(t *testing.T) {
	commits := makeCommits(2)
	times := []time.Time{
		ts("2026-03-01T10:00:00Z"),
		ts("2026-03-01T09:00:00Z"),
	}
	original := times[1]
	_ = EnforceSequential(commits, times)
	if !times[1].Equal(original) {
		t.Errorf("input slice was mutated: %s != %s", times[1], original)
	}
}
