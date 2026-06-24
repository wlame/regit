package split

import (
	"strconv"

	"github.com/wlame/regit/internal/model"
	"github.com/wlame/regit/internal/promptcache"
)

// Cache and CacheEntry are the split-proposal instantiation of the shared
// promptcache implementation (one JSON file per commit hash under
// <gitDir>/regit/cache/split/, invalidated by fingerprint).
type (
	Cache      = promptcache.Cache[model.SplitProposal]
	CacheEntry = promptcache.Entry[model.SplitProposal]
)

// NewCache roots a split cache at the repository's git dir (e.g. ".git").
func NewCache(gitDir string) *Cache {
	return promptcache.New[model.SplitProposal](gitDir, "split")
}

// Fingerprint hashes all proposal-affecting inputs. Any change to diffHash,
// modelName, contextKey, userHint, or maxCommits produces a different
// fingerprint, which causes Get to return a miss for the old entry.
func Fingerprint(diffHash, modelName, contextKey, userHint string, maxCommits int) string {
	return promptcache.Fingerprint(diffHash, modelName, contextKey, userHint, strconv.Itoa(maxCommits))
}
