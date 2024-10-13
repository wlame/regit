package strategy

import (
	"fmt"
	"math/rand"
	"sort"
	"time"

	"github.com/wlame/regit/internal/model"
)

// This file implements Layer A: day-assignment strategies. Each returns one
// calendar day (midnight in the param location) per commit, in commit order.
//
// A small set of shared helpers handles the recurring chore of enumerating the
// eligible days in a range (respecting the weekday mask) and distributing N
// commits across those days.

// eligibleDays returns every day in [Start, End] (inclusive) whose weekday is
// allowed by the mask, as midnight timestamps in the param location. When the
// mask is empty it falls back to all weekdays so a strategy never returns zero
// days for a non-empty range.
func eligibleDays(p Params) []time.Time {
	loc := p.Loc()
	mask := p.WeekdayMask
	if !mask.Any() {
		mask = AllWeekdays()
	}
	start := truncateDay(p.Start, loc)
	end := truncateDay(p.End, loc)
	var days []time.Time
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		if mask.Allows(d) {
			days = append(days, d)
		}
	}
	return days
}

// distributeEvenly spreads count items across the given days as evenly as
// possible and returns, for each item in order, the index of the day it lands
// on. When there are more items than days it stacks multiples per day; when
// there are fewer it spaces them out so they span the range rather than bunching
// at the start.
func distributeEvenly(count int, days int) []int {
	out := make([]int, count)
	if days <= 0 {
		return out // all land on day 0 (caller guarantees >=1 day in practice)
	}
	if count <= days {
		// Space items out: item i -> day round(i * (days-1) / (count-1)).
		if count == 1 {
			out[0] = 0
			return out
		}
		for i := 0; i < count; i++ {
			out[i] = i * (days - 1) / (count - 1)
		}
		return out
	}
	// More items than days: assign contiguous blocks so each day gets either
	// floor(count/days) or ceil(count/days) items, preserving order.
	base := count / days
	extra := count % days
	idx := 0
	for d := 0; d < days; d++ {
		n := base
		if d < extra {
			n++
		}
		for k := 0; k < n; k++ {
			out[idx] = d
			idx++
		}
	}
	return out
}

// daysFromIndexes maps per-commit day indexes into actual day timestamps.
func daysFromIndexes(idxs []int, days []time.Time) []time.Time {
	out := make([]time.Time, len(idxs))
	for i, di := range idxs {
		if di >= len(days) {
			di = len(days) - 1
		}
		out[i] = days[di]
	}
	return out
}

// --- Even Spread ------------------------------------------------------------

type EvenSpread struct{}

func (EvenSpread) Name() string        { return "Even Spread" }
func (EvenSpread) Description() string { return "Distribute commits evenly across a date range" }

func (EvenSpread) AssignDays(commits []model.Commit, p Params) ([]time.Time, error) {
	days := eligibleDays(p)
	if len(days) == 0 {
		return nil, fmt.Errorf("no eligible days in the selected range")
	}
	idxs := distributeEvenly(len(commits), len(days))
	return daysFromIndexes(idxs, days), nil
}

// --- Manual Pin -------------------------------------------------------------

// ManualPin places commits exactly on the days the user pinned counts onto
// (Params.Quotas), walking the pinned days chronologically and consuming
// commits in order. It is the single quota strategy behind both workflows —
// typing counts on the calendar and configuring through the palette.
//
// Pinned days always count: no date range or weekday mask filters them, since
// a pin IS an explicit instruction. Commits left over once every quota is
// filled stack onto the last pinned day so none are dropped.
type ManualPin struct{}

func (ManualPin) Name() string { return "Manual Pin" }
func (ManualPin) Description() string {
	return "Type counts on calendar days; commits land exactly there"
}

func (ManualPin) AssignDays(commits []model.Commit, p Params) ([]time.Time, error) {
	if len(p.Quotas) == 0 {
		return nil, fmt.Errorf("no per-day quotas set — type a count on calendar days")
	}
	days, err := sortedDayKeys(p.Quotas, p.Loc())
	if err != nil {
		return nil, err
	}

	out := make([]time.Time, len(commits))
	ci := 0
	var lastDay time.Time
	for _, d := range days {
		q := p.Quotas[dayKey(d)]
		if q <= 0 {
			continue
		}
		lastDay = d
		for k := 0; k < q && ci < len(commits); k++ {
			out[ci] = d
			ci++
		}
	}
	if lastDay.IsZero() {
		return nil, fmt.Errorf("all quotas are zero")
	}
	// Stack any remaining commits on the last pinned day.
	for ; ci < len(commits); ci++ {
		out[ci] = lastDay
	}
	return out, nil
}

// --- Picked Days --------------------------------------------------------------

