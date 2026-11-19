package ui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/wlame/regit/internal/config"
	"github.com/wlame/regit/internal/strategy"
)

// timeWindow is the config-derived clock window the time-of-day hint describes.
type timeWindow struct {
	start, end, lunchStart, lunchEnd, fixed, jitter int // minutes from midnight
}

// hhmm formats minutes-from-midnight as HH:MM.
func hhmm(min int) string {
	return fmt.Sprintf("%02d:%02d", min/60, min%60)
}

// timeModelHint returns a concrete one-line description of when a time model places
// commits, keyed by the model's Name(), so the strategy pane can explain each option.
func timeModelHint(name string, w timeWindow) string {
	switch name {
	case "Preserve original":
		return "keeps each commit's original time"
	case "Working hours":
		s := hhmm(w.start) + "–" + hhmm(w.end)
		if w.lunchStart != 0 || w.lunchEnd != 0 {
			s += " (lunch " + hhmm(w.lunchStart) + "–" + hhmm(w.lunchEnd) + ")"
		}
		return s
	case "Evening hours":
		return "18:00–23:59"
	case "Non-working hours":
		return "evenings on workdays; all day on days off"
	case "Uniform random":
		return "00:00–23:59"
	case "Fixed time":
		return "at " + hhmm(w.fixed)
	case "Jitter":
		center := w.fixed
		if center == 0 {
			center = (w.start + w.end) / 2
		}
		return "around " + hhmm(center) + " ± " + fmt.Sprintf("%d", w.jitter) + " min"
	}
	return ""
}

// paramsForm is the per-strategy parameter editor shown after a strategy is
// chosen from the palette. It uses a small fixed pool of text inputs whose labels
// and meanings are reassigned per strategy, plus a time-model selector.
type paramsForm struct {
	strat        strategy.Strategy
	specs        []fieldSpec       // original field specs, used by ready()
	labels       []string          // label per active input
	inputs       []textinput.Model // parallel to labels
	timeModels   []strategy.TimeModel
	timeModelIdx int
	focusIdx     int // 0..len(inputs); the final index selects the time model
	usesTimeMod  bool
	win          timeWindow // config-derived clock window, used for time-model hints

	// errMsg is the last apply error, rendered inside the modal. The main
	// status bar is hidden while a modal is open, so errors must show here.
	errMsg string
}

const dateLayout = "2006-01-02"

// newParamsForm builds the form for a strategy, seeding fields from config and
// the current calendar cursor (used as a sensible default anchor/day/range).
// Date fields start UNSET (empty value, default shown as dim placeholder) so the
// strategy cannot stage until the user explicitly fills them.
func newParamsForm(strat strategy.Strategy, cfg config.Config, cursor time.Time) paramsForm {
	specs, usesTimeMod := fieldSpecs(strat, cursor)
	f := paramsForm{
		strat:       strat,
		specs:       specs, // stored so ready() can check which inputs are date fields
		timeModels:  strategy.TimeModels(),
		usesTimeMod: usesTimeMod,
		// fixed: 0 because cfg.FixedTimeMin() does not exist; Fixed-time hint
		// shows "at 00:00" and Jitter centers on the workday midpoint instead.
		win: timeWindow{
			start:      cfg.WorkdayStartMin(),
			end:        cfg.WorkdayEndMin(),
			lunchStart: cfg.LunchStartMin(),
			lunchEnd:   cfg.LunchEndMin(),
			fixed:      0,
			jitter:     cfg.JitterMinutes,
		},
	}
	// Pre-select the configured default time model.
	for i, tm := range f.timeModels {
		if tm.Name() == cfg.DefaultTimeModel {
			f.timeModelIdx = i
		}
	}
	for _, s := range specs {
		ti := textinput.New()
		// Date fields start empty (unset/greyed); a dim placeholder shows the
		// suggested default so the field is still informative.
		ti.SetValue(s.value)
		ti.Placeholder = s.placeholder
		ti.Prompt = ""
		ti.CharLimit = 32
		ti.Width = 18
		f.labels = append(f.labels, s.label)
		f.inputs = append(f.inputs, ti)
	}
	// The carousel row (row 0) is the initial focus: the user can press right/left
	// to pick a strategy, or down/tab to move to the first input.
	// (Inputs start unfocused; moveFocus will focus them when navigating down.)
	return f
}

