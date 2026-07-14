package strategy

import (
	"strings"
	"testing"
	"time"

	"github.com/wlame/regit/internal/model"
)

// applyPlan: 6 commits Mar 1..6 12:00 UTC, none frozen (helpers from segment_test.go).
func applyParams() Params {
	return Params{
		Location:    time.UTC,
		WeekdayMask: AllWeekdays(),
		DayStartMin: 9 * 60,
		DayEndMin:   18 * 60,
		Seed:        1,
	}
}

func TestApply_TargetsOnlySelectedCommits(t *testing.T) {
	p := segPlan(6, 0)
	targets := pick(p, 1, 3) // c2 and c4; c1,c3,c5,c6 are anchors
	prm := applyParams()
	prm.Start = time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	prm.End = time.Date(2026, 3, 6, 0, 0, 0, 0, time.UTC)
	warns, err := Apply(p, targets, EvenSpread{}, WorkingHoursModel{}, prm, true, segNow)
	if err != nil || len(warns) != 0 {
		t.Fatalf("Apply: warns=%v err=%v", warns, err)
	}
	for i, c := range p.Commits {
		_, edited := p.EditFor(c.Hash)
		wantEdited := i == 1 || i == 3
		if edited != wantEdited {
			t.Errorf("commit %s edited=%v, want %v", c.Hash, edited, wantEdited)
		}
	}
}

func TestApply_BoundaryTimesOverrideTimeModel(t *testing.T) {
	// c2 selected between anchors c1 (Mar 1 12:00) and c3 (Mar 3 12:00).
	// Compress everything onto c3's day: c2 must land on Mar 3 BEFORE 12:00,
	// even though the working-hours model would happily pick an afternoon slot.
	p := segPlan(3, 0)
	targets := pick(p, 1)
	prm := applyParams()
	prm.CompressDay = time.Date(2026, 3, 3, 0, 0, 0, 0, time.UTC)
	if _, err := Apply(p, targets, Compress{}, WorkingHoursModel{}, prm, true, segNow); err != nil {
		t.Fatal(err)
	}
	e, _ := p.EditFor("c2")
	got := *e.NewAuthorDate
	ceil := p.Commits[2].OrigAuthorDate
	if !got.Before(ceil) {
		t.Errorf("c2 %v must be before anchor c3 %v", got, ceil)
	}
	if got.Format("2006-01-02") != "2026-03-03" {
		t.Errorf("c2 landed on %s, want the compress day", got.Format("2006-01-02"))
	}
	if len(p.Violations) != 0 {
		t.Errorf("unexpected violations: %+v", p.Violations)
	}
}

func TestApply_InfeasibleSqueezeReportsViolations(t *testing.T) {
	// 5 targets between two anchors 3 seconds apart: cannot fit at minGap=1s.
	loc := time.UTC
	base := time.Date(2026, 3, 10, 12, 0, 0, 0, loc)
	commits := make([]model.Commit, 7)
	for i := range commits {
		commits[i] = model.Commit{Hash: "h" + string(rune('1'+i)), OrigAuthorDate: base}
	}
	commits[0].OrigAuthorDate = base
	commits[6].OrigAuthorDate = base.Add(3 * time.Second)
	p := model.NewPlan(commits)
	targets := pick(p, 1, 2, 3, 4, 5)
	prm := applyParams()
	prm.CompressDay = time.Date(2026, 3, 10, 0, 0, 0, 0, loc)
	if _, err := Apply(p, targets, Compress{}, WorkingHoursModel{}, prm, true, segNow); err != nil {
		t.Fatal(err)
	}
	if len(p.Violations) == 0 {
		t.Fatal("squeeze must surface as violations")
	}
	// Anchors are never edited and never listed as shifted.
	for _, h := range p.Shifted {
		if h == "h1" || h == "h7" {
			t.Errorf("anchor %s listed as shifted", h)
		}
	}
	if _, ok := p.EditFor("h7"); ok {
		t.Error("anchor h7 must not be edited")
	}
}

