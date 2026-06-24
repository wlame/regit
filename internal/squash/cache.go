package squash

import (
	"github.com/wlame/regit/internal/promptcache"
)

// Proposal is the LLM's combined commit message for one chain.
type Proposal struct {
	Subject string
	Body    string
}

// Cache and CacheEntry are the squash-summary instantiation of the shared
// promptcache implementation. Entries are keyed by the chain's LAST member
// hash (one JSON file per chain under <gitDir>/regit/cache/squash/).
type (
	Cache      = promptcache.Cache[Proposal]
	CacheEntry = promptcache.Entry[Proposal]
)

// NewCache roots a squash cache at the repository's git dir (e.g. ".git").
func NewCache(gitDir string) *Cache {
	return promptcache.New[Proposal](gitDir, "squash")
}

// Fingerprint hashes all summary-affecting inputs into a 32-char hex string.
func Fingerprint(chainKey, modelName, extra string) string {
	return promptcache.Fingerprint(chainKey, modelName, extra)
}
