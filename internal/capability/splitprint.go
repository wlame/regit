// internal/capability/splitprint.go — printProposal/printEquivalentCommands moved
// from cmd/regit/split.go, writing to an io.Writer and using Short().
package capability

import (
	"fmt"
	"io"
	"os"

	"github.com/wlame/regit/internal/config"
	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/model"
	"github.com/wlame/regit/internal/termsafe"
)

func printProposal(w io.Writer, target model.Commit, prop model.SplitProposal) {
	fmt.Fprintf(w, "Split proposal for %s %q\n", Short(target.Hash), target.Subject)
	fmt.Fprintf(w, "  %d commits planned\n\n", len(prop.Groups))
	for i, g := range prop.Groups {
		wip := ""
		if g.WIP {
			wip = " [WIP]"
		}
		fmt.Fprintf(w, "  %d. %s%s\n", i+1, termsafe.Line(g.Subject), wip)
		for _, id := range g.HunkIDs {
			fmt.Fprintf(w, "       %s\n", id)
		}
	}
}

func printEquivalentCommands(w io.Writer, target model.Commit, prop model.SplitProposal) {
	fmt.Fprintf(w, "\nEquivalent git commands (for reference):\n")
	fmt.Fprintf(w, "  # splitting commit %s %q\n", Short(target.Hash), target.Subject)
	for i, g := range prop.Groups {
		if i == 0 {
			fmt.Fprintf(w, "  git commit-tree <tree%d> -p %s -m %q\n", i+1, Short(prop.ParentHash), g.Subject)
		} else {
			fmt.Fprintf(w, "  git commit-tree <tree%d> -p <prev> -m %q\n", i+1, g.Subject)
		}
	}
	fmt.Fprintf(w, "  git update-ref HEAD <last-sub-commit>\n")
}

// readmeForSplit reads README.md from the repo root when "readme" appears in
// cfg.Split.Context. Returns an empty string when not requested or unreadable
// (a missing README is not an error — it just provides no context).
func readmeForSplit(repo *gitio.Repo, cfg config.Config) string {
	for _, ctx := range cfg.Split.Context {
		if ctx == "readme" {
			data, err := os.ReadFile(repo.Root() + "/README.md")
			if err != nil {
				return "" // README absent or unreadable — silently ignore
			}
			return string(data)
		}
	}
	return ""
}
