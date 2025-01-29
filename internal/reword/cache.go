package reword

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// cacheVersion is bumped whenever the CacheEntry schema changes in a
// backward-incompatible way. Get rejects entries whose version does not match.
const cacheVersion = 1

// CacheEntry is one persisted reword result keyed by commit hash. Fingerprint
// captures every input that affects the result, so a stale entry (different
// model, flags, hint, or diff) is detected as a miss. CreatedAt is supplied by
// the caller; the cache package never calls time.Now() itself.
type CacheEntry struct {
	Version     int
	CommitHash  string
	Fingerprint string
	Result      Result
	// CreatedAt records when the entry was created. Callers are expected to pass
	// the current time; the cache itself remains clock-free for testability.
	CreatedAt time.Time
}

// Cache stores reword results as JSON files under
// <gitDir>/regit/cache/reword/<commitHash>.json. One file per commit hash means
// reads and writes are cheap single-file operations.
type Cache struct{ dir string }

// NewCache roots a cache at the repository's git dir (e.g. ".git"). The cache
// directory is created lazily on the first Put.
func NewCache(gitDir string) *Cache {
	return &Cache{dir: filepath.Join(gitDir, "regit", "cache", "reword")}
}

// Fingerprint hashes all result-affecting inputs into a 32-character hex
// string. Any change to diffHash, modelName, contextKey, userHint, or
// variants produces a different fingerprint, which causes Get to return a
// miss for the old entry.
func Fingerprint(diffHash, modelName, contextKey, userHint string, variants int) string {
	h := sha256.New()
	// Unit-separator (0x1F) delimiters prevent collisions across adjacent fields.
	fmt.Fprintf(h, "%s\x1f%s\x1f%s\x1f%s\x1f%d", diffHash, modelName, contextKey, userHint, variants)
	return fmt.Sprintf("%x", h.Sum(nil))[:32]
}

// path returns the JSON file path for the given commit hash.
func (c *Cache) path(commitHash string) string {
	return filepath.Join(c.dir, commitHash+".json")
}

// Get returns the cached result when an entry exists whose Version and
// Fingerprint both match; otherwise ok is false. A file that cannot be read or
// parsed is silently treated as a miss so a corrupt cache never blocks the
// caller.
func (c *Cache) Get(commitHash, fingerprint string) (Result, bool) {
	data, err := os.ReadFile(c.path(commitHash))
	if err != nil {
		return Result{}, false
	}
	var e CacheEntry
	if err := json.Unmarshal(data, &e); err != nil {
		return Result{}, false
	}
	if e.Version != cacheVersion || e.Fingerprint != fingerprint {
		return Result{}, false
	}
	return e.Result, true
}

// Put serialises the entry to disk, creating the cache directory as needed.
// It always stamps Version = cacheVersion regardless of what the caller
// passed in CacheEntry.Version, so the stored file is always self-consistent.
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

// Clear removes the entire reword cache directory and all entries within it.
func (c *Cache) Clear() error { return os.RemoveAll(c.dir) }