// PickedDays spreads commits across hand-picked calendar days (Params.Picked)
// with random per-day counts, deterministic for a given Params.Seed. Every
// picked day receives at least one commit when there are enough commits to go
// around, and the split is recomputed identically on each toggle so the
// calendar can display stable numbers.
type PickedDays struct{}

func (PickedDays) Name() string { return "Picked Days" }
func (PickedDays) Description() string {
	return "Random split across days picked on the calendar (space)"
}

func (PickedDays) AssignDays(commits []model.Commit, p Params) ([]time.Time, error) {
	chosen := make(map[string]int, len(p.Picked))
	for k, on := range p.Picked {
		if on {
			chosen[k] = 0
		}
	}
	if len(chosen) == 0 {
		return nil, fmt.Errorf("no days picked — press space on calendar days")
	}
	days, err := sortedDayKeys(chosen, p.Loc())
	if err != nil {
		return nil, err
	}

	// Random composition of len(commits) across the picked days: one commit
	// per day guaranteed when possible, the remainder scattered uniformly.
	counts := make([]int, len(days))
	rest := len(commits)
	if rest >= len(days) {
		for i := range counts {
			counts[i] = 1
		}
		rest -= len(days)
	}
	rng := rand.New(rand.NewSource(p.Seed))
	for i := 0; i < rest; i++ {
		counts[rng.Intn(len(days))]++
	}

	out := make([]time.Time, 0, len(commits))
	for i, d := range days {
		for k := 0; k < counts[i] && len(out) < len(commits); k++ {
			out = append(out, d)
		}
	}
	return out, nil
}

// sortedDayKeys parses a map's "2006-01-02" keys into midnight timestamps in
// loc, sorted chronologically.
func sortedDayKeys[V any](m map[string]V, loc *time.Location) ([]time.Time, error) {
	days := make([]time.Time, 0, len(m))
	for k := range m {
		d, err := time.ParseInLocation("2006-01-02", k, loc)
		if err != nil {
			return nil, fmt.Errorf("bad day key %q: %w", k, err)
		}
		days = append(days, truncateDay(d, loc))
	}
	sort.Slice(days, func(i, j int) bool { return days[i].Before(days[j]) })
	return days, nil
}

// --- Rate Fill --------------------------------------------------------------

// RateFill assigns a random number of commits per active day within
// [PerDayMin, PerDayMax], walking the eligible days in order until all commits
// are placed. It is deterministic given Params.Seed.
type RateFill struct{}

func (RateFill) Name() string        { return "Rate Fill" }
func (RateFill) Description() string { return "Fill N–M commits per active day across the range" }

func (RateFill) AssignDays(commits []model.Commit, p Params) ([]time.Time, error) {
	days := eligibleDays(p)
	if len(days) == 0 {
		return nil, fmt.Errorf("no eligible days in the selected range")
	}
	lo, hi := p.PerDayMin, p.PerDayMax
	if lo < 1 {
		lo = 1
	}
	if hi < lo {
		hi = lo
	}
	rng := newSeeded(p.Seed)
	out := make([]time.Time, len(commits))
	ci := 0
	di := 0
	for ci < len(commits) {
		day := days[di%len(days)]
		n := lo + rng.Intn(hi-lo+1)
		for k := 0; k < n && ci < len(commits); k++ {
			out[ci] = day
			ci++
		}
		di++
	}
	return out, nil
}

// --- Working Days / Weekends ------------------------------------------------

// WorkingDays is Even Spread restricted to the configured weekday mask (defaults
// to Mon–Fri when the caller leaves the mask empty).
type WorkingDays struct{}

func (WorkingDays) Name() string        { return "Working Days" }
func (WorkingDays) Description() string { return "Spread across weekdays only (Mon–Fri)" }

func (WorkingDays) AssignDays(commits []model.Commit, p Params) ([]time.Time, error) {
	if !p.WeekdayMask.Any() {
		p.WeekdayMask = WorkdaysMask()
	}
	return EvenSpread{}.AssignDays(commits, p)
}

// Weekends is Even Spread restricted to Saturday and Sunday.
type Weekends struct{}

func (Weekends) Name() string        { return "Weekends Only" }
func (Weekends) Description() string { return "Spread across Saturdays and Sundays only" }

func (Weekends) AssignDays(commits []model.Commit, p Params) ([]time.Time, error) {
	p.WeekdayMask = WeekendsMask()
	return EvenSpread{}.AssignDays(commits, p)
}

// --- Offset Shift -----------------------------------------------------------

// OffsetShift adds a fixed number of days and hours to every commit's original
// timestamp. It preserves the relative spacing and the time-of-day exactly, so
// it implements TimePreserver and bypasses Layer B.
type OffsetShift struct{}

func (OffsetShift) Name() string        { return "Offset Shift" }
func (OffsetShift) Description() string { return "Add N days + M hours to every commit" }

