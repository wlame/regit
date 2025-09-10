package squash

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Proposal is the LLM's combined commit message for one chain.
type Proposal struct {
	Subject string
	Body    string
}

// cacheVersion is bumped on any backward-incompatible CacheEntry change.
const cacheVersion = 1

// CacheEntry is one persisted squash summary, keyed by the chain's last commit
// hash. Fingerprint captures every input that affects the result. CreatedAt is
// supplied by the caller; the cache never calls time.Now().
type CacheEntry struct {
	Version     int
	CommitHash  string // the chain's last member hash
	Fingerprint string
	Proposal    Proposal
	CreatedAt   time.Time
}

// Cache stores summaries as JSON under <gitDir>/regit/cache/squash/<hash>.json.
type Cache struct{ dir string }

// NewCache roots a cache at the repository's git dir (e.g. ".git").
func NewCache(gitDir string) *Cache {
	return &Cache{dir: filepath.Join(gitDir, "regit", "cache", "squash")}
}

// Fingerprint hashes all result-affecting inputs into a 32-char hex string.
func Fingerprint(chainKey, modelName, extra string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%d\x1f%s\x1f%s\x1f%s", cacheVersion, chainKey, modelName, extra)
	return fmt.Sprintf("%x", h.Sum(nil))[:32]
}

func (c *Cache) path(commitHash string) string {
	return filepath.Join(c.dir, commitHash+".json")
}

// Get returns the cached proposal when an entry exists whose Version and
// Fingerprint both match. A corrupt/unreadable file is treated as a miss.
func (c *Cache) Get(commitHash, fingerprint string) (Proposal, bool) {
	data, err := os.ReadFile(c.path(commitHash))
	if err != nil {
		return Proposal{}, false
	}
	var e CacheEntry
	if err := json.Unmarshal(data, &e); err != nil {
		return Proposal{}, false
	}
	if e.Version != cacheVersion || e.Fingerprint != fingerprint {
		return Proposal{}, false
	}
	return e.Proposal, true
}

// Put serialises the entry, creating the cache dir as needed. Version is always
// stamped to cacheVersion so the stored file is self-consistent.
func (c *Cache) Put(e CacheEntry) error {
	if e.CommitHash == "" {
		return fmt.Errorf("cache: refusing to write entry with empty commit hash")
	}
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return err
	}
	e.Version = cacheVersion
	data, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.path(e.CommitHash), data, 0o644)
}

// Clear removes the entire squash cache directory.
func (c *Cache) Clear() error { return os.RemoveAll(c.dir) }
