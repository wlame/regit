package reword

import (
	"strconv"

	"github.com/wlame/regit/internal/promptcache"
)

// Cache and CacheEntry are the reword-result instantiation of the shared
// promptcache implementation (one JSON file per commit hash under
// <gitDir>/regit/cache/reword/, invalidated by fingerprint).
type (
	Cache      = promptcache.Cache[Result]
	CacheEntry = promptcache.Entry[Result]
)

// NewCache roots a reword cache at the repository's git dir (e.g. ".git").
func NewCache(gitDir string) *Cache {
	return promptcache.New[Result](gitDir, "reword")
}

// Fingerprint hashes all result-affecting inputs. Any change to diffHash,
// modelName, contextKey, userHint, or variants produces a different
// fingerprint, which causes Get to return a miss for the old entry.
func Fingerprint(diffHash, modelName, contextKey, userHint string, variants int) string {
	return promptcache.Fingerprint(diffHash, modelName, contextKey, userHint, strconv.Itoa(variants))
}