// ready reports whether the strategy has enough input to stage: every date
// field parses to a valid date. Pin-based strategies (Manual Pin / Picked Days)
// have no date fields and are judged ready by the caller from the active pin set.
func (f paramsForm) ready() bool {
	for i, sp := range f.specs {
		if sp.isDate {
			if _, err := time.Parse(dateLayout, strings.TrimSpace(f.inputs[i].Value())); err != nil {
				return false
			}
		}
	}
	return true
}

// fieldSpec describes one form input: its label, its initial value (empty for
// date fields so they start unset), an optional dim placeholder (the suggested
// default shown when the field is empty), and whether it is a date field that
// ready() must validate.
type fieldSpec struct {
	label       string
	value       string // empty for date fields (unset by default)
	placeholder string // dim hint shown when value is empty; non-empty for date fields
	isDate      bool   // true => ready() validates this field as YYYY-MM-DD
}

// fieldSpecs returns the inputs a strategy needs and whether it consumes a time
// model. Date fields start UNSET (empty value, placeholder holds the suggestion)
// so the strategy is not ready until the user explicitly fills them. Numeric
// fields keep their seeded values so the form is usable with a single Enter.
func fieldSpecs(strat strategy.Strategy, cursor time.Time) ([]fieldSpec, bool) {
	start := cursor.Format(dateLayout)
	end := cursor.AddDate(0, 1, 0).Format(dateLayout)

	// dateField builds an unset date spec (empty value, placeholder = suggestion).
	dateField := func(label, suggestion string) fieldSpec {
		return fieldSpec{label: label, value: "", placeholder: suggestion, isDate: true}
	}

	switch strat.Name() {
	case "Offset Shift":
		return []fieldSpec{{"Days", "30", "", false}, {"Hours", "0", "", false}}, false
	case "Preserve-Relative":
		// Anchor date is a date field; scale is numeric and seeded.
		return []fieldSpec{dateField("Anchor date", start), {"Scale (1.0=same)", "1.0", "", false}}, false
	case "Backwards-from-Deadline":
		return []fieldSpec{dateField("Deadline date", cursor.Format(dateLayout))}, true
	case "Compress":
		return []fieldSpec{dateField("Target day", cursor.Format(dateLayout))}, true
	case "Rate Fill":
		return []fieldSpec{dateField("Start date", start), dateField("End date", end), {"Min/day", "1", "", false}, {"Max/day", "3", "", false}}, true
	case "Manual Pin":
		// The pinned days themselves define the range; only the time-of-day
		// model is configurable. No date fields.
		return nil, true
	case "Picked Days":
		// The picked days define the range; Spread tunes how uneven the
		// random per-day counts are.
		return []fieldSpec{{spreadLabel, defaultSpreadValue, "", false}}, true
	default: // Even Spread, Working Days, Weekends
		return []fieldSpec{dateField("Start date", start), dateField("End date", end), {spreadLabel, defaultSpreadValue, "", false}}, true
	}
}

// fieldCount is the number of focusable rows: the strategy carousel (row 0),
// the text inputs (rows 1..len(inputs)), and, if used, the time-model selector
// (last row). Row 0 is always present.
func (f paramsForm) fieldCount() int {
	n := 1 + len(f.inputs) // 1 for the strategy carousel row
	if f.usesTimeMod {
		n++
	}
	return n
}

// onStrategy reports whether focus is on the strategy carousel row (row 0).
func (f paramsForm) onStrategy() bool { return f.focusIdx == 0 }

// onTimeModel reports whether focus is on the time-model selector (last row,
// only when this strategy uses one).
func (f paramsForm) onTimeModel() bool {
	return f.usesTimeMod && f.focusIdx == len(f.inputs)+1
}

