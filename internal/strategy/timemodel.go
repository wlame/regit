package strategy

import (
	"math/rand"
	"sort"
	"time"

	"github.com/wlame/ghist/internal/model"
)

// This file implements Layer B: time-of-day models. Each model receives the day
// every commit was assigned to and returns full timestamps. The shared helper
// assignWithinDays groups commits by day and asks the model for one clock time
// (in minutes from midnight) per commit, then sorts each day's times ascending
// so commits sharing a day stay in sequence.

// minutePicker yields a clock time, in minutes from midnight, for the nth commit
// of count commits that share a day. rng is the seeded source so output is
// reproducible.
type minutePicker func(rng *rand.Rand, n, count int) int

// assignWithinDays applies a minutePicker to every commit while guaranteeing
// that commits sharing the same calendar day come out in non-decreasing time
// order (the sequence invariant within a day).
func assignWithinDays(commits []model.Commit, days []time.Time, p Params, rng *rand.Rand, pick minutePicker) []time.Time {
	loc := p.Loc()
	times := make([]time.Time, len(commits))

	// Group the positional indexes of commits by their assigned day key.
	groups := make(map[string][]int)
	order := make([]string, 0)
	for i, d := range days {
		k := dayKey(d)
		if _, seen := groups[k]; !seen {
			order = append(order, k)
		}
		groups[k] = append(groups[k], i)
	}

	for _, k := range order {
		idxs := groups[k]
		mins := make([]int, len(idxs))
		for n := range idxs {
			mins[n] = clampMinute(pick(rng, n, len(idxs)))
		}
		// Sort ascending so earlier commits in the chain get earlier times.
		sort.Ints(mins)
		for n, idx := range idxs {
			day := truncateDay(days[idx], loc)
			times[idx] = day.Add(time.Duration(mins[n]) * time.Minute)
		}
	}
	return times
}

// clampMinute keeps a minute-of-day value within [0, 1439].
func clampMinute(m int) int {
	if m < 0 {
		return 0
	}
	if m > 1439 {
		return 1439
	}
	return m
}

// randMinuteInWindow picks a uniform random minute within [start, end],
// optionally skipping a lunch gap.
func randMinuteInWindow(rng *rand.Rand, start, end, lunchStart, lunchEnd int) int {
	if end <= start {
		return start
	}
	m := start + rng.Intn(end-start+1)
	if lunchEnd > lunchStart && m >= lunchStart && m < lunchEnd {
		// Push out of the lunch gap by reflecting into the afternoon window.
		m = lunchEnd + (m - lunchStart)
		if m > end {
			m = end
		}
	}
	return m
}

// --- Models -----------------------------------------------------------------

// PreserveTimeModel keeps each commit's original time-of-day.
type PreserveTimeModel struct{}

func (PreserveTimeModel) Name() string        { return "Preserve original" }
func (PreserveTimeModel) Description() string { return "Keep each commit's original time-of-day" }

func (PreserveTimeModel) AssignTimes(commits []model.Commit, days []time.Time, p Params, rng *rand.Rand) ([]time.Time, error) {
	loc := p.Loc()
	// Preserve needs each commit's own original time, so it does not use the
	// shared index-relative picker; it builds the timestamps directly instead.
	times := make([]time.Time, len(commits))
	for i, c := range commits {
		o := c.OrigAuthorDate.In(loc)
		mins := o.Hour()*60 + o.Minute()
		day := truncateDay(days[i], loc)
		times[i] = day.Add(time.Duration(mins) * time.Minute)
	}
	// Re-sort within day to preserve ordering even if originals were out of order.
	return resortWithinDays(commits, days, times, loc), nil
}

// WorkingHoursModel scatters commits across the configured working-hours window,
// honoring an optional lunch gap.
type WorkingHoursModel struct{}

func (WorkingHoursModel) Name() string { return "Working hours" }
func (WorkingHoursModel) Description() string {
	return "Random within the working-hours window (with optional lunch gap)"
}

func (WorkingHoursModel) AssignTimes(commits []model.Commit, days []time.Time, p Params, rng *rand.Rand) ([]time.Time, error) {
	start, end := workWindow(p)
	pick := func(rng *rand.Rand, _, _ int) int {
		return randMinuteInWindow(rng, start, end, p.LunchStart, p.LunchEnd)
	}
	return assignWithinDays(commits, days, p, rng, pick), nil
}

// The evening window shared by the evening-flavored models: 18:00–23:59.
const (
	eveningStartMin = 18 * 60
	eveningEndMin   = 23*60 + 59
)

// EveningModel scatters commits across the evening (18:00–23:59).
type EveningModel struct{}

func (EveningModel) Name() string        { return "Evening hours" }
func (EveningModel) Description() string { return "Random within the evening window (until 23:59)" }