func (OffsetShift) AssignDays(commits []model.Commit, p Params) ([]time.Time, error) {
	loc := p.Loc()
	out := make([]time.Time, len(commits))
	for i, c := range commits {
		shifted := c.OrigAuthorDate.In(loc).
			AddDate(0, 0, p.OffsetDays).
			Add(time.Duration(p.OffsetHours) * time.Hour)
		out[i] = truncateDay(shifted, loc)
	}
	return out, nil
}

func (OffsetShift) PreservedTimes(commits []model.Commit, days []time.Time, p Params) ([]time.Time, bool) {
	loc := p.Loc()
	out := make([]time.Time, len(commits))
	for i, c := range commits {
		out[i] = c.OrigAuthorDate.In(loc).
			AddDate(0, 0, p.OffsetDays).
			Add(time.Duration(p.OffsetHours) * time.Hour)
	}
	return out, true
}

// --- Preserve-Relative ------------------------------------------------------

// PreserveRelative anchors the first commit at Params.Anchor and reproduces the
// original spacing between commits, optionally scaled by Params.Scale (1.0 keeps
// the original gaps, 0.5 halves them, 2.0 doubles them). It owns its timestamps
// fully via TimePreserver.
type PreserveRelative struct{}

func (PreserveRelative) Name() string { return "Preserve-Relative" }
func (PreserveRelative) Description() string {
	return "Keep original spacing but anchor the first commit to a new date"
}

func (PreserveRelative) AssignDays(commits []model.Commit, p Params) ([]time.Time, error) {
	times, _ := PreserveRelative{}.PreservedTimes(commits, nil, p)
	loc := p.Loc()
	out := make([]time.Time, len(times))
	for i, t := range times {
		out[i] = truncateDay(t, loc)
	}
	return out, nil
}

func (PreserveRelative) PreservedTimes(commits []model.Commit, _ []time.Time, p Params) ([]time.Time, bool) {
	loc := p.Loc()
	out := make([]time.Time, len(commits))
	if len(commits) == 0 {
		return out, true
	}
	scale := p.Scale
	if scale <= 0 {
		scale = 1.0
	}
	base := commits[0].OrigAuthorDate
	anchor := p.Anchor.In(loc)
	for i, c := range commits {
		gap := c.OrigAuthorDate.Sub(base) // original distance from first commit
		scaled := time.Duration(float64(gap) * scale)
		out[i] = anchor.Add(scaled)
	}
	return out, true
}

// --- Backwards-from-Deadline ------------------------------------------------

// BackwardsFromDeadline places the newest commit on Params.Deadline and fills
// earlier commits onto preceding eligible days, walking backwards. The result is
// returned in the engine's canonical root-first order.
type BackwardsFromDeadline struct{}

func (BackwardsFromDeadline) Name() string { return "Backwards-from-Deadline" }
func (BackwardsFromDeadline) Description() string {
	return "End on a deadline date and fill earlier days backwards"
}

func (BackwardsFromDeadline) AssignDays(commits []model.Commit, p Params) ([]time.Time, error) {
	loc := p.Loc()
	mask := p.WeekdayMask
	if !mask.Any() {
		mask = AllWeekdays()
	}
	deadline := truncateDay(p.Deadline, loc)
	// Walk backwards from the deadline collecting enough eligible days for one
	// commit each (Even Spread within a day is handled later by the time model).
	need := len(commits)
	var daysDesc []time.Time
	for d := deadline; len(daysDesc) < need; d = d.AddDate(0, 0, -1) {
		if mask.Allows(d) {
			daysDesc = append(daysDesc, d)
		}
	}
	// daysDesc is newest-first; commits are root-first (oldest-first), so the
	// oldest commit maps to the furthest-back day.
	out := make([]time.Time, len(commits))
	for i := range commits {
		out[i] = daysDesc[len(commits)-1-i]
	}
	return out, nil
}

// --- Compress ---------------------------------------------------------------

// Compress packs every commit onto a single target day (Params.CompressDay).
// The time model then scatters them through that day in order.
type Compress struct{}

func (Compress) Name() string        { return "Compress" }
func (Compress) Description() string { return "Pack all commits into a single day" }

func (Compress) AssignDays(commits []model.Commit, p Params) ([]time.Time, error) {
	loc := p.Loc()
	day := truncateDay(p.CompressDay, loc)
	out := make([]time.Time, len(commits))
	for i := range commits {
		out[i] = day
	}
	return out, nil
}

// Strategies returns every day-assignment strategy in palette display order.
func Strategies() []Strategy {
	return []Strategy{
		EvenSpread{},
		ManualPin{},
		PickedDays{},
		RateFill{},
		WorkingDays{},
		Weekends{},
		OffsetShift{},
		PreserveRelative{},
		BackwardsFromDeadline{},
		Compress{},
	}
}
