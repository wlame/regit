package capability

import (
	"regexp"
	"strings"
	"unicode"
)

// RetitleTransform builds the title pipeline shared by the headless retitle
// verb and the TUI wizard: optional regex replacement, whitespace trim, then
// the two finishing options. Both surfaces call it, so a logged command
// produces exactly the titles the TUI staged. A nil re skips the replacement.
func RetitleTransform(re *regexp.Regexp, repl string, capitalize, ensurePeriod bool) func(string) string {
	return func(title string) string {
		out := title
		if re != nil {
			out = re.ReplaceAllString(out, repl)
		}
		// Titles never keep leading/trailing whitespace (e.g. after deleting
		// a "feat:" prefix while the space stayed behind).
		out = strings.TrimSpace(out)
		if capitalize {
			out = upperFirst(out)
		}
		if ensurePeriod && out != "" && !endsSentence(out) {
			out += "."
		}
		return out
	}
}

// upperFirst upper-cases the first rune of s.
func upperFirst(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// endsSentence reports whether the title already ends like a sentence, so
// ensure-period never produces endings like "Fix it!.".
func endsSentence(s string) bool {
	r := []rune(s)
	return strings.ContainsRune(".!?…:", r[len(r)-1])
}