func (EveningModel) AssignTimes(commits []model.Commit, days []time.Time, p Params, rng *rand.Rand) ([]time.Time, error) {
	pick := func(rng *rand.Rand, _, _ int) int {
		return randMinuteInWindow(rng, eveningStartMin, eveningEndMin, 0, 0)
	}
	return assignWithinDays(commits, days, p, rng, pick), nil
}

// NonWorkingHoursModel places commits outside the working window: evenings on
// working days (per the weekday mask), and anywhere from the start of the
// working window through the evening on days off (e.g. weekends).
type NonWorkingHoursModel struct{}

func (NonWorkingHoursModel) Name() string { return "Non-working hours" }
func (NonWorkingHoursModel) Description() string {
	return "Evenings on workdays; daytime+evening on days off"
}

func (NonWorkingHoursModel) AssignTimes(commits []model.Commit, days []time.Time, p Params, rng *rand.Rand) ([]time.Time, error) {
	loc := p.Loc()
	mask := p.WeekdayMask
	if !mask.Any() {
		mask = AllWeekdays()
	}
	dayStart, _ := workWindow(p)

	times := make([]time.Time, len(commits))
	for i := range commits {
		day := truncateDay(days[i], loc)
		start := eveningStartMin
		if !mask.Allows(day) {
			start = dayStart // a day off: the whole day up to 23:59 is fair game
		}
		mins := clampMinute(randMinuteInWindow(rng, start, eveningEndMin, 0, 0))
		times[i] = day.Add(time.Duration(mins) * time.Minute)
	}
	return resortWithinDays(commits, days, times, loc), nil
}

// UniformModel scatters commits uniformly across the whole day.
type UniformModel struct{}

func (UniformModel) Name() string        { return "Uniform random" }
func (UniformModel) Description() string { return "Anywhere in the day" }

func (UniformModel) AssignTimes(commits []model.Commit, days []time.Time, p Params, rng *rand.Rand) ([]time.Time, error) {
	pick := func(rng *rand.Rand, _, _ int) int {
		return rng.Intn(1440)
	}
	return assignWithinDays(commits, days, p, rng, pick), nil
}

// FixedModel places every commit at the same configured clock time. Commits
// sharing a day are nudged forward by one minute each to keep strict ordering.
type FixedModel struct{}

func (FixedModel) Name() string        { return "Fixed time" }
func (FixedModel) Description() string { return "All commits at a set clock time" }

func (FixedModel) AssignTimes(commits []model.Commit, days []time.Time, p Params, rng *rand.Rand) ([]time.Time, error) {
	pick := func(_ *rand.Rand, n, _ int) int {
		return p.FixedMin + n // +n keeps same-day commits ordered
	}
	return assignWithinDays(commits, days, p, rng, pick), nil
}

// JitterModel centers commits at the working-window midpoint (or FixedMin) and
// scatters them by +/- JitterMin minutes.
type JitterModel struct{}

func (JitterModel) Name() string        { return "Jitter" }
func (JitterModel) Description() string { return "Centered time with +/- jitter" }

func (JitterModel) AssignTimes(commits []model.Commit, days []time.Time, p Params, rng *rand.Rand) ([]time.Time, error) {
	center := p.FixedMin
	if center == 0 {
		start, end := workWindow(p)
		center = (start + end) / 2
	}
	spread := p.JitterMin
	if spread <= 0 {
		spread = 30
	}
	pick := func(rng *rand.Rand, _, _ int) int {
		return center + rng.Intn(2*spread+1) - spread
	}
	return assignWithinDays(commits, days, p, rng, pick), nil
}

// workWindow returns the working-hours window from params, defaulting to
// 09:00–18:00 when unset.
func workWindow(p Params) (start, end int) {
	start, end = p.DayStartMin, p.DayEndMin
	if start == 0 && end == 0 {
		return 9 * 60, 18 * 60
	}
	return start, end
}

// resortWithinDays ensures times are non-decreasing among commits sharing a day,
// preserving the input commit order while reassigning the sorted clock times.
func resortWithinDays(commits []model.Commit, days []time.Time, times []time.Time, loc *time.Location) []time.Time {
	groups := make(map[string][]int)
	for i, d := range days {
		k := dayKey(d)
		groups[k] = append(groups[k], i)
	}
	out := make([]time.Time, len(times))
	copy(out, times)
	for _, idxs := range groups {
		mins := make([]int, len(idxs))
		for n, idx := range idxs {
			t := out[idx].In(loc)
			mins[n] = t.Hour()*60 + t.Minute()
		}
		sort.Ints(mins)
		for n, idx := range idxs {
			day := truncateDay(days[idx], loc)
			out[idx] = day.Add(time.Duration(mins[n]) * time.Minute)
		}
	}
	return out
}

// TimeModels returns all available time-of-day models in display order.
func TimeModels() []TimeModel {
	return []TimeModel{
		PreserveTimeModel{},
		WorkingHoursModel{},
		EveningModel{},
		NonWorkingHoursModel{},
		UniformModel{},
		FixedModel{},
		JitterModel{},
	}
}
