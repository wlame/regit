package strategy

import (
	"testing"
	"time"

	"github.com/wlame/regit/internal/model"
)

// segPlan builds a plan of n commits, one per day starting 2026-03-01, hashes
// "c1".."cN" root-first, and freezes the first `frozen` commits.
func segPlan(n, frozen int) *model.Plan {
	loc := time.UTC
	commits := make([]model.Commit, n)
	for i := range commits {
		commits[i] = model.Commit{
			Hash:           "c" + string(rune('1'+i)),
			OrigAuthorDate: time.Date(2026, 3, 1+i, 12, 0, 0, 0, loc),
		}
	}
	p := model.NewPlan(commits)
	p.SetBase(frozen)
	return p
}

func pick(p *model.Plan, idxs ...int) []model.Commit {
	out := make([]model.Commit, 0, len(idxs))
	for _, i := range idxs {
		out = append(out, p.Commits[i])
	}
	return out
}

var segNow = time.Date(2026, 3, 20, 18, 0, 0, 0, time.UTC)

func TestSegmentTargets_SpecExample(t *testing.T) {
	// 10 commits, selection {2,4,5,8,9,10} 1-based → storage idx {1,3,4,7,8,9}.
	p := segPlan(10, 0)
	segs := SegmentTargets(p, pick(p, 1, 3, 4, 7, 8, 9), segNow)
	if len(segs) != 3 {
		t.Fatalf("want 3 segments, got %d", len(segs))
	}
	// Segment 1 = {c2}: floor c1 (Mar 1 12:00), ceil c3 (Mar 3 12:00).
	if got := segs[0].Commits[0].Hash; got != "c2" {
		t.Errorf("seg0 commit = %s", got)
	}
	if !segs[0].HasFloor || !segs[0].Floor.Equal(p.Commits[0].OrigAuthorDate) {
		t.Errorf("seg0 floor = %v (hasFloor=%v)", segs[0].Floor, segs[0].HasFloor)
	}
	if !segs[0].Ceil.Equal(p.Commits[2].OrigAuthorDate) {
		t.Errorf("seg0 ceil = %v", segs[0].Ceil)
	}
	// Segment 2 = {c4,c5}: floor c3, ceil c6.
	if len(segs[1].Commits) != 2 || segs[1].Commits[0].Hash != "c4" {
		t.Errorf("seg1 commits wrong: %+v", segs[1].Commits)
	}
	// Segment 3 = {c8,c9,c10}: floor c7, ceil = now (contains HEAD).
	if !segs[2].Ceil.Equal(segNow) {
		t.Errorf("seg2 ceil = %v, want now", segs[2].Ceil)
	}
	// Each segment records the storage index of its first commit.
	for i, wantFirst := range []int{1, 3, 7} {
		if segs[i].First != wantFirst {
			t.Errorf("seg%d First = %d, want %d", i, segs[i].First, wantFirst)
		}
	}
}

func TestSegmentTargets_NoSelectionIsOneSegment(t *testing.T) {
	p := segPlan(5, 2) // c1,c2 frozen
	segs := SegmentTargets(p, p.Active(), segNow)
	if len(segs) != 1 {
		t.Fatalf("want 1 segment, got %d", len(segs))
	}
	s := segs[0]
	if len(s.Commits) != 3 || !s.HasFloor || !s.Floor.Equal(p.Commits[1].OrigAuthorDate) {
		t.Errorf("degenerate segment wrong: %+v", s)
	}
	if !s.Ceil.Equal(segNow) {
		t.Errorf("ceil = %v, want now", s.Ceil)
	}
}

func TestSegmentTargets_RootSegmentHasNoFloor(t *testing.T) {
	p := segPlan(3, 0)
	segs := SegmentTargets(p, pick(p, 0), segNow)
	if len(segs) != 1 || segs[0].HasFloor {
		t.Fatalf("root segment must be floorless: %+v", segs)
	}
}

func TestSegmentTargets_SameDayAnchorRun(t *testing.T) {
	// B,C,D (idx 1,2,3) anchors on ONE day: floor of the following segment is
	// D's time (last anchor), ceil of the preceding segment is B's time (first).
	loc := time.UTC
	day := time.Date(2026, 3, 10, 0, 0, 0, 0, loc)
	commits := []model.Commit{
		{Hash: "a", OrigAuthorDate: day.Add(-48 * time.Hour)},
		{Hash: "b", OrigAuthorDate: day.Add(9 * time.Hour)},
		{Hash: "c", OrigAuthorDate: day.Add(12 * time.Hour)},
		{Hash: "d", OrigAuthorDate: day.Add(15 * time.Hour)},
		{Hash: "e", OrigAuthorDate: day.Add(72 * time.Hour)},
	}
	p := model.NewPlan(commits)
	segs := SegmentTargets(p, []model.Commit{commits[0], commits[4]}, segNow)
	if len(segs) != 2 {
		t.Fatalf("want 2 segments, got %d", len(segs))
	}
	if !segs[0].Ceil.Equal(commits[1].OrigAuthorDate) { // B, first anchor
		t.Errorf("seg0 ceil = %v, want B's time", segs[0].Ceil)
	}
	if !segs[1].Floor.Equal(commits[3].OrigAuthorDate) { // D, last anchor
		t.Errorf("seg1 floor = %v, want D's time", segs[1].Floor)
	}
}

