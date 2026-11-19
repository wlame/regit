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
	"fmt"
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

	// PinOwner maps a "2006-01-02" pin/pick day to the index of the segment
	// (per SegmentTargets order) that owns it, as computed by AttributePins.
	// A day on a shared segment boundary is usable by two segments; ownership
	// decides which one actually consumes it. nil → ownership falls back to
	// plain window containment (a shared day then serves both segments).
	PinOwner map[string]int

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

	// Spread (0–100) makes Picked Days' per-day counts uneven: 0 scatters the
	// commits uniformly (about the same count per day), higher values give
	// the days random weights so some get many more commits than others.
	Spread int

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

// Apply runs the full pipeline for the given targets: partition into
// anchor-bounded segments, then per segment run Layer A + Layer B with the
// params intersected to the segment's day window, clamp against the anchor
// times (anchors always win over strategy/time-model placement), and finally
// record edits for the targets. Anchors — frozen commits and non-target active
// commits — are never edited.
//
// Enforcement (EnforceSequential) runs per segment so the push-forward cascade
// can never move an anchor; detection (FindViolations) runs over the entire
// stitched chain, but the result is then filtered down to violations that
// touch at least one target commit (violationsTouchingTargets). An infeasible
// squeeze always involves a target, so squeeze detection still blocks apply
// through the ordinary violations channel; a pre-existing inversion purely
// between anchors/frozen commits — history this redate never touches — no
// longer does. Rebased or cherry-picked repos routinely carry such
// inversions, and anchors are never edited (their dates are never wrong from
// this redate's point of view), so policing them here would block every
// apply with no in-command remedy.
//
// The returned warnings name segments left unchanged (no overlap with the
// strategy's days, or a per-segment strategy error). A non-nil error means the
// whole staging is invalid (e.g. a broken time model) and nothing useful was
// staged.
func Apply(plan *model.Plan, targets []model.Commit, s Strategy, tm TimeModel, p Params,
	linkCommitter bool, now time.Time) ([]string, error) {
	segs := SegmentTargets(plan, targets, now)
	if len(segs) == 0 {
		return nil, nil
	}
	loc := p.Loc()
	var warnings []string
	assigned := make(map[string]time.Time)

	for si, seg := range segs {
		sp, ok := intersectParams(p, seg, si, loc, now)
		if !ok {
			warnings = append(warnings, fmt.Sprintf(
				"segment %d (%d commit(s)): no overlap with the strategy's days — left unchanged",
				si+1, len(seg.Commits)))
			continue
		}
		days, err := s.AssignDays(seg.Commits, sp)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf(
				"segment %d (%d commit(s)): %v — left unchanged", si+1, len(seg.Commits), err))
			continue
		}
		var times []time.Time
		if preserver, ok := s.(TimePreserver); ok {
			if preserved, done := preserver.PreservedTimes(seg.Commits, days, sp); done {
				times = preserved
			}
		}
		if times == nil {
			// Offset the seed per segment so two segments never draw the same
			// random stream; still fully deterministic for a given Params.Seed.
			rng := rand.New(rand.NewSource(sp.Seed + int64(si)))
			times, err = tm.AssignTimes(seg.Commits, days, sp, rng)
			if err != nil {
				return warnings, err
			}
		}
		times = clampSegment(seg.Commits, times, seg)
		for i, c := range seg.Commits {
			assigned[c.Hash] = times[i]
		}
	}

	// Global detection over the stitched chain: anchors contribute their fixed
	// dates, targets their proposed ones. Frozen history is included so the
	// oldest segment is still checked against its frozen parent.
	full := make([]time.Time, len(plan.Commits))
	for i, c := range plan.Commits {
		if t, ok := assigned[c.Hash]; ok {
			full[i] = t
		} else {
			full[i] = plan.EffectiveAuthorDate(c)
		}
	}
	moved := make(map[string]bool, len(assigned))
	for h := range assigned {
		moved[h] = true
	}
	plan.Violations = ViolationsInvolving(FindViolations(plan.Commits, full), moved)
	plan.Shifted = nil
	for _, h := range ShiftedByEnforcement(plan.Commits, full) {
		if _, isTarget := assigned[h]; isTarget { // anchors can never actually shift
			plan.Shifted = append(plan.Shifted, h)
		}
	}

	for hash, t := range assigned {
		e, _ := plan.EditFor(hash)
		newAuthor := t
		e.NewAuthorDate = &newAuthor
		if linkCommitter {
			newCommit := t
			e.NewCommitDate = &newCommit
		}
		plan.SetEdit(hash, e)
	}
	return warnings, nil
}

