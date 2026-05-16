package capability

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/wlame/regit/internal/model"
	"github.com/wlame/regit/internal/strategy"
)

func runRedate(ctx *RunContext, a ParsedArgs) error {
	strat := strategyByName(a.String("strategy"))
	if strat == nil {
		return usageErr("unknown --strategy=%s", a.String("strategy"))
	}
	tmName := a.String("time")
	if tmName == "" {
		tmName = ctx.Cfg.DefaultTimeModel
	}
	tm := timeModelByName(tmName)
	if tm == nil {
		return usageErr("unknown --time=%s", tmName)
	}
	// The Fixed-time model needs a time of day. When it is selected via config
	// (default_time_model) rather than the --time flag, Validate never runs the
	// AppliesIf gate that would demand --time-fixed, so FixedMin would silently stay
	// 0 and date every commit at midnight. Fail fast with an actionable message
	// instead; the flags must be given together so Validate accepts --time-fixed.
	if _, isFixed := tm.(strategy.FixedModel); isFixed && !a.Has("time-fixed") {
		return usageErr("the Fixed-time model needs a time of day; pass --time=Fixed-time --time-fixed=HH:MM")
	}

	commits, err := ctx.Repo.ReadCommits()
	if err != nil {
		return runtimeErr("read commits: %v", err)
	}
	plan := model.NewPlan(commits)
	if from := a.String("from"); from != "" {
		idx, err := FindCommit(ctx.Repo, commits, from)
		if err != nil {
			return usageErr("%v", err)
		}
		plan.SetBase(idx)
	}

	params, err := redateParams(ctx, a)
	if err != nil {
		return usageErr("%v", err)
	}
	linkDates := ctx.Cfg.LinkDates
	if a.Has("link-dates") {
		linkDates = a.Bool("link-dates")
	}
	if err := strategy.Apply(plan, strat, tm, params, linkDates); err != nil {
		return usageErr("%v", err)
	}

	// Replay only remaps a rewritten commit's first parent (see
	// gitio.Repo.Replay); a merge's other parent(s) — the side branch(es) it
	// brought in — keep their original dates untouched. That silent partial
	// effect is surprising enough to gate behind an explicit opt-in rather than
	// just redating the merge commit itself and calling it done.
	if a.Bool("execute") && !a.Bool("allow-merges") {
		merges := 0
		for _, c := range plan.Active() {
			if c.IsMerge {
				merges++
			}
		}
		if merges > 0 {
			return guardErr("range contains %d merge commit(s); their side branches keep their original dates — pass --allow-merges to proceed", merges)
		}
	}
	return applyOrPreview(ctx, plan, a, previewDates(ctx.Cfg.Location()))
}