// update handles key input for the form (navigation, editing, time-model cycle).
func (f *paramsForm) update(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch key.String() {
	case "tab", "down":
		f.moveFocus(1)
		return nil
	case "shift+tab", "up":
		f.moveFocus(-1)
		return nil
	case "left":
		if f.onTimeModel() {
			f.cycleTimeModel(-1)
			return nil
		}
	case "right":
		if f.onTimeModel() {
			f.cycleTimeModel(1)
			return nil
		}
	}
	// Forward key to the focused text input when on an input row (rows 1..len(inputs)).
	if inputIdx := f.focusIdx - 1; !f.onStrategy() && !f.onTimeModel() && inputIdx >= 0 && inputIdx < len(f.inputs) {
		var cmd tea.Cmd
		f.inputs[inputIdx], cmd = f.inputs[inputIdx].Update(msg)
		return cmd
	}
	return nil
}

func (f *paramsForm) moveFocus(delta int) {
	// Blur the current input if one is focused. Inputs sit at focusIdx 1..len(inputs).
	if inputIdx := f.focusIdx - 1; inputIdx >= 0 && inputIdx < len(f.inputs) {
		f.inputs[inputIdx].Blur()
	}
	n := f.fieldCount()
	f.focusIdx = (f.focusIdx + delta + n) % n
	// Focus the new input if the new position maps to one.
	if inputIdx := f.focusIdx - 1; inputIdx >= 0 && inputIdx < len(f.inputs) {
		f.inputs[inputIdx].Focus()
	}
}

func (f *paramsForm) cycleTimeModel(delta int) {
	n := len(f.timeModels)
	f.timeModelIdx = (f.timeModelIdx + delta + n) % n
}

func (f paramsForm) timeModel() strategy.TimeModel {
	return f.timeModels[f.timeModelIdx]
}

// buildParams assembles strategy.Params from the form values plus config-derived
// defaults (working window, weekday mask, timezone, seed). It returns an error
// with a human-readable message when a field cannot be parsed.
func (f paramsForm) buildParams(cfg config.Config, quotas map[string]int, picked map[string]bool) (strategy.Params, error) {
	loc := cfg.Location()
	p := strategy.Params{
		Location:    loc,
		WeekdayMask: strategy.WeekdayMask(cfg.WeekdayMask()),
		DayStartMin: cfg.WorkdayStartMin(),
		DayEndMin:   cfg.WorkdayEndMin(),
		LunchStart:  cfg.LunchStartMin(),
		LunchEnd:    cfg.LunchEndMin(),
		JitterMin:   cfg.JitterMinutes,
		Seed:        cfg.Seed,
		Quotas:      quotas,
		Picked:      picked,
	}

	val := func(label string) string {
		for i, l := range f.labels {
			if l == label {
				return strings.TrimSpace(f.inputs[i].Value())
			}
		}
		return ""
	}
	parseDate := func(label string) (time.Time, error) {
		return time.ParseInLocation(dateLayout, val(label), loc)
	}

	switch f.strat.Name() {
	case "Offset Shift":
		d, err := strconv.Atoi(val("Days"))
		if err != nil {
			return p, fmt.Errorf("days must be a number")
		}
		h, _ := strconv.Atoi(val("Hours"))
		p.OffsetDays, p.OffsetHours = d, h
	case "Preserve-Relative":
		a, err := parseDate("Anchor date")
		if err != nil {
			return p, fmt.Errorf("anchor date must be YYYY-MM-DD")
		}
		p.Anchor = a
		if s, err := strconv.ParseFloat(val("Scale (1.0=same)"), 64); err == nil {
			p.Scale = s
		}
	case "Backwards-from-Deadline":
		d, err := parseDate("Deadline date")
		if err != nil {
			return p, fmt.Errorf("deadline must be YYYY-MM-DD")
		}
		p.Deadline = d
	case "Compress":
		d, err := parseDate("Target day")
		if err != nil {
			return p, fmt.Errorf("target day must be YYYY-MM-DD")
		}
		p.CompressDay = d
	case "Rate Fill":
		if err := setRange(&p, parseDate); err != nil {
			return p, err
		}
		p.PerDayMin, _ = strconv.Atoi(val("Min/day"))
		p.PerDayMax, _ = strconv.Atoi(val("Max/day"))
	case "Picked Days":
		spread, err := parseSpread(val(spreadLabel))
		if err != nil {
			return p, err
		}
		p.Spread = spread
	case "Manual Pin":
		// No date fields: the pinned days themselves define the range.
	default: // Even Spread, Working Days, Weekends
		if err := setRange(&p, parseDate); err != nil {
			return p, err
		}
		spread, err := parseSpread(val(spreadLabel))
		if err != nil {
			return p, err
		}
		p.Spread = spread
	}
	return p, nil
}

