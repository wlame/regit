package ui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/wlame/ghist/internal/config"
	"github.com/wlame/ghist/internal/strategy"
)

// paramsForm is the per-strategy parameter editor shown after a strategy is
// chosen from the palette. It uses a small fixed pool of text inputs whose labels
// and meanings are reassigned per strategy, plus a time-model selector.
type paramsForm struct {
	strat        strategy.Strategy
	labels       []string          // label per active input
	inputs       []textinput.Model // parallel to labels
	timeModels   []strategy.TimeModel
	timeModelIdx int
	focusIdx     int // 0..len(inputs); the final index selects the time model
	usesTimeMod  bool

	// errMsg is the last apply error, rendered inside the modal. The main
	// status bar is hidden while a modal is open, so errors must show here.
	errMsg string
}

const dateLayout = "2006-01-02"

// newParamsForm builds the form for a strategy, seeding fields from config and
// the current calendar cursor (used as a sensible default anchor/day/range).
func newParamsForm(strat strategy.Strategy, cfg config.Config, cursor time.Time) paramsForm {
	specs, usesTimeMod := fieldSpecs(strat, cursor)
	f := paramsForm{
		strat:       strat,
		timeModels:  strategy.TimeModels(),
		usesTimeMod: usesTimeMod,
	}
	// Pre-select the configured default time model.
	for i, tm := range f.timeModels {
		if tm.Name() == cfg.DefaultTimeModel {
			f.timeModelIdx = i
		}
	}
	for _, s := range specs {
		ti := textinput.New()
		ti.SetValue(s.value)
		ti.Prompt = ""
		ti.CharLimit = 32
		ti.Width = 18
		f.labels = append(f.labels, s.label)
		f.inputs = append(f.inputs, ti)
	}
	if len(f.inputs) > 0 {
		f.inputs[0].Focus()
	}
	return f
}

type fieldSpec struct {
	label string
	value string
}

// fieldSpecs returns the inputs a strategy needs and whether it consumes a time
// model. Defaults are seeded from the calendar cursor so the form is usable with
// a single Enter press.
func fieldSpecs(strat strategy.Strategy, cursor time.Time) ([]fieldSpec, bool) {
	start := cursor.Format(dateLayout)
	end := cursor.AddDate(0, 1, 0).Format(dateLayout)

	switch strat.Name() {
	case "Offset Shift":
		return []fieldSpec{{"Days", "30"}, {"Hours", "0"}}, false
	case "Preserve-Relative":
		return []fieldSpec{{"Anchor date", start}, {"Scale (1.0=same)", "1.0"}}, false
	case "Backwards-from-Deadline":
		return []fieldSpec{{"Deadline date", cursor.Format(dateLayout)}}, true
	case "Compress":
		return []fieldSpec{{"Target day", cursor.Format(dateLayout)}}, true
	case "Rate Fill":
		return []fieldSpec{{"Start date", start}, {"End date", end}, {"Min/day", "1"}, {"Max/day", "3"}}, true
	case "Per-Day Quota", "Manual Pin":
		return []fieldSpec{{"Start date", start}, {"End date", end}}, true
	default: // Even Spread, Working Days, Weekends
		return []fieldSpec{{"Start date", start}, {"End date", end}}, true
	}
}

// fieldCount is the number of focusable items (inputs plus, if used, the time
// model selector).
func (f paramsForm) fieldCount() int {
	n := len(f.inputs)
	if f.usesTimeMod {
		n++
	}
	return n
}

// onTimeModel reports whether focus is on the time-model selector.
func (f paramsForm) onTimeModel() bool {
	return f.usesTimeMod && f.focusIdx == len(f.inputs)
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
	if !f.onTimeModel() && f.focusIdx < len(f.inputs) {
		var cmd tea.Cmd
		f.inputs[f.focusIdx], cmd = f.inputs[f.focusIdx].Update(msg)
		return cmd
	}
	return nil
}

func (f *paramsForm) moveFocus(delta int) {
	if f.focusIdx < len(f.inputs) {
		f.inputs[f.focusIdx].Blur()
	}
	n := f.fieldCount()
	f.focusIdx = (f.focusIdx + delta + n) % n
	if f.focusIdx < len(f.inputs) {
		f.inputs[f.focusIdx].Focus()
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
func (f paramsForm) buildParams(cfg config.Config, quotas map[string]int) (strategy.Params, error) {
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
	default:
		if err := setRange(&p, parseDate); err != nil {
			return p, err
		}
	}
	return p, nil
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

// viewPalette renders the strategy chooser list.
func viewPalette(strategies []strategy.Strategy, cursor int, st styles) string {
	var b strings.Builder
	b.WriteString(st.modalTitle.Render("Choose a strategy") + "\n\n")
	for i, s := range strategies {
		line := fmt.Sprintf("%-26s %s", s.Name(), s.Description())
		if i == cursor {
			b.WriteString(st.selected.Render("▸ "+line) + "\n")
		} else {
			b.WriteString("  " + st.dim.Render(line) + "\n")
		}
	}
	b.WriteString("\n" + st.help.Render("↑/↓ select · enter configure · esc cancel"))
	return st.modal.Render(b.String())
}

// view renders the parameter form for the chosen strategy. It returns bare
// content: the caller wraps it in the strategy pane.
func (f paramsForm) view(st styles) string {
	var b strings.Builder
	b.WriteString(st.selected.Render(" "+f.strat.Name()+" ") + "\n")
	b.WriteString(st.dim.Render(truncate(f.strat.Description(), 56)) + "\n\n")

	for i, label := range f.labels {
		cursor := "  "
		if i == f.focusIdx {
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
	}

	if f.errMsg != "" {
		b.WriteString(st.statusErr.Render("✗ "+truncate(f.errMsg, 56)) + "\n")
	}

	b.WriteString(st.help.Render("↑/↓ field · ←/→ model · enter apply · tab back"))
	return b.String()
}
