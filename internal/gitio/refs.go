package gitio

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// BackupRefPrefix namespaces every backup ref regit creates so they are easy to
// list, restore from, and prune without colliding with user refs.
const BackupRefPrefix = "refs/regit/backup/"

// BackupRef is one saved snapshot of a branch tip created before a rewrite.
type BackupRef struct {
	Name      string // full ref name, e.g. refs/regit/backup/main/1717689600-0
	Hash      string // commit the ref points at
	Branch    string // branch the backup was taken on; "" for legacy flat refs
	Timestamp int64  // unix seconds embedded in the name; 0 if unparsable
}

// ResolveRef returns the commit hash a ref or revision points to.
func (r *Repo) ResolveRef(rev string) (string, error) {
	return r.git("rev-parse", "--verify", rev)
}

// UpdateRef points a ref at newValue. When oldValue is non-empty git performs a
// compare-and-swap, refusing the update if the ref no longer matches oldValue —
// a guard against clobbering concurrent changes.
func (r *Repo) UpdateRef(ref, newValue, oldValue string) error {
	args := []string{"update-ref", ref, newValue}
	if oldValue != "" {
		args = append(args, oldValue)
	}
	_, err := r.git(args...)
	return err
}

// DeleteRef removes a ref (used when pruning backups).
func (r *Repo) DeleteRef(ref string) error {
	_, err := r.git("update-ref", "-d", ref)
	return err
}

// BackupRefDocPattern is the human-readable shape of the names BackupRefName
// produces. The README safety table shows it and the docs drift test asserts
// it appears there; keep it in lockstep with BackupRefName directly below
// (TestBackupRefDocPattern_rendersLikeBackupRefName enforces the pairing).
const BackupRefDocPattern = "refs/regit/backup/<branch>/<ts>-<seq>"

// BackupRefName renders the branch-qualified, sequence-suffixed backup ref name.
// Branch names may contain '/'; the final path segment is always "<ts>-<seq>",
// so parsing splits on the LAST '/'. Legacy refs (a bare "<ts>") predate this
// scheme and parse with Branch "".
func BackupRefName(branch string, unixTS int64, seq int) string {
	return fmt.Sprintf("%s%s/%d-%d", BackupRefPrefix, branch, unixTS, seq)
}

// zeroOID returns the all-zeros object ID sized to match hash (works for both
// SHA-1 and SHA-256 repos). git update-ref treats it as "ref must not exist".
func zeroOID(hash string) string {
	return strings.Repeat("0", len(hash))
}

// maxBackupSeq bounds the collision-retry loop; hitting it means something is
// mass-creating refs and deserves a hard error rather than an infinite loop.
const maxBackupSeq = 100

// CreateBackupRef creates refs/regit/backup/<branch>/<ts>-<seq> pointing at
// head, bumping <seq> until an unused name is found. Creation uses a
// must-not-exist compare-and-swap (all-zeros old value), so an existing backup
// can never be silently overwritten (H2).
func (r *Repo) CreateBackupRef(branch string, unixTS int64, head string) (string, error) {
	for seq := 0; seq < maxBackupSeq; seq++ {
		ref := BackupRefName(branch, unixTS, seq)
		if err := r.UpdateRef(ref, head, zeroOID(head)); err == nil {
			return ref, nil
		} else if _, exists := r.ResolveRef(ref); exists != nil {
			// The ref does NOT exist yet the guarded create failed → a real
			// error (permissions, bad name), not a collision. Surface it.
			//
			// Tradeoff: if another process deleted this exact ref in the tiny
			// window between the failed create above and this re-resolve, we'd
			// misreport a collision as a real error. Harmless (the caller sees
			// an error and can retry) and bounded to a single-process CLI where
			// no other process is racing us to create backup refs.
			return "", fmt.Errorf("create backup ref %s: %w", ref, err)
		}
		// Ref already exists (same-second apply): try the next sequence number.
	}
	return "", fmt.Errorf("create backup ref: %d refs already exist for %s at %d", maxBackupSeq, branch, unixTS)
}

