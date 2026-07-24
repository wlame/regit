// Package promptcache persists LLM results as JSON files keyed by a git hash
// and invalidated by a fingerprint of every result-affecting input. It is the
// single implementation behind the split, reword, and squash caches, which
// used to be three near-identical ~90-line clones that could drift.
package promptcache

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Version identifies the on-disk entry schema. Get rejects entries whose
// version differs, so a schema change simply turns old files into misses.
//
// v1 was the per-feature clones, whose payload field carried the feature's
// own name ("Proposal" / "Result"); v2 is this shared implementation with the
// uniform "Payload" field. The bump matters: without it a v1 file would still
// match on fingerprint and deserialize to an EMPTY payload — a false hit.
const Version = 2

// Entry is one persisted result. Key is the git hash the result belongs to (a
// commit hash for split/reword, a chain's last-member hash for squash).
// Fingerprint captures every input that affects the payload, so a stale entry
// (different model, flags, hint, or diff) is detected as a miss. CreatedAt is
// supplied by the caller; the cache never reads the clock, keeping it
// deterministic for tests.
type Entry[T any] struct {
	Version     int
	Key         string
	Fingerprint string
	Payload     T
	CreatedAt   time.Time
}

// Cache stores entries as JSON files under
// <gitDir>/regit/cache/<feature>/<key>.json. One file per key means reads and
// writes are cheap single-file operations.
type Cache[T any] struct{ dir string }

// New roots a cache at the repository's git dir (e.g. ".git") under the given
// feature name. The directory is created lazily on the first Put.
func New[T any](gitDir, feature string) *Cache[T] {
	return &Cache[T]{dir: filepath.Join(gitDir, "regit", "cache", feature)}
}

// Path returns the JSON file path for the given key (exported for tests and
// tooling that inspect or corrupt entries deliberately).
func (c *Cache[T]) Path(key string) string {
	return filepath.Join(c.dir, key+".json")
}

// Get returns the cached payload when an entry exists whose Version and
// Fingerprint both match; otherwise ok is false. A file that cannot be read
// or parsed is silently treated as a miss so a corrupt cache never blocks the
// caller.
func (c *Cache[T]) Get(key, fingerprint string) (T, bool) {
	var zero T
	data, err := os.ReadFile(c.Path(key))
	if err != nil {
		return zero, false
	}
	var e Entry[T]
	if err := json.Unmarshal(data, &e); err != nil {
		return zero, false
	}
	if e.Version != Version || e.Fingerprint != fingerprint {
		return zero, false
	}
	return e.Payload, true
}

// Put serialises the entry to disk, creating the cache directory as needed.
// It always stamps Version = Version regardless of what the caller passed, so
// the stored file is always self-consistent.
//
// The write is ATOMIC: data goes to a temp file in the same directory which is
// then renamed over the target. A crash mid-write, a full disk, or a second
// regit writing the same key concurrently can therefore never leave a
// truncated JSON file — readers see either the old complete entry or the new
// one, never a torn mix.
func (c *Cache[T]) Put(e Entry[T]) error {
	if e.Key == "" {
		return fmt.Errorf("cache: refusing to write entry with empty key")
	}
	if err := os.MkdirAll(c.dir, 0o700); err != nil {
		return err
	}
	e.Version = Version
	data, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(c.dir, "put-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// The remove is a no-op after a successful rename; it only cleans up the
	// temp file on the error paths.
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// 0600, matching CreateTemp's own default. A cache entry holds the commit
	// diff that produced it, which for a split or redact run can contain the
	// secret the operator is removing — so the file must not be readable by
	// other local users. (This deliberately reverses an earlier widening to
	// 0644 that was made only for consistency with WriteFile.)
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpName, c.Path(e.Key))
}

// Clear removes the entire cache directory and all entries within it.
func (c *Cache[T]) Clear() error { return os.RemoveAll(c.dir) }

// Fingerprint hashes the given fields into a 32-character hex string (128
// bits — ample for collision resistance). Fields are joined with a
// unit-separator byte so adjacent fields cannot collide.
func Fingerprint(fields ...string) string {
	h := sha256.New()
	h.Write([]byte(strings.Join(fields, "\x1f")))
	return fmt.Sprintf("%x", h.Sum(nil))[:32]
}

// ContentHash returns a short hex digest of s, used to fold variable-length
// inputs (e.g. README content) into a fingerprint component without bloating
// it.
func ContentHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", sum[:8])
}
