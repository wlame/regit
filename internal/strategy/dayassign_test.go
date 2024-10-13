package strategy

import (
	"testing"
	"time"
)

func day(s string) time.Time { return ts(s + "T00:00:00Z") }

func paramsRange(start, end string) Params {
	return Params{
		Start:       day(start),
		End:         day(end),
		Location:    time.UTC,
		WeekdayMask: AllWeekdays(),
		Seed:        1,
	}
}

func TestDistributeEvenly_fewerItemsThanDaysSpansRange(t *testing.T) {
	// 3 items across 5 days should hit the first and last day and spread between.
	got := distributeEvenly(3, 5)
	want := []int{0, 2, 4}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("distributeEvenly(3,5) = %v, want %v", got, want)
		}
	}
}

func TestDistributeEvenly_moreItemsThanDaysStacksContiguously(t *testing.T) {
	// 5 items across 2 days: first day gets ceil (3), second gets floor (2).
	got := distributeEvenly(5, 2)
	want := []int{0, 0, 0, 1, 1}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("distributeEvenly(5,2) = %v, want %v", got, want)
		}
	}
}

func TestDistributeEvenly_singleItem(t *testing.T) {
	if got := distributeEvenly(1, 7); len(got) != 1 || got[0] != 0 {
		t.Fatalf("distributeEvenly(1,7) = %v, want [0]", got)
	}
}

