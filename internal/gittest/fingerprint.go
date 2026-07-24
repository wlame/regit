package gittest

import (
	"bytes"
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
// safety.redactSurvivingLine). internal/gittest imports no other internal package.
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
		// An unresolved merge emits multiple stage lines (1/2/3) for the same
		// path; this keeps only the last one written. That's acceptable because
		// every regit apply is gated on a clean tree, and a conflicted merge is
		// dirty, so this oracle never has to represent multi-stage entries.
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
		if info.Mode()&os.ModeSymlink != 0 {
			// Hash the LINK TARGET, not the file it points at: os.Lstat reports
			// the link's own mode while a content read would follow the link, so
			// re-pointing a symlink at different-but-byte-identical content would
			// otherwise go undetected. Readlink reads the link itself, so this
			// works even for a link that points nowhere.
			target, err := os.Readlink(full)
			if err != nil {
				continue
			}
			s.Worktree[path] = fmt.Sprintf("symlink %x", sha256.Sum256([]byte(target)))
			continue
		}
		content, err := os.ReadFile(full)
		if err != nil {
			continue // unreadable (e.g. permission denied): skip rather than fail
		}
		s.Worktree[path] = fmt.Sprintf("%o %x", info.Mode().Perm(), sha256.Sum256(content))
	}
	return s
}

// snapGit runs one git command under the hermetic Env and returns stdout.
// stderr is captured separately (never merged into the returned stdout, which
// callers parse) and folded into the error so a Snap failure names git's
// actual complaint instead of a bare "exit status 1".
func snapGit(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	var stderr bytes.Buffer
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = Env(dir)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return string(out), fmt.Errorf("%w: %s", err, msg)
		}
	}
	return string(out), err
}

// Change is one difference between two Snapshots, kept STRUCTURED rather than
// pre-rendered so a declared change can be matched exactly. The earlier
// substring form let a declaration of "index removed: junk.log" also swallow an
// undeclared "index removed: junk.log.bak" — precisely the class of silent miss
// this oracle exists to prevent.
type Change struct {
	Kind   string // "head-symref", "head-commit", "ref", "index", "worktree"
	Op     string // "added", "removed", "changed"
	Path   string // ref name or file path; empty for the head kinds
	Detail string // before -> after; for the failure message only, never matched
}

// String renders one change for a test failure message.
func (c Change) String() string {
	switch {
	case c.Path == "":
		return fmt.Sprintf("%s %s: %s", c.Kind, c.Op, c.Detail)
	case c.Detail == "":
		return fmt.Sprintf("%s %s: %s", c.Kind, c.Op, c.Path)
	default:
		return fmt.Sprintf("%s %s: %s (%s)", c.Kind, c.Op, c.Path, c.Detail)
	}
}

// Declared names one change an operation promises to make. It is matched
// against a Change by Kind+Op+Path exactly — never by substring, so a sibling
// path that merely shares a prefix can never be absorbed by a declaration.
type Declared struct {
	Kind string // "index" or "worktree"
	Op   string // "added", "removed", "changed"
	Path string
}

func (d Declared) matches(c Change) bool {
	return c.Kind == d.Kind && c.Op == d.Op && c.Path == d.Path
}

// Delta is the ordered difference between two Snapshots.
type Delta struct{ Changes []Change }

// IsEmpty reports whether the two snapshots were identical.
func (d Delta) IsEmpty() bool { return len(d.Changes) == 0 }

// String renders one change per line, indented for a t.Errorf block.
func (d Delta) String() string {
	if d.IsEmpty() {
		return "(no changes)"
	}
	lines := make([]string, len(d.Changes))
	for i, c := range d.Changes {
		lines[i] = c.String()
	}
	return "\n  " + strings.Join(lines, "\n  ")
}

// Diff reports every way other differs from s, sorted for deterministic output.
func (s Snapshot) Diff(other Snapshot) Delta {
	var out []Change
	if s.Head != other.Head {
		out = append(out, Change{Kind: "head-symref", Op: "changed",
			Detail: s.Head + " -> " + other.Head})
	}
	if s.HeadHash != other.HeadHash {
		out = append(out, Change{Kind: "head-commit", Op: "changed",
			Detail: short(s.HeadHash) + " -> " + short(other.HeadHash)})
	}
	out = append(out, diffMaps("ref", s.Refs, other.Refs)...)
	out = append(out, diffMaps("index", s.Index, other.Index)...)
	out = append(out, diffMaps("worktree", s.Worktree, other.Worktree)...)
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return Delta{Changes: out}
}

