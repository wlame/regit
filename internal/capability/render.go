package capability

import (
	"fmt"
	"sort"
	"strings"
)

// wrapWidth is the column past which Render wraps a long invocation onto a
// continuation line. Footer lines stay copy-pasteable in a shell because the
// break uses a trailing backslash.
const wrapWidth = 76

// Render produces the exact, runnable `regit <verb> …` command for an action,
// from the SAME Param definitions the parser consumes — so the result always
// round-trips through Parse (see render_test.go). Flags whose value equals the
// Param.Default are omitted. A "_positional" key supplies positional args.
// Boolean flags are emitted bare (--execute) only when their value is "true".
func Render(name string, argMap map[string]string) string {
	c, ok := Lookup(name)
	if !ok {
		return "regit " + name
	}
	defaults := map[string]string{}
	for _, p := range c.Params {
		defaults[p.Name] = p.Default
	}

	var parts []string
	parts = append(parts, "regit", c.Name)
	if pos := argMap["_positional"]; pos != "" {
		parts = append(parts, pos)
	}

	// Stable flag order: registry (Param) order, then any extras alphabetically.
	emitted := map[string]bool{}
	emit := func(pName, val string) {
		if pName == "_positional" {
			return
		}
		if val == "" || val == defaults[pName] {
			return
		}
		p, known := paramByName(c, pName)
		if known && p.Type == ParamBool {
			if val == "true" {
				parts = append(parts, "--"+pName)
			}
			return
		}
		parts = append(parts, fmt.Sprintf("--%s=%s", pName, val))
	}
	for _, p := range c.Params {
		if v, ok := argMap[p.Name]; ok {
			emit(p.Name, v)
			emitted[p.Name] = true
		}
	}
	var extras []string
	for k := range argMap {
		if k != "_positional" && !emitted[k] {
			extras = append(extras, k)
		}
	}
	sort.Strings(extras)
	for _, k := range extras {
		emit(k, argMap[k])
	}

	return wrapInvocation(parts)
}

// tokenize splits a rendered invocation back into argv tokens, dropping the
// trailing "\" continuation markers Render inserts. It is the inverse used by the
// round-trip test and by nothing at runtime (the footer only renders).
func tokenize(line string) []string {
	line = strings.ReplaceAll(line, "\\\n", " ")
	return strings.Fields(line)
}

// wrapInvocation joins parts with spaces, inserting "\<newline>  " when a line
// would exceed wrapWidth, so long --pin=… strings stay readable and runnable.
func wrapInvocation(parts []string) string {
	var b strings.Builder
	col := 0
	for i, p := range parts {
		if i > 0 {
			if col+1+len(p) > wrapWidth {
				b.WriteString(" \\\n  ")
				col = 2
			} else {
				b.WriteString(" ")
				col++
			}
		}
		b.WriteString(p)
		col += len(p)
	}
	return b.String()
}

func paramByName(c Command, name string) (Param, bool) {
	for _, p := range c.Params {
		if p.Name == name {
			return p, true
		}
	}
	return Param{}, false
}