// redateParams builds strategy.Params from flags, falling back to config.
func redateParams(ctx *RunContext, a ParsedArgs) (strategy.Params, error) {
	loc := ctx.Cfg.Location()
	p := strategy.Params{Location: loc, Seed: ctx.Cfg.Seed}

	for name, dst := range map[string]*time.Time{
		"start": &p.Start, "end": &p.End, "deadline": &p.Deadline,
		"compress-day": &p.CompressDay, "anchor": &p.Anchor,
	} {
		if d, ok := a.Date(name); ok {
			*dst = d.In(loc)
		}
	}

	if wd := a.List("weekdays"); len(wd) > 0 {
		p.WeekdayMask = weekdayMask(wd)
	} else {
		// No flag: fall back to the configured weekday set — the same default
		// the TUI seeds its strategies with (see ui/palette.go) — so a headless
		// redate and a TUI redate distribute over the same days. A config mask
		// with no valid day names degrades to all days rather than none.
		p.WeekdayMask = strategy.WeekdayMask(ctx.Cfg.WeekdayMask())
		if !p.WeekdayMask.Any() {
			p.WeekdayMask = strategy.AllWeekdays()
		}
	}

	p.Quotas = a.KeyVals("pin")
	if picks := a.List("pick"); len(picks) > 0 {
		p.Picked = map[string]bool{}
		for _, d := range picks {
			p.Picked[d] = true
		}
	}
	p.PerDayMin, p.PerDayMax = a.Int("per-day-min"), a.Int("per-day-max")
	p.OffsetDays, p.OffsetHours = a.Int("offset-days"), a.Int("offset-hours")
	p.Scale = a.Float("scale")
	// A zero or negative scale would collapse or invert the whole span; refuse
	// it up front instead of producing an all-violations plan.
	if a.Has("scale") && p.Scale <= 0 {
		return p, fmt.Errorf("scale must be a positive number, got %g", p.Scale)
	}

	startMin, err := clockToMin(firstNonEmpty(a.String("workday-start"), ctx.Cfg.WorkdayStart))
	if err != nil {
		return p, fmt.Errorf("workday-start: %w", err)
	}
	endMin, err := clockToMin(firstNonEmpty(a.String("workday-end"), ctx.Cfg.WorkdayEnd))
	if err != nil {
		return p, fmt.Errorf("workday-end: %w", err)
	}
	// config.Validate covers the config file, but the flags can re-invert the
	// window per invocation; an inverted window dated everything at the start.
	if endMin <= startMin {
		return p, fmt.Errorf("workday-end (%02d:%02d) must be after workday-start (%02d:%02d)",
			endMin/60, endMin%60, startMin/60, startMin%60)
	}
	p.DayStartMin, p.DayEndMin = startMin, endMin

	if tf := a.String("time-fixed"); tf != "" {
		if p.FixedMin, err = clockToMin(tf); err != nil {
			return p, fmt.Errorf("time-fixed: %w", err)
		}
	}
	p.JitterMin = ctx.Cfg.JitterMinutes
	if a.Has("jitter-min") { // an explicit 0 disables jitter; only omission keeps the config default
		p.JitterMin = a.Int("jitter-min")
	}
	if ls := firstNonEmpty(a.String("lunch-start"), ctx.Cfg.LunchStart); ls != "" {
		p.LunchStart, _ = clockToMin(ls)
	}
	if le := firstNonEmpty(a.String("lunch-end"), ctx.Cfg.LunchEnd); le != "" {
		p.LunchEnd, _ = clockToMin(le)
	}
	if s := a.Int("seed"); s != 0 {
		p.Seed = int64(s)
	}
	if p.Seed == 0 {
		p.Seed = ctx.Now.UnixNano()
	}
	return p, nil
}

func previewDates(loc *time.Location) func(io.Writer, *model.Plan) {
	return func(w io.Writer, plan *model.Plan) {
		active := plan.Active()
		fmt.Fprintf(w, "Date plan (%d commit(s)):\n", len(active))
		for _, c := range active {
			e, _ := plan.EditFor(c.Hash)
			nd := c.OrigAuthorDate
			if e.NewAuthorDate != nil {
				nd = *e.NewAuthorDate
			}
			fmt.Fprintf(w, "  %s  %s -> %s\n", Short(c.Hash),
				c.OrigAuthorDate.In(loc).Format("2006-01-02 15:04"),
				nd.In(loc).Format("2006-01-02 15:04"))
		}
		if len(plan.Violations) > 0 {
			fmt.Fprintf(w, "  ⚠ %d ordering violation(s)\n", len(plan.Violations))
		} else {
			fmt.Fprintln(w, "  ✓ sequential")
		}
		// Warn about merges in the dry-run preview too, so the caveat is visible
		// well before --execute would refuse to apply it (see the merge gate in
		// runRedate).
		merges := 0
		for _, c := range active {
			if c.IsMerge {
				merges++
			}
		}
		if merges > 0 {
			fmt.Fprintf(w, "  ⚠ %d merge commit(s) — side-branch dates unchanged (apply needs --allow-merges)\n", merges)
		}
	}
}

func weekdayMask(names []string) strategy.WeekdayMask {
	short := map[string]time.Weekday{
		"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday,
		"thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday,
	}
	var m strategy.WeekdayMask
	for _, n := range names {
		if wd, ok := short[strings.ToLower(strings.TrimSpace(n))]; ok {
			m[int(wd)] = true
		}
	}
	if !m.Any() {
		return strategy.AllWeekdays()
	}
	return m
}

// clockToMin parses "HH:MM" to minutes from midnight; "" → 0.
func clockToMin(s string) (int, error) {
	if s == "" {
		return 0, nil
	}
	h, m, ok := strings.Cut(s, ":")
	if !ok {
		return 0, fmt.Errorf("expected HH:MM, got %q", s)
	}
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, fmt.Errorf("invalid clock %q", s)
	}
	return hh*60 + mm, nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
