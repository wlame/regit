package capability

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/wlame/regit/internal/strategy"
)

// RedateArgMap projects a concrete strategy run (the values the TUI already
// computed) into the flag map that Render turns into an exact `regit redate …
// --execute` line. Only non-zero, non-default fields are emitted.
func RedateArgMap(strat, tm string, p strategy.Params, linkDates bool) map[string]string {
	m := map[string]string{
		"strategy": canonName(strat),
		"time":     canonName(tm),
		"execute":  "true",
	}
	if !p.Start.IsZero() {
		m["start"] = p.Start.Format("2006-01-02")
	}
	if !p.End.IsZero() {
		m["end"] = p.End.Format("2006-01-02")
	}
	if len(p.Quotas) > 0 {
		m["pin"] = keyValsToString(p.Quotas)
	}
	if len(p.Picked) > 0 {
		m["pick"] = pickedToString(p.Picked)
	}
	if p.PerDayMin > 0 {
		m["per-day-min"] = strconv.Itoa(p.PerDayMin)
	}
	if p.PerDayMax > 0 {
		m["per-day-max"] = strconv.Itoa(p.PerDayMax)
	}
	if p.OffsetDays != 0 {
		m["offset-days"] = strconv.Itoa(p.OffsetDays)
	}
	if p.OffsetHours != 0 {
		m["offset-hours"] = strconv.Itoa(p.OffsetHours)
	}
	if !p.Anchor.IsZero() {
		m["anchor"] = p.Anchor.Format("2006-01-02")
	}
	if p.Scale != 0 && p.Scale != 1 {
		m["scale"] = strconv.FormatFloat(p.Scale, 'g', -1, 64)
	}
	if !p.Deadline.IsZero() {
		m["deadline"] = p.Deadline.Format("2006-01-02")
	}
	if !p.CompressDay.IsZero() {
		m["compress-day"] = p.CompressDay.Format("2006-01-02")
	}
	if !linkDates {
		m["link-dates"] = "false"
	}
	return m
}

// RetitleArgMap builds the flag map for a `regit retitle --execute` line.
func RetitleArgMap(match, replace, commits string) map[string]string {
	m := map[string]string{"match": match, "execute": "true"}
	if replace != "" {
		m["replace"] = replace
	}
	if commits != "" {
		m["commits"] = commits
	}
	return m
}

// DropBodiesArgMap builds the flag map for a `regit drop-bodies --execute` line.
func DropBodiesArgMap(commits string) map[string]string {
	m := map[string]string{"execute": "true"}
	if commits != "" {
		m["commits"] = commits
	}
	return m
}

// SplitArgMap builds the flag map for a `regit split <commitish> --execute` line.
func SplitArgMap(commitish string) map[string]string {
	return map[string]string{"_positional": commitish, "execute": "true"}
}

// RewordArgMap builds the flag map for a `regit reword <commitish> --execute` line.
func RewordArgMap(commitish string) map[string]string {
	return map[string]string{"_positional": commitish, "execute": "true"}
}

// keyValsToString serialises a map[date]count as "DAY=N,DAY=N" in stable
// (sorted) order, matching the --pin flag's wire format.
func keyValsToString(kv map[string]int) string {
	keys := make([]string, 0, len(kv))
	for k := range kv {
		keys = append(keys, k)
	}
	sort.Strings(keys) // stable, day-ordered output
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s=%d", k, kv[k])
	}
	return strings.Join(parts, ",")
}

// pickedToString serialises the Picked map as a comma-joined list of enabled
// days in sorted order, matching the --pick flag's wire format.
func pickedToString(p map[string]bool) string {
	var days []string
	for d, on := range p {
		if on {
			days = append(days, d)
		}
	}
	sort.Strings(days)
	return strings.Join(days, ",")
}