// TestApply_PreExistingAnchorInversionDoesNotBlockApply is the regression test
// for the Finding-1 fix: a non-monotonic run of ANCHOR dates (history this
// redate never touches) must not surface in plan.Violations, or every apply
// touching a rebased/cherry-picked repo would hard-block with no remedy.
//
// Layout (7 commits, root-first, dates all well before segNow so no ceiling
// gets today-clamped): c1,c2,c3 are anchors, with c3 (storage index 2) dated
// BEFORE c1 (storage index 0) — a rebase-shaped inversion entirely among
// anchors, surfacing as a raw FindViolations pair on the adjacent (c2, c3)
// step. c4 is the lone target, boxed between anchor c3 (floor) and anchor c5
// (ceil) with a clean, feasible window; c6,c7 are trailing anchors.
func TestApply_PreExistingAnchorInversionDoesNotBlockApply(t *testing.T) {
	loc := time.UTC
	day := func(d int) time.Time { return time.Date(2026, 3, d, 12, 0, 0, 0, loc) }
	commits := []model.Commit{
		{Hash: "c1", OrigAuthorDate: day(10)},
		{Hash: "c2", OrigAuthorDate: day(15)},
		{Hash: "c3", OrigAuthorDate: day(2)}, // BEFORE c1: anchor/anchor inversion
		{Hash: "c4", OrigAuthorDate: day(12)},
		{Hash: "c5", OrigAuthorDate: day(18)},
		{Hash: "c6", OrigAuthorDate: day(19)},
		{Hash: "c7", OrigAuthorDate: day(20)},
	}
	p := model.NewPlan(commits)
	targets := pick(p, 3) // c4 only; c3 (floor, Mar2) .. c5 (ceil, Mar18) is clean
	prm := applyParams()
	prm.CompressDay = day(10) // safely inside (c3, c5)
	if _, err := Apply(p, targets, Compress{}, WorkingHoursModel{}, prm, true, segNow); err != nil {
		t.Fatal(err)
	}
	if len(p.Violations) != 0 {
		t.Errorf("pre-existing anchor/anchor inversion must not block apply, got violations: %+v",
			p.Violations)
	}
	if _, ok := p.EditFor("c4"); !ok {
		t.Error("the target with a clean window must still be staged")
	}
	// Anchors are never edited, inversion or not.
	for _, h := range []string{"c1", "c2", "c3", "c5", "c6", "c7"} {
		if _, ok := p.EditFor(h); ok {
			t.Errorf("anchor %s must not be edited", h)
		}
	}
}

func TestApply_FutureEndClampedToToday(t *testing.T) {
	p := segPlan(3, 0)
	prm := applyParams()
	prm.Start = time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	prm.End = time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC) // far future
	if _, err := Apply(p, p.Active(), EvenSpread{}, WorkingHoursModel{}, prm, true, segNow); err != nil {
		t.Fatal(err)
	}
	for _, c := range p.Commits {
		if e, ok := p.EditFor(c.Hash); ok && e.NewAuthorDate.After(segNow) {
			t.Errorf("%s dated after now: %v", c.Hash, *e.NewAuthorDate)
		}
	}
}

func TestApply_NonIntersectingSegmentWarnsAndKeepsDates(t *testing.T) {
	p := segPlan(6, 0)
	targets := pick(p, 1, 4) // seg{c2}: window Mar1..Mar3; seg{c5}: Mar4..Mar6
	prm := applyParams()
	prm.Start = time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC) // misses seg 1 entirely
	prm.End = time.Date(2026, 3, 6, 0, 0, 0, 0, time.UTC)
	warns, err := Apply(p, targets, EvenSpread{}, WorkingHoursModel{}, prm, true, segNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "segment 1") {
		t.Fatalf("want one warning naming segment 1, got %v", warns)
	}
	if _, ok := p.EditFor("c2"); ok {
		t.Error("skipped segment's commit must keep its date")
	}
	if _, ok := p.EditFor("c5"); !ok {
		t.Error("intersecting segment must still stage")
	}
}

func TestApply_PickOwnerScopesSharedDay(t *testing.T) {
	// Shared boundary day owned by segment 0: segment 1 sees no picks → warning.
	loc := time.UTC
	day := time.Date(2026, 3, 10, 0, 0, 0, 0, loc)
	commits := []model.Commit{
		{Hash: "a", OrigAuthorDate: day.Add(-24 * time.Hour)},
		{Hash: "x", OrigAuthorDate: day.Add(-20 * time.Hour)}, // target seg 0
		{Hash: "b", OrigAuthorDate: day.Add(10 * time.Hour)},  // anchor ON day
		{Hash: "y", OrigAuthorDate: day.Add(30 * time.Hour)},  // target seg 1
	}
	p := model.NewPlan(commits)
	key := day.Format("2006-01-02")
	prm := applyParams()
	prm.Picked = map[string]bool{key: true}
	prm.PinOwner = map[string]int{key: 0}
	warns, err := Apply(p, []model.Commit{commits[1], commits[3]}, PickedDays{}, WorkingHoursModel{}, prm, true, segNow)
	if err != nil {
		t.Fatal(err)
	}
	e, ok := p.EditFor("x")
	if !ok || e.NewAuthorDate.Format("2006-01-02") != key {
		t.Fatalf("x must land on the shared day, edit=%+v ok=%v", e, ok)
	}
	if !e.NewAuthorDate.Before(commits[2].OrigAuthorDate) {
		t.Errorf("x %v must precede anchor b %v", *e.NewAuthorDate, commits[2].OrigAuthorDate)
	}
	if _, ok := p.EditFor("y"); ok {
		t.Error("segment without picks must stay unstaged")
	}
	if len(warns) != 1 {
		t.Errorf("want a warning for the pickless segment, got %v", warns)
	}
}
