package gittest

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Snapshot is a complete, comparable picture of a repository's mutable state:
// where HEAD points, every ref, the index, and the working tree.
//
// It exists so "the repository is unchanged" can be an assertion rather than a
// comment. It deliberately shells out to git ITSELF rather than going through
// internal/gitio: an oracle built on the code it validates lets a bug hide from
// the very check meant to catch it (the same reasoning recorded for
// safety.redactLineSurvives). internal/gittest imports no other internal package.
//
// Dangling objects are deliberately NOT captured. They are unreachable and
// reclaimed by `git gc`, and safety.runApply already documents them as the
// expected residue of an aborted apply.
type Snapshot struct {
	Head     string            // symbolic-ref target, or "detached"
	HeadHash string            // resolved HEAD commit, "" in an empty repository
	Refs     map[string]string // refs/**  -> OID
	Index    map[string]string // path     -> "mode OID stage"
	Worktree map[string]string // path     -> "mode sha256hex"
}

// Snap captures dir's current state. Every git invocation runs under Env, so a
// fixture's snapshot can never be perturbed by the developer's real git config.
func Snap(t *testing.T, dir string) Snapshot {
	t.Helper()
	s := Snapshot{
		Refs:     map[string]string{},
		Index:    map[string]string{},
		Worktree: map[string]string{},
	}

	// A detached HEAD has no symbolic ref; that is a state worth capturing, not
	// an error, so the failure is folded into a sentinel value.
	if out, err := snapGit(t, dir, "symbolic-ref", "HEAD"); err == nil {
		s.Head = strings.TrimSpace(out)
	} else {
		s.Head = "detached"
	}
	// An empty repository has no HEAD commit. Also not an error.
	if out, err := snapGit(t, dir, "rev-parse", "HEAD"); err == nil {
		s.HeadHash = strings.TrimSpace(out)
	}

	out, err := snapGit(t, dir, "for-each-ref", "--format=%(refname) %(objectname)")
	if err != nil {
		t.Fatalf("gittest.Snap: for-each-ref: %v", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		if name, oid, ok := strings.Cut(line, " "); ok {
			s.Refs[name] = oid
		}
	}

	// ls-files --stage emits "<mode> <oid> <stage>\t<path>". Splitting on the TAB
	// first keeps paths containing spaces intact.
	out, err = snapGit(t, dir, "ls-files", "--stage")
	if err != nil {
		t.Fatalf("gittest.Snap: ls-files --stage: %v", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		meta, path, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		s.Index[path] = strings.Join(strings.Fields(meta), " ")
	}

	// --cached --others --exclude-standard is exactly the file set
	// `git status --porcelain` reports on, which is what gitio.IsDirty gates
	// against. Ignored files are excluded on purpose: build output changing
	// would otherwise fail the oracle for reasons unrelated to the operation.
	out, err = snapGit(t, dir, "ls-files", "--cached", "--others", "--exclude-standard")
	if err != nil {
		t.Fatalf("gittest.Snap: ls-files --cached --others: %v", err)
	}
	for _, path := range strings.Split(strings.TrimSpace(out), "\n") {
		if path == "" {
			continue
		}
		full := filepath.Join(dir, path)
		info, err := os.Lstat(full)
		if err != nil {
			continue // listed but absent: a staged deletion, nothing to hash
		}
		content, err := os.ReadFile(full)
		if err != nil {
			continue // unreadable (e.g. a symlink to nowhere): skip rather than fail
		}
		s.Worktree[path] = fmt.Sprintf("%o %x", info.Mode().Perm(), sha256.Sum256(content))
	}
	return s
}

// snapGit runs one git command under the hermetic Env and returns stdout.
func snapGit(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = Env(dir)
	out, err := cmd.Output()
	return string(out), err
}

// Delta is the ordered, human-readable difference between two Snapshots. It
// carries rendered strings rather than structured diffs because its only
// consumer is a test failure message that must name the exact thing that moved.
type Delta struct{ Changes []string }

// IsEmpty reports whether the two snapshots were identical.
func (d Delta) IsEmpty() bool { return len(d.Changes) == 0 }

// String renders one change per line, indented for a t.Errorf block.
func (d Delta) String() string {
	if d.IsEmpty() {
		return "(no changes)"
	}
	return "\n  " + strings.Join(d.Changes, "\n  ")
}

// Diff reports every way other differs from s, sorted for deterministic output.
func (s Snapshot) Diff(other Snapshot) Delta {
	var out []string
	if s.Head != other.Head {
		out = append(out, fmt.Sprintf("HEAD symref: %s -> %s", s.Head, other.Head))
	}
	if s.HeadHash != other.HeadHash {
		out = append(out, fmt.Sprintf("HEAD commit: %s -> %s", short(s.HeadHash), short(other.HeadHash)))
	}
	out = append(out, diffMaps("ref", s.Refs, other.Refs)...)
	out = append(out, diffMaps("index", s.Index, other.Index)...)
	out = append(out, diffMaps("worktree", s.Worktree, other.Worktree)...)
	sort.Strings(out)
	return Delta{Changes: out}
}

// diffMaps renders added / removed / changed entries between two keyed maps.
func diffMaps(kind string, before, after map[string]string) []string {
	var out []string
	for k, bv := range before {
		av, ok := after[k]
		if !ok {
			out = append(out, fmt.Sprintf("%s removed: %s", kind, k))
			continue
		}
		if av != bv {
			out = append(out, fmt.Sprintf("%s changed: %s (%s -> %s)", kind, k, short(bv), short(av)))
		}
	}
	for k := range after {
		if _, ok := before[k]; !ok {
			out = append(out, fmt.Sprintf("%s added: %s", kind, k))
		}
	}
	return out
}

// short trims an OID for a readable message, leaving short values alone.
func short(v string) string {
	if len(v) > 8 && !strings.Contains(v, " ") {
		return v[:8]
	}
	return v
}

// backupRefAdded reports whether a change line is the one residue an aborted
// apply is allowed to leave: a newly created refs/regit/backup/* ref.
func backupRefAdded(change string) bool {
	return strings.HasPrefix(change, "ref added: refs/regit/backup/")
}

// regitRefAdded reports whether a change line is any newly created regit
// bookkeeping ref (backup or applied), which a SUCCESSFUL apply creates.
func regitRefAdded(change string) bool {
	return strings.HasPrefix(change, "ref added: refs/regit/")
}

// AssertAbortedCleanly fails unless the repository is byte-identical apart from
// an added backup ref. This is the strongest claim regit makes about failure:
// a guard, verification, or structural abort must leave HEAD, every branch and
// tag, the index, and every working-tree file exactly as they were.
//
// An aborted apply never records an applied ref — safety.runApply calls
// RecordAppliedHead only after the compare-and-swap succeeds — so only backup
// refs are tolerated here.
func AssertAbortedCleanly(t *testing.T, before, after Snapshot) {
	t.Helper()
	var unexpected []string
	for _, c := range before.Diff(after).Changes {
		if backupRefAdded(c) {
			continue
		}
		unexpected = append(unexpected, c)
	}
	if len(unexpected) > 0 {
		t.Errorf("aborted apply changed the repository:\n  %s", strings.Join(unexpected, "\n  "))
	}
}

// AssertAppliedCleanly fails unless the only changes are the branch ref moving,
// regit bookkeeping refs appearing, and the caller's DECLARED changes. Each
// declared entry is matched as a substring against a change line, so a caller
// writes `AssertAppliedCleanly(t, before, after, "index removed: creds.env")`
// or just the path when either component may carry it.
//
// Declaring changes explicitly is the point: a test that passes a bare list of
// paths it expects to move will fail the moment an operation touches anything
// else, which is exactly how the purge/redact working-tree defect surfaces.
func AssertAppliedCleanly(t *testing.T, before, after Snapshot, declared ...string) {
	t.Helper()
	var unexpected []string
	for _, c := range before.Diff(after).Changes {
		if regitRefAdded(c) || strings.HasPrefix(c, "HEAD commit:") {
			continue
		}
		// The current branch moving is the whole point of an apply.
		if strings.HasPrefix(c, "ref changed: "+before.Head) {
			continue
		}
		if matchesAny(c, declared) {
			continue
		}
		unexpected = append(unexpected, c)
	}
	if len(unexpected) > 0 {
		t.Errorf("apply changed more than it declared:\n  %s\ndeclared: %v",
			strings.Join(unexpected, "\n  "), declared)
	}
}

// AssertUndoRestores fails unless after is identical to before apart from the
// regit bookkeeping refs both the apply and the undo accumulated. This proves
// reversibility is real rather than merely that a backup ref exists.
func AssertUndoRestores(t *testing.T, before, after Snapshot) {
	t.Helper()
	var unexpected []string
	for _, c := range before.Diff(after).Changes {
		if regitRefAdded(c) {
			continue
		}
		unexpected = append(unexpected, c)
	}
	if len(unexpected) > 0 {
		t.Errorf("undo did not restore the repository:\n  %s", strings.Join(unexpected, "\n  "))
	}
}

// matchesAny reports whether change contains any of the declared substrings.
func matchesAny(change string, declared []string) bool {
	for _, d := range declared {
		if d != "" && strings.Contains(change, d) {
			return true
		}
	}
	return false
}