func TestEvenSpread_assignsOneDayPerCommitInOrder(t *testing.T) {
	commits := makeCommits(4)
	p := paramsRange("2026-03-01", "2026-03-31")
	days, err := EvenSpread{}.AssignDays(commits, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 4 {
		t.Fatalf("expected 4 days, got %d", len(days))
	}
	for i := 1; i < len(days); i++ {
		if days[i].Before(days[i-1]) {
			t.Errorf("days not non-decreasing at %d: %s < %s", i, days[i], days[i-1])
		}
	}
	if !days[0].Equal(day("2026-03-01")) {
		t.Errorf("first commit should anchor to range start, got %s", days[0])
	}
}

func TestWorkingDays_onlyPlacesOnWeekdays(t *testing.T) {
	commits := makeCommits(10)
	p := paramsRange("2026-03-01", "2026-03-31")
	p.WeekdayMask = WeekdayMask{} // empty -> WorkingDays should default to Mon-Fri
	days, err := WorkingDays{}.AssignDays(commits, p)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range days {
		wd := d.Weekday()
		if wd == time.Saturday || wd == time.Sunday {
			t.Errorf("commit placed on weekend: %s (%s)", d, wd)
		}
	}
}

func TestWeekends_onlyPlacesOnWeekends(t *testing.T) {
	commits := makeCommits(6)
	p := paramsRange("2026-03-01", "2026-03-31")
	days, err := Weekends{}.AssignDays(commits, p)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range days {
		wd := d.Weekday()
		if wd != time.Saturday && wd != time.Sunday {
			t.Errorf("commit placed off-weekend: %s (%s)", d, wd)
		}
	}
}

func TestOffsetShift_addsDaysAndHoursPreservingTime(t *testing.T) {
	commits := makeCommits(1)
	commits[0].OrigAuthorDate = ts("2026-01-10T13:30:00Z")
	p := Params{Location: time.UTC, OffsetDays: 5, OffsetHours: 2}
	times, ok := OffsetShift{}.PreservedTimes(commits, nil, p)
	if !ok {
		t.Fatal("OffsetShift should preserve its own times")
	}
	want := ts("2026-01-15T15:30:00Z")
	if !times[0].Equal(want) {
		t.Errorf("offset shift = %s, want %s", times[0], want)
	}
}

func TestPreserveRelative_keepsSpacingFromAnchor(t *testing.T) {
	commits := makeCommits(3)
	commits[0].OrigAuthorDate = ts("2026-01-01T00:00:00Z")
	commits[1].OrigAuthorDate = ts("2026-01-03T00:00:00Z") // +2d
	commits[2].OrigAuthorDate = ts("2026-01-06T00:00:00Z") // +5d
	p := Params{Location: time.UTC, Anchor: ts("2026-06-01T00:00:00Z"), Scale: 1.0}
	times, _ := PreserveRelative{}.PreservedTimes(commits, nil, p)
	if !times[0].Equal(ts("2026-06-01T00:00:00Z")) {
		t.Errorf("first should equal anchor, got %s", times[0])
	}
	if !times[1].Equal(ts("2026-06-03T00:00:00Z")) {
		t.Errorf("second should keep +2d gap, got %s", times[1])
	}
	if !times[2].Equal(ts("2026-06-06T00:00:00Z")) {
		t.Errorf("third should keep +5d gap, got %s", times[2])
	}
}

func TestPreserveRelative_scaleHalvesGaps(t *testing.T) {
	commits := makeCommits(2)
	commits[0].OrigAuthorDate = ts("2026-01-01T00:00:00Z")
	commits[1].OrigAuthorDate = ts("2026-01-05T00:00:00Z") // +4d
	p := Params{Location: time.UTC, Anchor: ts("2026-06-01T00:00:00Z"), Scale: 0.5}
	times, _ := PreserveRelative{}.PreservedTimes(commits, nil, p)
	if !times[1].Equal(ts("2026-06-03T00:00:00Z")) { // 4d * 0.5 = 2d
		t.Errorf("scaled gap = %s, want 2026-06-03", times[1])
	}
}

func TestBackwardsFromDeadline_oldestFurthestBack(t *testing.T) {
	commits := makeCommits(3)
	p := Params{
		Location:    time.UTC,
		Deadline:    day("2026-03-10"),
		WeekdayMask: AllWeekdays(),
	}
	days, err := BackwardsFromDeadline{}.AssignDays(commits, p)
	if err != nil {
		t.Fatal(err)
	}
	// Newest commit (index 2, root-first) lands on the deadline.
	if !days[2].Equal(day("2026-03-10")) {
		t.Errorf("newest commit should land on deadline, got %s", days[2])
	}
	// Oldest commit (index 0) is the furthest back.
	if !days[0].Before(days[1]) || !days[1].Before(days[2]) {
		t.Errorf("commits not ordered oldest-first: %v", days)
	}
}

func TestCompress_allCommitsOnOneDay(t *testing.T) {
	commits := makeCommits(5)
	p := Params{Location: time.UTC, CompressDay: day("2026-04-15")}
	days, err := Compress{}.AssignDays(commits, p)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range days {
		if !d.Equal(day("2026-04-15")) {
			t.Errorf("compress placed commit off target day: %s", d)
		}
	}
}

func TestRateFill_placesAllCommitsWithinBounds(t *testing.T) {
	commits := makeCommits(7)
	p := paramsRange("2026-03-01", "2026-03-31")
	p.PerDayMin, p.PerDayMax = 2, 3
	days, err := RateFill{}.AssignDays(commits, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 7 {
		t.Fatalf("expected 7 placements, got %d", len(days))
	}
	// Count per day must never exceed PerDayMax.
	counts := map[string]int{}
	for _, d := range days {
		counts[dayKey(d)]++
	}
	for k, n := range counts {
		if n > 3 {
			t.Errorf("day %s got %d commits, exceeds max 3", k, n)
		}
	}
}

func TestRateFill_deterministicForSeed(t *testing.T) {
	commits := makeCommits(7)
	p := paramsRange("2026-03-01", "2026-03-31")
	p.PerDayMin, p.PerDayMax = 1, 4
	a, _ := RateFill{}.AssignDays(commits, p)
	b, _ := RateFill{}.AssignDays(commits, p)
	for i := range a {
		if !a[i].Equal(b[i]) {
			t.Fatalf("RateFill not deterministic at %d: %s vs %s", i, a[i], b[i])
		}
	}
}

func TestManualPin_fillsAccordingToQuotas(t *testing.T) {
	commits := makeCommits(4)
	p := Params{Location: time.UTC, Quotas: map[string]int{
		"2026-03-01": 1,
		"2026-03-03": 2,
	}}
	days, err := ManualPin{}.AssignDays(commits, p)
	if err != nil {
		t.Fatal(err)
	}
	if !days[0].Equal(day("2026-03-01")) {
		t.Errorf("commit 0 should be on 03-01, got %s", days[0])
	}
	if !days[1].Equal(day("2026-03-03")) || !days[2].Equal(day("2026-03-03")) {
		t.Errorf("commits 1,2 should be on 03-03, got %s,%s", days[1], days[2])
	}
	// 4th commit overflows quotas -> stacks on the last quota day (03-03).
	if !days[3].Equal(day("2026-03-03")) {
		t.Errorf("overflow commit should stack on last quota day, got %s", days[3])
	}
}

// A pin is an explicit instruction: weekday masks and date ranges never
// filter pinned days (2026-03-07 is a Saturday).
func TestManualPin_pinnedDayBeatsWeekdayMask(t *testing.T) {
	commits := makeCommits(1)
	p := paramsRange("2026-03-01", "2026-03-05") // range that excludes the pin
	p.WeekdayMask = WeekdayMask{false, true, true, true, true, true, false}
	p.Quotas = map[string]int{"2026-03-07": 1}
	days, err := ManualPin{}.AssignDays(commits, p)
	if err != nil {
		t.Fatal(err)
	}
	if !days[0].Equal(day("2026-03-07")) {
		t.Errorf("pinned Saturday should be honored, got %s", days[0])
	}
}

func TestManualPin_allZeroQuotasErrors(t *testing.T) {
	commits := makeCommits(2)
	p := Params{Location: time.UTC, Quotas: map[string]int{"2026-03-01": 0}}
	if _, err := (ManualPin{}).AssignDays(commits, p); err == nil {
		t.Error("all-zero quotas should be an error")
	}
}

func TestPickedDays_randomSplitCoversAllDays(t *testing.T) {
	commits := makeCommits(10)
	p := Params{Location: time.UTC, Seed: 7, Picked: map[string]bool{
		"2026-03-02": true,
		"2026-03-05": true,
		"2026-03-09": true,
	}}
	days, err := PickedDays{}.AssignDays(commits, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 10 {
		t.Fatalf("every commit must get a day, got %d", len(days))
	}

	counts := map[string]int{}
	for i, d := range days {
		counts[d.Format("2006-01-02")]++
		if i > 0 && days[i].Before(days[i-1]) {
			t.Fatal("assigned days must be chronological along the commit order")
		}
	}
	if len(counts) != 3 {
		t.Errorf("with 10 commits every picked day should receive at least one, got %v", counts)
	}
	for key := range counts {
		if !p.Picked[key] {
			t.Errorf("commit landed on unpicked day %s", key)
		}
	}

	// Deterministic per seed: a second run gives the identical split.
	again, _ := PickedDays{}.AssignDays(commits, p)
	for i := range days {
		if !days[i].Equal(again[i]) {
			t.Fatal("picked-days split must be deterministic for a fixed seed")
		}
	}
}

func TestPickedDays_fewerCommitsThanDays(t *testing.T) {
	commits := makeCommits(2)
	p := Params{Location: time.UTC, Seed: 1, Picked: map[string]bool{
		"2026-03-02": true, "2026-03-03": true, "2026-03-04": true, "2026-03-05": true,
	}}
	days, err := PickedDays{}.AssignDays(commits, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 2 {
		t.Fatalf("both commits must be placed, got %d", len(days))
	}
	for _, d := range days {
		if !p.Picked[d.Format("2006-01-02")] {
			t.Errorf("commit landed on unpicked day %s", d)
		}
	}
}

func TestPickedDays_noDaysErrors(t *testing.T) {
	if _, err := (PickedDays{}).AssignDays(makeCommits(2), Params{Location: time.UTC}); err == nil {
		t.Error("no picked days should be an error")
	}
}

func TestEvenSpread_emptyRangeErrors(t *testing.T) {
	commits := makeCommits(2)
	p := Params{
		Start:       day("2026-03-07"), // Saturday
		End:         day("2026-03-07"),
		Location:    time.UTC,
		WeekdayMask: WorkdaysMask(), // excludes Saturday -> no eligible days
	}
	if _, err := (EvenSpread{}).AssignDays(commits, p); err == nil {
		t.Fatal("expected error for range with no eligible days")
	}
}