// ViolationsInvolving keeps only the violations where the child or the parent
// is one of the moved commits. A pair of commits that were already out of date
// order before any edit (common after a rebase or cherry-pick, which keep each
// commit's original author date), and that no edit moves, is not something
// the user caused or can be asked to resolve, so it must not block an apply.
func ViolationsInvolving(violations []model.SequenceViolation,
	moved map[string]bool) []model.SequenceViolation {
	var out []model.SequenceViolation
	for _, v := range violations {
		if moved[v.ChildHash] || moved[v.ParentHash] {
			out = append(out, v)
		}
	}
	return out
}

// intersectParams narrows p's day inputs to the segment's window. ok=false
// means the strategy cannot place anything in this segment (no overlap).
func intersectParams(p Params, seg Segment, segIdx int, loc *time.Location,
	now time.Time) (Params, bool) {
	var winStart time.Time
	if seg.HasFloor {
		winStart = truncateDay(seg.Floor, loc)
	}
	winEnd := truncateDay(seg.Ceil, loc)
	if today := truncateDay(now, loc); winEnd.After(today) {
		winEnd = today
	}
	inWindow := func(d time.Time) bool {
		return (winStart.IsZero() || !d.Before(winStart)) && !d.After(winEnd)
	}

	q := p
	if !q.Start.IsZero() || !q.End.IsZero() {
		start, end := truncateDay(q.Start, loc), truncateDay(q.End, loc)
		if !winStart.IsZero() && start.Before(winStart) {
			start = winStart
		}
		if end.After(winEnd) {
			end = winEnd
		}
		if start.After(end) {
			return p, false
		}
		q.Start, q.End = start, end
	}
	if len(q.Quotas) > 0 {
		q.Quotas = ownedDays(q.Quotas, p.PinOwner, segIdx, inWindow, loc)
		if len(q.Quotas) == 0 {
			return p, false
		}
	}
	if len(q.Picked) > 0 {
		filtered := ownedDays(boolToInt(q.Picked), p.PinOwner, segIdx, inWindow, loc)
		if len(filtered) == 0 {
			return p, false
		}
		q.Picked = intToBool(filtered)
	}
	if !q.Deadline.IsZero() {
		d := truncateDay(q.Deadline, loc)
		if d.After(winEnd) {
			d = winEnd
		}
		if !winStart.IsZero() && d.Before(winStart) {
			return p, false
		}
		q.Deadline = d
	}
	if !q.CompressDay.IsZero() && !inWindow(truncateDay(q.CompressDay, loc)) {
		return p, false
	}
	return q, true
}

// ownedDays keeps the day-keyed entries this segment may use: inside its
// window AND (when ownership is known) owned by it.
func ownedDays(days map[string]int, owner map[string]int, segIdx int,
	inWindow func(time.Time) bool, loc *time.Location) map[string]int {
	out := make(map[string]int, len(days))
	for k, v := range days {
		d, err := time.ParseInLocation("2006-01-02", k, loc)
		if err != nil || !inWindow(truncateDay(d, loc)) {
			continue
		}
		if owner != nil {
			if o, ok := owner[k]; !ok || o != segIdx {
				continue
			}
		}
		out[k] = v
	}
	return out
}

func boolToInt(m map[string]bool) map[string]int {
	out := make(map[string]int, len(m))
	for k, on := range m {
		if on {
			out[k] = 1
		}
	}
	return out
}

func intToBool(m map[string]int) map[string]bool {
	out := make(map[string]bool, len(m))
	for k := range m {
		out[k] = true
	}
	return out
}

// clampSegment forces a segment's proposed times inside (Floor, Ceil). Anchor
// times override whatever the strategy/time model chose (the spec's "time can
// compensate" rule for shared boundary days). Order is first re-established by
// EnforceSequential; then the head is raised above the floor and the tail
// lowered below the ceiling, each with a minGap cascade. When the two passes
// collide the segment cannot fit — the out-of-order result is left for
// FindViolations to flag rather than silently moving an anchor.
func clampSegment(commits []model.Commit, times []time.Time, seg Segment) []time.Time {
	out := EnforceSequential(commits, times)
	if seg.HasFloor {
		floor := seg.Floor.Add(minGap)
		for i := 0; i < len(out) && out[i].Before(floor); i++ {
			out[i] = floor
			floor = floor.Add(minGap)
		}
	}
	ceil := seg.Ceil.Add(-minGap)
	for i := len(out) - 1; i >= 0 && out[i].After(ceil); i-- {
		out[i] = ceil
		ceil = ceil.Add(-minGap)
	}
	return out
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
