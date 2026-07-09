// segment.go partitions a redate's target commits into anchor-bounded segments
// and derives the calendar day-windows those segments may occupy. An "anchor"
// is any commit NOT being redated — frozen history or an unselected commit —
// whose date is immovable and therefore constrains its neighbors.
//
// Everything here is pure: no git, no clock (callers pass `now`), no UI. The
// same functions drive the TUI calendar highlight and the headless --commits
// validation, so the two surfaces cannot disagree.
package strategy

import (
	"time"

	"github.com/wlame/regit/internal/model"
)

// Segment is a maximal run of consecutive target commits bounded by anchors.
// Its commits may only be dated inside (Floor, Ceil): the floor is the author
// date of the anchor immediately below (the LAST commit of the preceding
// anchor run), the ceiling the author date of the anchor immediately above
// (the FIRST commit of the following run), capped at now. Day-granularity
// pickers use the inclusive [day(Floor), day(Ceil)] window — time-of-day
// compensates on the shared boundary days.
type Segment struct {
	Commits  []model.Commit // root-first target commits of this run
	First    int            // storage index of Commits[0] in plan.Commits
	Floor    time.Time      // anchor time below; meaningful only when HasFloor
	Ceil     time.Time      // anchor time above, capped at now; now for the HEAD run
	HasFloor bool           // false when the run starts at the repository root
}

// SegmentTargets partitions targets into segments by walking the plan
// root-first. Frozen or unknown "targets" are treated as anchors (defensive:
// callers should never pass them). now caps every ceiling — regit never dates
// a commit in the future.
func SegmentTargets(plan *model.Plan, targets []model.Commit, now time.Time) []Segment {
	want := make(map[string]bool, len(targets))
	for _, c := range targets {
		want[c.Hash] = true
	}
	var segs []Segment
	open := -1 // index into segs of the segment currently being extended
	for i, c := range plan.Commits {
		if want[c.Hash] && !plan.IsFrozen(i) {
			if open == -1 {
				seg := Segment{First: i}
				if i > 0 {
					// The commit right below the run is the binding floor —
					// with a same-day anchor run this is its LAST commit.
					seg.Floor = plan.EffectiveAuthorDate(plan.Commits[i-1])
					seg.HasFloor = true
				}
				segs = append(segs, seg)
				open = len(segs) - 1
			}
			segs[open].Commits = append(segs[open].Commits, c)
			continue
		}
		if open != -1 {
			// First anchor above the run closes it and provides the ceiling.
			ceil := plan.EffectiveAuthorDate(c)
			if ceil.After(now) {
				ceil = now
			}
			segs[open].Ceil = ceil
			open = -1
		}
	}
	if open != -1 {
		segs[open].Ceil = now // the run contains HEAD: today/now is the ceiling
	}
	return segs
}

// Window is a segment's pickable day range at calendar granularity, both ends
// inclusive. StartDay is the zero time when the segment starts at the root
// (unbounded past). EndDay never exceeds today(now).
type Window struct {
	StartDay time.Time
	EndDay   time.Time
	Size     int // number of commits in the segment (its pick/pin capacity)
}

// Windows projects segments to day-granularity windows in loc.
func Windows(segs []Segment, loc *time.Location, now time.Time) []Window {
	today := truncateDay(now, loc)
	out := make([]Window, len(segs))
	for i, s := range segs {
		w := Window{EndDay: truncateDay(s.Ceil, loc), Size: len(s.Commits)}
		if w.EndDay.After(today) {
			w.EndDay = today
		}
		if s.HasFloor {
			w.StartDay = truncateDay(s.Floor, loc)
		}
		out[i] = w
	}
	return out
}

// ContainsDay reports whether the calendar day of `day` lies inside the window.
func (w Window) ContainsDay(day time.Time, loc *time.Location) bool {
	d := truncateDay(day, loc)
	if !w.StartDay.IsZero() && d.Before(w.StartDay) {
		return false
	}
	return !d.After(w.EndDay)
}

// SegmentsForDay returns the indices of every window containing day, ascending.
// At most two windows can match: adjacent segments share exactly the boundary
// day when their bounding anchors sit on one calendar day.
func SegmentsForDay(wins []Window, day time.Time, loc *time.Location) []int {
	var out []int
	for i, w := range wins {
		if w.ContainsDay(day, loc) {
			out = append(out, i)
		}
	}
	return out
}

// AttributePins assigns each pinned/picked day to exactly ONE owning segment,
// replaying the days in `order` (recency order in the TUI; chronological order
// for headless validation, where overflow is an error rather than an eviction).
// weight is the per-day cost against the segment's Size: 1 for a Picked-Days
// pick, the quota for a Manual-Pin pin.
//
// Rule per day (spec "Shared boundary days"): among the windows containing the
// day, prefer one with free capacity; several free → the earliest; none free →
// the earliest candidate, and the day is also reported in overflow so the
// caller can evict (TUI) or refuse (CLI). Days inside no window are dropped.
func AttributePins(order []string, weight map[string]int, wins []Window,
	loc *time.Location) (map[string]int, []string) {
	owner := make(map[string]int, len(order))
	used := make([]int, len(wins))
	var overflow []string
	for _, dayKey := range order {
		d, err := time.ParseInLocation("2006-01-02", dayKey, loc)
		if err != nil {
			continue // malformed keys cannot be attributed; callers validate format
		}
		cands := SegmentsForDay(wins, d, loc)
		if len(cands) == 0 {
			continue
		}
		seg := -1
		for _, si := range cands {
			if used[si]+weight[dayKey] <= wins[si].Size {
				seg = si
				break
			}
		}
		if seg == -1 {
			seg = cands[0]
			overflow = append(overflow, dayKey)
		}
		owner[dayKey] = seg
		used[seg] += weight[dayKey]
	}
	return owner, overflow
}
