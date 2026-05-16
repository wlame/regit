package capability

import (
	"fmt"
	"io"
	"strconv"
	"strings"
)

// DeprecatedRewrite translates retired flags for the legacy verbs to the new
// convention, emitting a one-line notice. It recognizes both the bare forms
// (--yes, --dry-run) and the value forms (--yes=true, --dry-run=true): --yes (or
// --yes=true) becomes --execute; --yes=false is dropped (the default is already a
// dry-run); --dry-run and --dry-run=true are dropped (dry-run is the default);
// --dry-run=false — the legacy "apply" — becomes --execute. Non-legacy verbs and
// all other flags pass through untouched.
func DeprecatedRewrite(verb string, args []string, warn io.Writer) []string {
	if verb != "split" && verb != "reword" {
		return args
	}
	var out []string
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			out = append(out, a)
			continue
		}
		name, val, hasVal := strings.Cut(strings.TrimLeft(a, "-"), "=")
		switch name {
		case "yes":
			fmt.Fprintln(warn, "regit: --yes is deprecated; use --execute")
			if !hasVal || parseBoolDefault(val, true) {
				out = append(out, "--execute")
			}
		case "dry-run":
			// Legacy semantics: --dry-run=false meant "actually apply". Dropping
			// it silently would turn an intended apply into a preview, so it
			// translates to --execute; every other form is the current default.
			if hasVal && !parseBoolDefault(val, true) {
				fmt.Fprintln(warn, "regit: --dry-run=false is deprecated; use --execute (translated)")
				out = append(out, "--execute")
			} else {
				fmt.Fprintln(warn, "regit: --dry-run is the default now and was ignored")
			}
		default:
			out = append(out, a)
		}
	}
	return out
}

// parseBoolDefault parses a boolean flag value, returning def when it is not a
// recognized bool (so a malformed --yes=xyz keeps the legacy "apply" intent).
func parseBoolDefault(s string, def bool) bool {
	if b, err := strconv.ParseBool(s); err == nil {
		return b
	}
	return def
}
