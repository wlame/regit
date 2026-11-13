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
// --execute` line. Only non-zero, non-default fields are emitted. commits is
// the rendered --commits target spec (see Model.redateCommitSpec): empty means
// the whole active range, matching the flag's own default.
func RedateArgMap(
	strat, tm string, p strategy.Params, linkDates bool, commits string,
) map[string]string {
	m := map[string]string{
		"strategy": canonName(strat),
		"time":     canonName(tm),
		"execute":  "true",
	}
	if commits != "" {
		m["commits"] = commits
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
	// Time-model parameters. time-fixed is REQUIRED by Parse when time=Fixed-time,
	// so omitting it produced a logged line the CLI rejected — the round-trip
	// contract violation this closes. jitter-min reproduces the Jitter spread, and
	// seed makes every randomized strategy replay to the same distribution (the
	// headless run resolves seed 0 to a clock value, so the concrete seed must be
	// emitted or "preview then --execute" would diverge).
	switch canonName(tm) {
	case "Fixed-time":
		m["time-fixed"] = minToClock(p.FixedMin)
	case "Jitter":
		m["jitter-min"] = strconv.Itoa(p.JitterMin)
	}
	if p.Seed != 0 {
		m["seed"] = strconv.FormatInt(p.Seed, 10)
	}
	if !linkDates {
		m["link-dates"] = "false"
	}
	return m
}

// minToClock formats a minutes-from-midnight value as "HH:MM", the inverse of
// clockToMin, so a rendered --time-fixed / footer value re-parses.
func minToClock(min int) string {
	if min < 0 {
		min = 0
	}
	return fmt.Sprintf("%02d:%02d", (min/60)%24, min%60)
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

// RewordArgMap builds the flag map for a `regit reword <commitish>… --execute`
// line. It is variadic because a reword walks a whole selected set; pass every
// reworded commit so the logged command reproduces the entire walk, not just the
// last commit. Hashes are space-joined into the positional slot. (SplitArgMap stays
// single-commit because split targets exactly one commit.)
func RewordArgMap(commitishes ...string) map[string]string {
	return map[string]string{"_positional": strings.Join(commitishes, " "), "execute": "true"}
}

// SquashArgMap builds the flag map for a `regit squash <spec> --execute` line. The
// TUI passes the chains as a comma-joined "first^..last" spec so the logged command
// round-trips through parseSquashSpec to the same commits.
func SquashArgMap(spec string) map[string]string {
	return map[string]string{"_positional": spec, "execute": "true"}
}

// ReauthorArgMap builds the flag map for a `regit reauthor --execute` line. The
// committer flag defaults to true, so it is emitted only when the caller turned
// it off (Render omits any value equal to the Param default). commits is the
// comma-joined target spec the TUI selection produced.
func ReauthorArgMap(name, email string, committer bool, commits string) map[string]string {
	m := map[string]string{"execute": "true"}
	if name != "" {
		m["name"] = name
	}
	if email != "" {
		m["email"] = email
	}
	if !committer {
		m["committer"] = "false"
	}
	if commits != "" {
		m["commits"] = commits
	}
	return m
}

// PurgeArgMap builds the flag map for a `regit purge <paths> --execute` line.
// Paths render as positionals, matching the registry's variadic positional
// slot. Render never shell-quotes "_positional" (split's single commitish,
// squash's "a^..b,c" spec, and reword's space-joined hashes never need it),
// so a path containing whitespace is quoted HERE, per-path, before joining —
// otherwise "my file.txt" would split into two separate positional tokens
// once the rendered line is tokenized or pasted into a real shell. Quoting
// per-path (rather than teaching Render to quote "_positional" generically)
// keeps every other verb's positional rendering byte-identical to before.
func PurgeArgMap(paths []string) map[string]string {
	quoted := make([]string, len(paths))
	for i, p := range paths {
		quoted[i] = shellQuote(p)
	}
	return map[string]string{"_positional": strings.Join(quoted, " "), "execute": "true"}
}

// RedactArgMap builds the flag map for a `regit redact --execute` line.
// Unlike most other builders here, every flag is set UNCONDITIONALLY (even
// when empty) rather than only when non-zero: the redact footer is a full,
// verbatim projection of the operator's selection, deliberately including
// --match itself. There is no privacy upside to hiding the secret from the
// logged command: this same invocation already writes it, verbatim, to
// .git/regit/command.log and the operator's shell history (see runRedact's
// hygiene warning) — and hiding it from the footer would make the logged
// line non-reproducible. Render's own default/empty-value omission (see
// render.go's emit) still trims the map down to only the flags that differ
// from their registry default, exactly as it does for every other verb, so
// setting every key here costs nothing extra in the rendered output.
func RedactArgMap(match string, globs []string, mode, pattern, with string, cascade bool) map[string]string {
	return map[string]string{
		"match":           match,
		"in":              strings.Join(globs, ","),
		"mode":            mode,
		"replace-pattern": pattern,
		"replace-with":    with,
		"no-cascade":      strconv.FormatBool(!cascade),
		"execute":         "true",
	}
}

// SecretPlaceholder stands in for a redacted secret in a logged command.
const SecretPlaceholder = "<secret>"

// MaskSecretArgs returns a copy of a redact argMap with every occurrence of
// secret replaced by SecretPlaceholder, so the rendered line can be written
// to the persistent command log without recording the credential. The
// replace-pattern is masked too, because users often paste the literal into it.
func MaskSecretArgs(args map[string]string, secret string) map[string]string {
	out := make(map[string]string, len(args))
	for k, v := range args {
		if secret != "" {
			v = strings.ReplaceAll(v, secret, SecretPlaceholder)
		}
		out[k] = v
	}
	return out
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
