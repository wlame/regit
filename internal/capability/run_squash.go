// internal/capability/run_squash.go
package capability

import (
	"strings"

	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/model"
)

// parseSquashSpec parses a comma-separated list of A..B ranges and single refs
// into the union of selected commit hashes. Each item resolves via resolveTargets,
// so A..B uses git semantics (commits AFTER A through B; A excluded) and a single
// ref selects just that commit. Whitespace around items is trimmed.
func parseSquashSpec(repo *gitio.Repo, commits []model.Commit, spec string) (map[string]bool, error) {
	selected := map[string]bool{}
	for _, item := range strings.Split(spec, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		targets, err := resolveTargets(repo, commits, item)
		if err != nil {
			return nil, err
		}
		for _, c := range targets {
			selected[c.Hash] = true
		}
	}
	return selected, nil
}
