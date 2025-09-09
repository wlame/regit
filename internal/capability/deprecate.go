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
// dry-run); --dry-run in any form is dropped (dry-run is the default). Non-legacy
// verbs and all other flags pass through untouched.
func DeprecatedRewrite(verb string, args []string, warn io.Writer) []string {
	if verb != "split" && verb != "reword" {
		return args
	}
	var out []string
	for _, a := range args {
		name, val, hasVal := strings.Cut(strings.TrimLeft(a, "-"), "=")
		switch name {
		case "yes":
			fmt.Fprintln(warn, "regit: --yes is deprecated; use --execute")
			if !hasVal || parseBoolDefault(val, true) {
				out = append(out, "--execute")
			}
		case "dry-run":
			fmt.Fprintln(warn, "regit: --dry-run is the default now and was ignored")
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