func TestSegmentTargets_FrozenTargetIsIgnored(t *testing.T) {
	p := segPlan(4, 2)
	segs := SegmentTargets(p, pick(p, 0, 3), segNow) // c1 frozen → anchor
	if len(segs) != 1 || segs[0].Commits[0].Hash != "c4" {
		t.Fatalf("frozen target must not form a segment: %+v", segs)
	}
}

func TestWindows_InclusiveDaysAndTodayCap(t *testing.T) {
	p := segPlan(10, 0)
	segs := SegmentTargets(p, pick(p, 1, 3, 4, 7, 8, 9), segNow)
	wins := Windows(segs, time.UTC, segNow)
	day := func(d int) time.Time { return time.Date(2026, 3, d, 0, 0, 0, 0, time.UTC) }
	cases := []struct {
		start, end time.Time
		size       int
	}{
		{day(1), day(3), 1},
		{day(3), day(6), 2},
		{day(7), day(20), 3}, // capped at today, not some future anchor
	}
	for i, c := range cases {
		if !wins[i].StartDay.Equal(c.start) || !wins[i].EndDay.Equal(c.end) || wins[i].Size != c.size {
			t.Errorf("win[%d] = %+v, want %v..%v size %d", i, wins[i], c.start, c.end, c.size)
		}
	}
	// Shared boundary day 3 belongs to BOTH windows 0 and 1.
	if got := SegmentsForDay(wins, day(3), time.UTC); len(got) != 2 || got[0] != 0 || got[1] != 1 {
		t.Errorf("SegmentsForDay(day3) = %v, want [0 1]", got)
	}
	if got := SegmentsForDay(wins, day(21), time.UTC); len(got) != 0 {
		t.Errorf("future day must match nothing, got %v", got)
	}
}

func TestAttributePins(t *testing.T) {
	day := func(d int) string {
		return time.Date(2026, 3, d, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
	}
	wins := []Window{
		{StartDay: mustDay(t, day(1)), EndDay: mustDay(t, day(3)), Size: 1},
		{StartDay: mustDay(t, day(3)), EndDay: mustDay(t, day(6)), Size: 2},
	}
	unit := func(days ...string) map[string]int {
		m := map[string]int{}
		for _, d := range days {
			m[d] = 1
		}
		return m
	}

	// Shared day 3, both free → earlier segment (0).
	owner, overflow := AttributePins([]string{day(3)}, unit(day(3)), wins, time.UTC)
	if owner[day(3)] != 0 || len(overflow) != 0 {
		t.Errorf("shared-day both-free: owner=%v overflow=%v", owner, overflow)
	}

	// Segment 0 already holds day 2 → shared day 3 goes to segment 1 (capacity preference).
	owner, overflow = AttributePins([]string{day(2), day(3)}, unit(day(2), day(3)), wins, time.UTC)
	if owner[day(2)] != 0 || owner[day(3)] != 1 || len(overflow) != 0 {
		t.Errorf("capacity preference: owner=%v overflow=%v", owner, overflow)
	}

	// Everything full → overflow, attributed to the earlier candidate.
	owner, overflow = AttributePins(
		[]string{day(2), day(4), day(5), day(3)},
		unit(day(2), day(4), day(5), day(3)), wins, time.UTC)
	if owner[day(3)] != 0 || len(overflow) != 1 || overflow[0] != day(3) {
		t.Errorf("both-full: owner=%v overflow=%v", owner, overflow)
	}

	// A day outside every window is dropped entirely.
	owner, _ = AttributePins([]string{day(9)}, unit(day(9)), wins, time.UTC)
	if _, ok := owner[day(9)]; ok {
		t.Errorf("out-of-window day must be dropped, owner=%v", owner)
	}

	// Quota weights: pin of 2 fills segment 1 (size 2); the next pin overflows.
	owner, overflow = AttributePins(
		[]string{day(4), day(5)}, map[string]int{day(4): 2, day(5): 1}, wins, time.UTC)
	if owner[day(4)] != 1 || len(overflow) != 1 || overflow[0] != day(5) {
		t.Errorf("weighted: owner=%v overflow=%v", owner, overflow)
	}
}

func mustDay(t *testing.T, key string) time.Time {
	t.Helper()
	d, err := time.ParseInLocation("2006-01-02", key, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	return d
}