// spreadLabel names the unevenness field of Picked Days and the date-range
// strategies (Even Spread, Working Days, Weekends).
const spreadLabel = "Spread (0-100)"

// defaultSpreadValue is the Spread field's starting value, the same default
// the CLI's --spread uses.
var defaultSpreadValue = strconv.Itoa(strategy.DefaultSpread)

// parseSpread reads the Spread field: empty means 0 (about even).
func parseSpread(value string) (int, error) {
	if value == "" {
		return 0, nil
	}
	spread, err := strconv.Atoi(value)
	if err != nil || spread < 0 || spread > strategy.MaxSpread {
		return 0, fmt.Errorf("spread must be a whole number from 0 to %d", strategy.MaxSpread)
	}
	return spread, nil
}

// setRange fills Start/End from "Start date"/"End date" fields.
func setRange(p *strategy.Params, parseDate func(string) (time.Time, error)) error {
	s, err := parseDate("Start date")
	if err != nil {
		return fmt.Errorf("start date must be YYYY-MM-DD")
	}
	e, err := parseDate("End date")
	if err != nil {
		return fmt.Errorf("end date must be YYYY-MM-DD")
	}
	if e.Before(s) {
		return fmt.Errorf("end date is before start date")
	}
	p.Start, p.End = s, e
	return nil
}

// view renders the parameter form for the chosen strategy. It returns bare
// content: the caller wraps it in the strategy pane. The first focus row (row 0)
// is the strategy carousel (◀ Name ▶); input rows follow at indices 1..len(inputs).
func (f paramsForm) view(st styles) string {
	var b strings.Builder

	// Row 0: strategy carousel.
	if f.onStrategy() {
		b.WriteString(st.listCursor.Render("▸ ") + st.selected.Render(" ◀ "+f.strat.Name()+" ▶ ") + "\n")
	} else {
		b.WriteString("  " + st.dim.Render("◀ "+f.strat.Name()+" ▶") + "\n")
	}
	b.WriteString(st.dim.Render(truncate(f.strat.Description(), 56)) + "\n\n")

	// Input rows (shifted: input i is at focusIdx i+1).
	for i, label := range f.labels {
		cursor := "  "
		if f.focusIdx == i+1 {
			cursor = st.listCursor.Render("▸ ")
		}
		b.WriteString(fmt.Sprintf("%s%-18s %s\n", cursor, label+":", f.inputs[i].View()))
	}
	if f.usesTimeMod {
		cursor := "  "
		if f.onTimeModel() {
			cursor = st.listCursor.Render("▸ ")
		}
		tm := f.timeModel()
		b.WriteString(fmt.Sprintf("%s%-18s ◀ %s ▶\n", cursor, "Time of day:", tm.Name()))
		b.WriteString(st.dim.Render(fmt.Sprintf("%-20s %s", "", timeModelHint(tm.Name(), f.win))) + "\n")
	}

	if f.errMsg != "" {
		b.WriteString(st.statusErr.Render("✗ "+truncate(f.errMsg, 56)) + "\n")
	}

	b.WriteString(st.help.Render("[ ] strategy · < > time · r reshuffle · ↑/↓ field · enter apply · tab back"))
	return b.String()
}
