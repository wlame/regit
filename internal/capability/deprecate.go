package capability

import (
	"fmt"
	"io"
)

// DeprecatedRewrite translates retired flags for the legacy verbs to the new
// convention, emitting a one-line notice. --yes → --execute; --dry-run dropped
// (dry-run is the default). Non-legacy verbs pass through untouched.
func DeprecatedRewrite(verb string, args []string, warn io.Writer) []string {
	if verb != "split" && verb != "reword" {
		return args
	}
	var out []string
	for _, a := range args {
		switch a {
		case "--yes", "-yes":
			fmt.Fprintln(warn, "regit: --yes is deprecated; use --execute")
			out = append(out, "--execute")
		case "--dry-run", "-dry-run":
			fmt.Fprintln(warn, "regit: --dry-run is the default now and was ignored")
		default:
			out = append(out, a)
		}
	}
	return out
}
