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
			} else {
				parts = append(parts, "--"+pName+"=false")
			}
			return
		}
		parts = append(parts, fmt.Sprintf("--%s=%s", pName, shellQuote(val)))
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

// shellQuote wraps s in single quotes when it contains whitespace or shell
// metacharacters, so the rendered invocation is copy-pasteable. Embedded single
// quotes are escaped by the standard POSIX single-quote idiom. Values with no
// special characters are returned unchanged, so space-free tokens look as before.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	needsQuoting := false
	for _, ch := range s {
		switch ch {
		case ' ', '\t', '\n', '\'', '"', '$', '\\', '`', ';', '&', '|',
			'<', '>', '(', ')', '*', '?', '[', ']', '!':
			needsQuoting = true
		}
		if needsQuoting {
			break
		}
	}
	if !needsQuoting {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// tokenize splits a rendered invocation back into argv tokens, dropping the
// trailing "\" continuation markers Render inserts. Unlike strings.Fields it is
// single-quote-aware: characters inside '...' are kept together as one token,
// and shellQuote's backslash-escaped quote is handled correctly so a
// value like "^feat: " round-trips through Render → tokenize intact.
func tokenize(line string) []string {
	// Remove the trailing-backslash + newline continuation markers that
	// wrapInvocation inserts between long flag tokens.
	line = strings.ReplaceAll(line, "\\\n", " ")

	var tokens []string
	var cur strings.Builder
	inSingleQuote := false

	for i := 0; i < len(line); {
		ch := line[i]
		if inSingleQuote {
			if ch == '\'' {
				// End of single-quoted segment.
				inSingleQuote = false
				i++
			} else {
				cur.WriteByte(ch)
				i++
			}
		} else {
			switch ch {
			case ' ', '\t':
				if cur.Len() > 0 {
					tokens = append(tokens, cur.String())
					cur.Reset()
				}
				i++
			case '\'':
				// Begin a single-quoted segment; the quote character itself is dropped.
				inSingleQuote = true
				i++
			case '\\':
				// Outside single quotes a backslash escapes the next character
				// literally (this handles the '\'' sequence shellQuote emits).
				if i+1 < len(line) {
					cur.WriteByte(line[i+1])
					i += 2
				} else {
					i++
				}
			default:
				cur.WriteByte(ch)
				i++
			}
		}
	}
	if cur.Len() > 0 {
		tokens = append(tokens, cur.String())
	}
	return tokens
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
