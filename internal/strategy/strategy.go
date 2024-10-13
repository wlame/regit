// Package strategy is the pure, side-effect-free heart of regit. Given an
// ordered list of commits and a set of parameters, it decides what new dates
// each commit should have. It knows nothing about git, the terminal, or the
// filesystem, which makes it instant to preview and trivial to unit-test.
//
// The engine works in two composable layers:
//
//	Layer A — a Strategy assigns each commit to a calendar DAY.
//	Layer B — a TimeModel assigns the clock TIME within that day.
//
// A handful of strategies (Offset Shift, Preserve-Relative) determine the full
// timestamp themselves and opt out of Layer B by implementing TimePreserver.
//
// After both layers run, EnforceSequential guarantees the hard invariant that a
// child commit is never dated before its parent.
package strategy

import (
	"math/rand"
	"time"

	"github.com/wlame/regit/internal/model"
)

// WeekdayMask selects which weekdays are eligible for commits. Index it with a
// time.Weekday value (Sunday == 0 ... Saturday == 6).
type WeekdayMask [7]bool

// Allows reports whether the given day's weekday is enabled in the mask.
func (m WeekdayMask) Allows(t time.Time) bool {
	return m[int(t.Weekday())]
}

// Any reports whether at least one weekday is enabled. A mask with nothing
// enabled would make day-based strategies unable to place any commit.
func (m WeekdayMask) Any() bool {
	for _, ok := range m {
		if ok {
			return true
		}
	}
	return false
}

// AllWeekdays enables every day of the week.
func AllWeekdays() WeekdayMask {
	return WeekdayMask{true, true, true, true, true, true, true}
}

// WorkdaysMask enables Monday through Friday.
func WorkdaysMask() WeekdayMask {
	var m WeekdayMask
	m[int(time.Monday)] = true
	m[int(time.Tuesday)] = true
	m[int(time.Wednesday)] = true
	m[int(time.Thursday)] = true
	m[int(time.Friday)] = true
	return m
}

// WeekendsMask enables Saturday and Sunday only.
func WeekendsMask() WeekdayMask {
	var m WeekdayMask
	m[int(time.Saturday)] = true
	m[int(time.Sunday)] = true
	return m
}

// Params is the union of every input a strategy or time model might read. Each
// implementation only consults the fields relevant to it; the rest are ignored.
// Keeping a single struct (rather than one per strategy) keeps the orchestration
// and the UI form-binding simple.
type Params struct {
	// Date range, day granularity. Strategies treat Start and End as inclusive
	// calendar days in Location.
	Start    time.Time
	End      time.Time
	Location *time.Location

	// WeekdayMask restricts which days are eligible (used by day-based strategies).
	WeekdayMask WeekdayMask

	// PerDayMin/PerDayMax bound the commits-per-active-day for Rate Fill.
	PerDayMin int
	PerDayMax int

	// Quotas maps a "2006-01-02" day key to a target commit count, used by
	// Manual Pin.
	Quotas map[string]int

	// Picked marks "2006-01-02" day keys chosen on the calendar for the
	// Picked Days strategy (only true entries count).
	Picked map[string]bool

	// Offset Shift parameters.
	OffsetDays  int
	OffsetHours int

	// Preserve-Relative parameters: Anchor is the new instant for the first
	// commit; Scale stretches/compresses the original spacing (1.0 = unchanged).
	Anchor time.Time
	Scale  float64

	// Deadline is the last day for Backwards-from-Deadline.
	Deadline time.Time

	// CompressDay is the single target day for Compress.
	CompressDay time.Time

	// Time-of-day window (minutes from midnight) for the WorkingHours/Evening
	// /Uniform/Fixed/Jitter models, plus jitter spread and lunch gap.
	DayStartMin int // e.g. 9*60
	DayEndMin   int // e.g. 18*60
	FixedMin    int // for Fixed time model
	JitterMin   int // +/- minutes for Jitter model
	LunchStart  int // optional lunch gap start (minutes); 0 disables
	LunchEnd    int

	// Seed makes randomized models reproducible. Equal seeds -> equal output.
	Seed int64
}