// AppliedRefPrefix namespaces the "applied head" refs. For each backup created
// before a rewrite, regit records the head that rewrite PRODUCED under a
// matching applied ref, so undo can tell whether the branch still points where
// the apply left it (or has advanced past that point).
const AppliedRefPrefix = "refs/regit/applied/"

// appliedRefFor maps a backup ref name to its paired applied ref name by
// swapping the prefix; both share the same "<branch>/<ts>-<seq>" tail.
func appliedRefFor(backupRef string) string {
	return AppliedRefPrefix + strings.TrimPrefix(backupRef, BackupRefPrefix)
}

// RecordAppliedHead points the applied ref paired with backupRef at head. Best
// effort in spirit but returns the error so a future caller could log it; a
// missing applied ref simply disables the advanced-HEAD guard for that backup.
func (r *Repo) RecordAppliedHead(backupRef, head string) error {
	return r.UpdateRef(appliedRefFor(backupRef), head, "")
}

// AppliedHeadFor returns the recorded produced-head for a backup, and whether
// one exists. A backup without a paired applied ref (e.g. a legacy backup)
// yields ("", false, nil).
func (r *Repo) AppliedHeadFor(backupRef string) (string, bool, error) {
	ref := appliedRefFor(backupRef)
	hash, err := r.ResolveRef(ref)
	if err != nil {
		return "", false, nil // absent → guard simply does not apply
	}
	return hash, true, nil
}

// DeleteAppliedRef removes the applied ref paired with a backup (used when a
// backup is pruned; no prune command ships in M4, so this is for completeness
// and future M6 use).
func (r *Repo) DeleteAppliedRef(backupRef string) error {
	return r.DeleteRef(appliedRefFor(backupRef))
}

// BackupRefBranch extracts the branch a backup ref was taken on, or "" for
// legacy flat refs that predate branch qualification.
func BackupRefBranch(name string) string {
	rest := strings.TrimPrefix(name, BackupRefPrefix)
	if i := strings.LastIndexByte(rest, '/'); i >= 0 {
		return rest[:i]
	}
	return ""
}

// backupRefTimestamp parses the unix timestamp out of the final "<ts>-<seq>"
// (or legacy bare "<ts>") segment; 0 when unparsable.
func backupRefTimestamp(name string) int64 {
	rest := strings.TrimPrefix(name, BackupRefPrefix)
	if i := strings.LastIndexByte(rest, '/'); i >= 0 {
		rest = rest[i+1:]
	}
	if i := strings.IndexByte(rest, '-'); i >= 0 {
		rest = rest[:i]
	}
	ts, _ := strconv.ParseInt(rest, 10, 64)
	return ts
}

// ListBackupRefs returns every regit backup ref, newest first (by embedded
// timestamp, then name as tiebreak so ordering stays deterministic).
func (r *Repo) ListBackupRefs() ([]BackupRef, error) {
	out, err := r.gitRaw("for-each-ref", "--format=%(refname) %(objectname)", BackupRefPrefix)
	if err != nil {
		return nil, err
	}
	var refs []BackupRef
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) != 2 {
			continue
		}
		refs = append(refs, BackupRef{
			Name:      parts[0],
			Hash:      parts[1],
			Branch:    BackupRefBranch(parts[0]),
			Timestamp: backupRefTimestamp(parts[0]),
		})
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Timestamp != refs[j].Timestamp {
			return refs[i].Timestamp > refs[j].Timestamp
		}
		return refs[i].Name > refs[j].Name
	})
	return refs, nil
}

// CurrentBranch returns the checked-out branch name, or an error if HEAD is
// detached (rewrites require a branch to move).
func (r *Repo) CurrentBranch() (string, error) {
	name, err := r.git("symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return "", fmt.Errorf("HEAD is detached; check out a branch before rewriting")
	}
	return name, nil
}

// BranchRef converts a branch name to its full ref path.
func BranchRef(branch string) string {
	return "refs/heads/" + branch
}
