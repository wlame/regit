package reword

import (
	"os"
	"path/filepath"

	"github.com/wlame/regit/internal/gitio"
)

// READMEMaxBytes is the byte cap applied by ReadmeForContext when including the
// README in a prompt. Large READMEs are truncated to keep the prompt within
// practical token budgets.
const READMEMaxBytes = 4000

// GatherDiff returns the wide-context unified diff for a commit.
// It delegates directly to repo.DiffText so all git access stays in gitio.
func GatherDiff(repo *gitio.Repo, parentTree, commitTree string, contextLines int) (string, error) {
	return repo.DiffText(parentTree, commitTree, contextLines)
}

// ReadREADME returns the repo's README.md contents truncated to maxBytes, or ""
// if none exists. It reads from the working tree root (the file the user sees);
// a missing file is not an error.
func ReadREADME(repo *gitio.Repo, maxBytes int) string {
	data, err := os.ReadFile(filepath.Join(repo.Root(), "README.md"))
	if err != nil {
		return ""
	}
	if maxBytes > 0 && len(data) > maxBytes {
		return string(data[:maxBytes])
	}
	return string(data)
}

// ReadmeForContext returns the repo's README (capped to READMEMaxBytes) when
// the context slice contains "readme", and "" otherwise. Both front-ends
// (headless and TUI) call this so README inclusion is controlled by the same
// reword-config/flag in both paths.
func ReadmeForContext(repo *gitio.Repo, context []string) string {
	for _, k := range context {
		if k == "readme" {
			return ReadREADME(repo, READMEMaxBytes)
		}
	}
	return ""
}
