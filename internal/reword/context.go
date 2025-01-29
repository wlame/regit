package reword

import (
	"os"
	"path/filepath"

	"github.com/wlame/regit/internal/gitio"
)

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