// Location returns p.Location or time.Local when unset, so callers never have to
// nil-check it.
func (p Params) Loc() *time.Location {
	if p.Location != nil {
		return p.Location
	}
	return time.Local
}

// Strategy is Layer A: it assigns every commit to a calendar day. The returned
// slice has one entry per input commit, in the same order, each truncated to
// midnight in the parameter location.
type Strategy interface {
	Name() string
	Description() string
	AssignDays(commits []model.Commit, p Params) ([]time.Time, error)
}

// TimePreserver is implemented by strategies that fully determine the clock time
// themselves (Offset Shift, Preserve-Relative). When PreservedTimes returns
// ok == true, the orchestrator skips the TimeModel and uses these timestamps.
type TimePreserver interface {
	PreservedTimes(commits []model.Commit, days []time.Time, p Params) (times []time.Time, ok bool)
}

// TimeModel is Layer B: given the day each commit was assigned, it picks the
// clock time within that day. Implementations must keep commits that share a day
// in non-decreasing time order to preserve sequence.
type TimeModel interface {
	Name() string
	Description() string
	AssignTimes(commits []model.Commit, days []time.Time, p Params, rng *rand.Rand) ([]time.Time, error)
}

// Apply runs the full pipeline for the given plan: Layer A, then Layer B (unless
// the strategy preserves its own times), then EnforceSequential, then it records
// the resulting edits on the plan (linking committer date to author date unless
// linkCommitter is false).
//
// It mutates plan.Edits and plan.Violations and returns an error only when a
// layer cannot produce a valid assignment (e.g. an empty weekday mask).
func Apply(plan *model.Plan, s Strategy, tm TimeModel, p Params, linkCommitter bool) error {
	// Only the active suffix is rewritten; commits before the plan's base
	// boundary are frozen and must keep their dates (and hashes).
	commits := plan.Active()
	if len(commits) == 0 {
		return nil
	}

	days, err := s.AssignDays(commits, p)
	if err != nil {
		return err
	}

	var times []time.Time
	if preserver, ok := s.(TimePreserver); ok {
		if preserved, done := preserver.PreservedTimes(commits, days, p); done {
			times = preserved
		}
	}
	if times == nil {
		rng := rand.New(rand.NewSource(p.Seed))
		times, err = tm.AssignTimes(commits, days, p, rng)
		if err != nil {
			return err
		}
	}

	// The first active commit must still land after its frozen parent, whose
	// date can no longer move. Clamping before EnforceSequential lets the
	// usual push-forward cascade absorb the correction.
	if plan.Base > 0 {
		floor := plan.Commits[plan.Base-1].OrigAuthorDate
		if times[0].Before(floor.Add(minGap)) {
			times[0] = floor.Add(minGap)
		}
	}

	times = EnforceSequential(commits, times)
	plan.Violations = FindViolations(commits, times)

	for i, c := range commits {
		t := times[i]
		e, _ := plan.EditFor(c.Hash)
		newAuthor := t
		e.NewAuthorDate = &newAuthor
		if linkCommitter {
			newCommit := t
			e.NewCommitDate = &newCommit
		}
		plan.SetEdit(c.Hash, e)
	}
	return nil
}

// newSeeded returns a reproducible RNG from a seed, used by strategies that need
// randomness (e.g. Rate Fill) so previews and tests are deterministic.
func newSeeded(seed int64) *rand.Rand {
	return rand.New(rand.NewSource(seed))
}

// truncateDay returns t reduced to midnight in loc.
func truncateDay(t time.Time, loc *time.Location) time.Time {
	t = t.In(loc)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
}

// dayKey formats a day as the canonical "2006-01-02" map key.
func dayKey(t time.Time) string {
	return t.Format("2006-01-02")
}
