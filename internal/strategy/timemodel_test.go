package strategy

import (
	"math/rand"
	"testing"
	"time"

	"github.com/wlame/regit/internal/model"
)

// farFutureNow is a fixed clock for tests that migrated off the old
// Apply(plan, s, tm, p, link) signature: it must sit after every test's date
// range so the new today-ceiling in SegmentTargets never clamps a distribution
// that these tests' original expectations assumed lands wherever the strategy
// placed it.
var farFutureNow = time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)

func TestWorkingHours_staysWithinWindow(t *testing.T) {
	commits := makeCommits(20)
	days := make([]time.Time, 20)
	for i := range days {
		days[i] = day("2026-03-02") // all same day to stress the window
	}
	p := Params{Location: time.UTC, DayStartMin: 9 * 60, DayEndMin: 18 * 60}
	rng := rand.New(rand.NewSource(1))
	times, err := WorkingHoursModel{}.AssignTimes(commits, days, p, rng)
	if err != nil {
		t.Fatal(err)
	}
	for _, tm := range times {
		mins := tm.Hour()*60 + tm.Minute()
		if mins < 9*60 || mins > 18*60 {
			t.Errorf("time %s outside working window", tm)
		}
	}
}

func TestTimeModel_sameDayCommitsAreNonDecreasing(t *testing.T) {
	commits := makeCommits(15)
	days := make([]time.Time, 15)
	for i := range days {
		days[i] = day("2026-03-02")
	}
	p := Params{Location: time.UTC}
	rng := rand.New(rand.NewSource(7))
	times, _ := UniformModel{}.AssignTimes(commits, days, p, rng)
	for i := 1; i < len(times); i++ {
		if times[i].Before(times[i-1]) {
			t.Errorf("same-day commits not ordered at %d: %s < %s", i, times[i], times[i-1])
		}
	}
}

func TestPreserveTimeModel_keepsOriginalClockTime(t *testing.T) {
	commits := makeCommits(1)
	commits[0].OrigAuthorDate = ts("2026-01-01T14:37:00Z")
	days := []time.Time{day("2026-09-09")}
	p := Params{Location: time.UTC}
	times, _ := PreserveTimeModel{}.AssignTimes(commits, days, p, rand.New(rand.NewSource(1)))
	if times[0].Hour() != 14 || times[0].Minute() != 37 {
		t.Errorf("preserve model dropped clock time: %s", times[0])
	}
	if !day(times[0].Format("2006-01-02")).Equal(day("2026-09-09")) {
		t.Errorf("preserve model used wrong day: %s", times[0])
	}
}

func TestFixedModel_ordersSameDayCommits(t *testing.T) {
	commits := makeCommits(3)
	days := []time.Time{day("2026-03-02"), day("2026-03-02"), day("2026-03-02")}
	p := Params{Location: time.UTC, FixedMin: 12 * 60}
	times, _ := FixedModel{}.AssignTimes(commits, days, p, rand.New(rand.NewSource(1)))
	for i := 1; i < len(times); i++ {
		if !times[i].After(times[i-1]) {
			t.Errorf("fixed model should strictly order same-day commits: %v", times)
		}
	}
}

func TestApply_endToEndProducesValidLinkedEdits(t *testing.T) {
	commits := makeCommits(6)
	plan := model.NewPlan(commits)
	p := paramsRange("2026-03-01", "2026-03-31")
	p.DayStartMin, p.DayEndMin = 9*60, 18*60

	_, err := Apply(plan, plan.Active(), EvenSpread{}, WorkingHoursModel{}, p, true, farFutureNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Edits) != 6 {
		t.Fatalf("expected 6 edits, got %d", len(plan.Edits))
	}
	if !plan.IsValid() {
		t.Errorf("plan should be sequence-valid, has %d violations", len(plan.Violations))
	}
	// Linked dates: author == committer for every edit.
	for hash, e := range plan.Edits {
		if e.NewAuthorDate == nil || e.NewCommitDate == nil {
			t.Fatalf("edit %s missing a date", hash)
		}
		if !e.NewAuthorDate.Equal(*e.NewCommitDate) {
			t.Errorf("edit %s author != committer date", hash)
		}
	}
}

// With a base boundary set, Apply must leave frozen commits unedited and date
// the first active commit after its frozen parent, even when the requested
// range starts earlier.
func TestApply_respectsBaseBoundary(t *testing.T) {
	commits := makeCommits(4)
	parentDate := ts("2026-06-05T14:00:00Z")
	commits[1].OrigAuthorDate = parentDate
	plan := model.NewPlan(commits)
	plan.SetBase(2) // freeze commits[0] and commits[1]

	// Range deliberately starts three months BEFORE the frozen parent's date,
	// but must still extend past it: the segment engine's floor/ceil window
	// (Task 1) intersects the requested range with the frozen parent's date
	// rather than silently overriding it, so a range that never reaches the
	// parent's date would correctly have no overlap at all. Ending well after
	// parentDate keeps the test's "starts before, must still land after" intent
	// meaningful under that intersection.
	p := paramsRange("2026-03-01", "2026-12-31")
	_, err := Apply(plan, plan.Active(), EvenSpread{}, UniformModel{}, p, true, farFutureNow)
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range commits[:2] {
		if _, ok := plan.Edits[c.Hash]; ok {
			t.Errorf("frozen commit %s must not be edited", c.Hash)
		}
	}
	if len(plan.Edits) != 2 {
		t.Fatalf("expected 2 edits for the active commits, got %d", len(plan.Edits))
	}
	first := plan.EffectiveAuthorDate(commits[2])
	if !first.After(parentDate) {
		t.Errorf("first active commit (%v) must be dated after its frozen parent (%v)", first, parentDate)
	}
	if !plan.IsValid() {
		t.Errorf("plan should be sequence-valid, has %d violations", len(plan.Violations))
	}
}

func TestApply_unlinkedLeavesCommitterDateAlone(t *testing.T) {
	commits := makeCommits(2)
	plan := model.NewPlan(commits)
	p := paramsRange("2026-03-01", "2026-03-31")
	_, err := Apply(plan, plan.Active(), EvenSpread{}, UniformModel{}, p, false, farFutureNow)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range plan.Edits {
		if e.NewCommitDate != nil {
			t.Errorf("unlinked apply should not set committer date")
		}
	}
}

func TestApply_offsetShiftBypassesTimeModel(t *testing.T) {
	commits := makeCommits(2)
	commits[0].OrigAuthorDate = ts("2026-01-01T08:00:00Z")
	commits[1].OrigAuthorDate = ts("2026-01-02T09:30:00Z")
	plan := model.NewPlan(commits)
	p := Params{Location: time.UTC, OffsetDays: 10}
	// Pass a time model that would override times if it were (incorrectly) used.
	_, err := Apply(plan, plan.Active(), OffsetShift{}, FixedModel{}, p, true, farFutureNow)
	if err != nil {
		t.Fatal(err)
	}
	e := plan.Edits[commits[0].Hash]
	want := ts("2026-01-11T08:00:00Z") // original time preserved, +10 days
	if !e.NewAuthorDate.Equal(want) {
		t.Errorf("offset shift via Apply = %s, want %s", e.NewAuthorDate, want)
	}
}