// diffMaps reports added / removed / changed entries between two keyed maps.
func diffMaps(kind string, before, after map[string]string) []Change {
	var out []Change
	for k, bv := range before {
		av, ok := after[k]
		if !ok {
			out = append(out, Change{Kind: kind, Op: "removed", Path: k})
			continue
		}
		if av != bv {
			out = append(out, Change{Kind: kind, Op: "changed", Path: k,
				Detail: short(bv) + " -> " + short(av)})
		}
	}
	for k := range after {
		if _, ok := before[k]; !ok {
			out = append(out, Change{Kind: kind, Op: "added", Path: k})
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

// isBackupRefAdded reports whether c is the one residue an aborted apply may
// leave: a newly created refs/regit/backup/* ref.
func isBackupRefAdded(c Change) bool {
	return c.Kind == "ref" && c.Op == "added" && strings.HasPrefix(c.Path, "refs/regit/backup/")
}

// isRegitRefAdded reports whether c is any newly created regit bookkeeping ref
// (backup or applied), which a SUCCESSFUL apply creates.
func isRegitRefAdded(c Change) bool {
	return c.Kind == "ref" && c.Op == "added" && strings.HasPrefix(c.Path, "refs/regit/")
}

// testingT is the subset of *testing.T the assertions use. Taking an interface
// rather than the concrete type is what lets the oracle's OWN tests verify that
// a violation is actually reported — a helper that silently never fails would
// otherwise be indistinguishable from one that works.
type testingT interface {
	Helper()
	Errorf(format string, args ...any)
}

// AssertAbortedCleanly fails unless the repository is byte-identical apart from
// an added backup ref. This is the strongest claim regit makes about failure:
// a guard, verification, or structural abort must leave HEAD, every branch and
// tag, the index, and every working-tree file exactly as they were.
//
// An aborted apply never records an applied ref — safety.runApply calls
// RecordAppliedHead only after the compare-and-swap succeeds — so only backup
// refs are tolerated here.
func AssertAbortedCleanly(t testingT, before, after Snapshot) {
	t.Helper()
	var unexpected []Change
	for _, c := range before.Diff(after).Changes {
		if isBackupRefAdded(c) {
			continue
		}
		unexpected = append(unexpected, c)
	}
	if len(unexpected) > 0 {
		t.Errorf("aborted apply changed the repository:%s", Delta{Changes: unexpected})
	}
}

// AssertAppliedCleanly fails unless the only changes are the branch ref moving,
// regit bookkeeping refs appearing, and the caller's DECLARED changes. Each
// declared entry is matched against a Change by Kind+Op+Path exactly, so a
// caller writes AssertAppliedCleanly(t, before, after,
// Declared{Kind: "index", Op: "removed", Path: "creds.env"}) and only that
// precise change is waived — a sibling path sharing a prefix or substring can
// never be absorbed by it.
//
// Declaring changes explicitly is the point: a test that passes a bare list of
// paths it expects to move will fail the moment an operation touches anything
// else, which is exactly how the purge/redact working-tree defect surfaces.
//
// A declaration is also a PROMISE, not merely a permission: this assertion
// additionally fails if any declared change never shows up in the delta. A
// delta-only check can only ever flag changes that DID happen, so it is
// structurally blind to an absence — an operation that silently fails to make
// a change it should have (e.g. moving a branch ref without refreshing the
// index to match) produces no Change for that path at all, and a purely
// negative assertion has nothing to flag. Requiring every declaration to fire
// turns the oracle from "did you do anything you didn't say" into "did you do
// everything you said," which is what makes it capable of catching a
// working-tree-sync defect like regit purge leaving a removed file staged.
func AssertAppliedCleanly(t testingT, before, after Snapshot, declared ...Declared) {
	t.Helper()
	fulfilled := make([]bool, len(declared))
	var unexpected []Change
	for _, c := range before.Diff(after).Changes {
		// regit bookkeeping refs are the expected residue of a successful apply.
		if isRegitRefAdded(c) {
			continue
		}
		// safety.runApply requires CurrentBranch() to succeed before any apply
		// runs, so a detached-HEAD apply never reaches these assertions; the HEAD
		// commit therefore necessarily moves together with the branch ref that
		// the next waiver licenses, which makes this skip unconditionally safe.
		if c.Kind == "head-commit" {
			continue
		}
		// The current branch moving is the whole point of an apply.
		if c.Kind == "ref" && c.Op == "changed" && c.Path == before.Head {
			continue
		}
		if markMatchingDeclared(c, declared, fulfilled) {
			continue
		}
		unexpected = append(unexpected, c)
	}

	var unfulfilled []Declared
	for i, d := range declared {
		if !fulfilled[i] {
			unfulfilled = append(unfulfilled, d)
		}
	}

	// Both checks run to completion and both are reported: short-circuiting
	// after the first would let a test with one of each failure learn about
	// only whichever check happened to run first.
	if len(unexpected) > 0 {
		t.Errorf("apply changed more than it declared:%s\ndeclared: %v",
			Delta{Changes: unexpected}, declared)
	}
	if len(unfulfilled) > 0 {
		t.Errorf("apply promised %d declared change(s) that never happened:%s",
			len(unfulfilled), declaredList(unfulfilled))
	}
}

// markMatchingDeclared marks every entry in declared that matches c as
// fulfilled (there is normally at most one, since Kind+Op+Path is unique
// within one Diff, but a caller passing a duplicate declaration should not
// leave one copy looking unfulfilled) and reports whether any matched.
func markMatchingDeclared(c Change, declared []Declared, fulfilled []bool) bool {
	matched := false
	for i, d := range declared {
		if d.matches(c) {
			fulfilled[i] = true
			matched = true
		}
	}
	return matched
}

// declaredList renders unfulfilled declarations for a failure message, one per
// line in the same style as Delta.String, so each promised-but-absent change
// is individually named rather than dumped as an opaque struct slice.
func declaredList(declared []Declared) string {
	lines := make([]string, len(declared))
	for i, d := range declared {
		lines[i] = fmt.Sprintf("%s %s: %s", d.Kind, d.Op, d.Path)
	}
	return "\n  " + strings.Join(lines, "\n  ")
}

// AssertUndoRestores fails unless after is identical to before apart from the
// regit bookkeeping refs both the apply and the undo accumulated. This proves
// reversibility is real rather than merely that a backup ref exists.
func AssertUndoRestores(t testingT, before, after Snapshot) {
	t.Helper()
	var unexpected []Change
	for _, c := range before.Diff(after).Changes {
		if isRegitRefAdded(c) {
			continue
		}
		unexpected = append(unexpected, c)
	}
	if len(unexpected) > 0 {
		t.Errorf("undo did not restore the repository:%s", Delta{Changes: unexpected})
	}
}
