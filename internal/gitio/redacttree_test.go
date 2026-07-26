package gitio

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wlame/regit/internal/model"
)

func redactTestRepo(t *testing.T, content string) (*Repo, string) {
	t.Helper()
	r, dir := purgeTestRepo(t) // reuse the existing throwaway-repo helper
	writeAndCommit(t, r, dir, "cfg.yaml", content)
	return r, headTree(t, r)
}

func blobAt(t *testing.T, r *Repo, tree, path string) string {
	t.Helper()
	out, err := r.gitRaw("cat-file", "blob", tree+":"+path)
	if err != nil {
		t.Fatalf("cat-file: %v", err)
	}
	return out
}

func TestRedactBlobsInTree_removeDropsExactLine(t *testing.T) {
	r, tree := redactTestRepo(t, "a\ntoken: sk-123\nb\n")
	spec := model.RedactSpec{Match: "sk-123", Mode: model.RedactRemove,
		Targets: map[string][]string{"cfg.yaml": {"token: sk-123"}}}
	newTree, err := r.RedactBlobsInTree(tree, spec)
	if err != nil {
		t.Fatal(err)
	}
	if got := blobAt(t, r, newTree, "cfg.yaml"); got != "a\nb\n" {
		t.Fatalf("blob = %q, want %q", got, "a\nb\n")
	}
}

func TestRedactBlobsInTree_replaceAppliesRuleWithinLine(t *testing.T) {
	r, tree := redactTestRepo(t, "a\ntoken: sk-123\nb\n")
	spec := model.RedactSpec{Match: "sk-123", Mode: model.RedactReplace,
		Rule:    model.RedactRule{Pattern: `sk-\d+`, Replacement: "READONLY"},
		Targets: map[string][]string{"cfg.yaml": {"token: sk-123"}}}
	newTree, err := r.RedactBlobsInTree(tree, spec)
	if err != nil {
		t.Fatal(err)
	}
	if got := blobAt(t, r, newTree, "cfg.yaml"); got != "a\ntoken: READONLY\nb\n" {
		t.Fatalf("blob = %q", got)
	}
}

func TestRedactBlobsInTree_preservesCRLFAndNoFinalNewline(t *testing.T) {
	r, tree := redactTestRepo(t, "keep\r\ntoken: sk-123\r\nlast-no-eol")
	spec := model.RedactSpec{Match: "sk-123", Mode: model.RedactRemove,
		Targets: map[string][]string{"cfg.yaml": {"token: sk-123"}}}
	newTree, err := r.RedactBlobsInTree(tree, spec)
	if err != nil {
		t.Fatal(err)
	}
	if got := blobAt(t, r, newTree, "cfg.yaml"); got != "keep\r\nlast-no-eol" {
		t.Fatalf("blob = %q", got)
	}
}

// TestRedactBlobsInTree_replacePreservesCRLF exercises the eol reattachment
// branch inside RedactReplace mode ("\r" captured from cmp and stitched
// back onto the rewritten line), which no other test reaches: the existing
// CRLF test above uses RedactRemove, which never touches that branch at all.
func TestRedactBlobsInTree_replacePreservesCRLF(t *testing.T) {
	r, tree := redactTestRepo(t, "keep\r\ntoken: sk-123\r\nlast")
	spec := model.RedactSpec{Match: "sk-123", Mode: model.RedactReplace,
		Rule:    model.RedactRule{Pattern: `sk-\d+`, Replacement: "READONLY"},
		Targets: map[string][]string{"cfg.yaml": {"token: sk-123"}}}
	newTree, err := r.RedactBlobsInTree(tree, spec)
	if err != nil {
		t.Fatal(err)
	}
	if got := blobAt(t, r, newTree, "cfg.yaml"); got != "keep\r\ntoken: READONLY\r\nlast" {
		t.Fatalf("blob = %q", got)
	}
}

// TestRedactBlobsInTree_replaceNoActualMatchIsNoop: the target line's full
// content is selected via Targets, but the rule's pattern does not occur
// inside it, so ReplaceAllString reproduces the line byte-for-byte. This
// must NOT register as a change — a "redaction" that alters no bytes must
// return the untouched input tree, not a rehashed blob that looks redacted
// but never removed the secret.
func TestRedactBlobsInTree_replaceNoActualMatchIsNoop(t *testing.T) {
	r, tree := redactTestRepo(t, "a\ntoken: sk-123\nb\n")
	spec := model.RedactSpec{Match: "sk-123", Mode: model.RedactReplace,
		Rule:    model.RedactRule{Pattern: `zzz-\d+`, Replacement: "READONLY"},
		Targets: map[string][]string{"cfg.yaml": {"token: sk-123"}}}
	newTree, err := r.RedactBlobsInTree(tree, spec)
	if err != nil {
		t.Fatal(err)
	}
	if newTree != tree {
		t.Fatalf("a replace that matches nothing must be a no-op: %s != %s", newTree, tree)
	}
}

// TestRedactLines_replaceWithoutActualMatchReportsNoChange is the direct
// unit-level counterpart to the no-op test above, on the pure function
// itself rather than through RedactBlobsInTree's git plumbing. This is the
// test that actually distinguishes the fixed "changed" flag from the buggy
// one: at the RedactBlobsInTree/tree-hash level, a no-real-match replace
// happens to come back as a no-op EVEN with the pre-fix bug (git's blob
// store is content-addressed, so hash-object of byte-identical content
// always reproduces the same oid, and a from-scratch write-tree of an index
// whose only "edit" reasserts an unchanged entry reproduces the identical
// tree hash) — so a tree-hash assertion alone can't tell buggy from fixed.
// This test asserts the returned bool directly, which the bug got wrong
// unconditionally.
func TestRedactLines_replaceWithoutActualMatchReportsNoChange(t *testing.T) {
	content := "a\ntoken: sk-123\nb\n"
	rule := model.RedactRule{Pattern: `zzz-\d+`, Replacement: "READONLY"}
	got, changed, err := redactLines(content, []string{"token: sk-123"}, model.RedactReplace, &rule)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("changed must be false when the rule's pattern never matches within the target line")
	}
	if got != content {
		t.Fatalf("content = %q, want unchanged %q", got, content)
	}
}

func TestRedactBlobsInTree_removesAllDuplicateInstances(t *testing.T) {
	r, tree := redactTestRepo(t, "dup\nx\ndup\n")
	spec := model.RedactSpec{Match: "dup", Mode: model.RedactRemove,
		Targets: map[string][]string{"cfg.yaml": {"dup"}}}
	newTree, err := r.RedactBlobsInTree(tree, spec)
	if err != nil {
		t.Fatal(err)
	}
	if got := blobAt(t, r, newTree, "cfg.yaml"); got != "x\n" {
		t.Fatalf("blob = %q", got)
	}
}

func TestRedactBlobsInTree_noMatchAndMissingPathAreNoops(t *testing.T) {
	r, tree := redactTestRepo(t, "a\nb\n")
	spec := model.RedactSpec{Match: "zzz", Mode: model.RedactRemove,
		Targets: map[string][]string{"cfg.yaml": {"zzz"}, "gone.txt": {"zzz"}}}
	newTree, err := r.RedactBlobsInTree(tree, spec)
	if err != nil {
		t.Fatal(err)
	}
	if newTree != tree {
		t.Fatalf("no-op must return the input tree: %s != %s", newTree, tree)
	}
}

func TestRedactBlobsInTree_removingOnlyLineLeavesEmptyFile(t *testing.T) {
	r, tree := redactTestRepo(t, "token: sk-123\n")
	spec := model.RedactSpec{Match: "sk-123", Mode: model.RedactRemove,
		Targets: map[string][]string{"cfg.yaml": {"token: sk-123"}}}
	newTree, err := r.RedactBlobsInTree(tree, spec)
	if err != nil {
		t.Fatal(err)
	}
	if got := blobAt(t, r, newTree, "cfg.yaml"); got != "" {
		t.Fatalf("file should become empty, not deleted: %q", got)
	}
	if !strings.Contains(strings.Join(treePaths(t, r, newTree), " "), "cfg.yaml") {
		t.Fatal("cfg.yaml must remain present (empty), never deleted")
	}
}

// TestRedactBlobsInTree_symlinkEntryIsSkipped covers the contract's
// symlink-skip clause, which none of the brief's tests exercised: the
// symlink's target text happens to equal the target line so the only thing
// that can be saving it is the ls-tree mode (120000) check, not content.
func TestRedactBlobsInTree_symlinkEntryIsSkipped(t *testing.T) {
	r, dir := purgeTestRepo(t)
	if err := os.Symlink("token: sk-123", filepath.Join(dir, "link.yaml")); err != nil {
		t.Fatal(err)
	}
	gitT(t, dir, "add", "link.yaml")
	gitT(t, dir, "commit", "-q", "-m", "add symlink")
	tree := headTree(t, r)
	spec := model.RedactSpec{Match: "sk-123", Mode: model.RedactRemove,
		Targets: map[string][]string{"link.yaml": {"token: sk-123"}}}
	newTree, err := r.RedactBlobsInTree(tree, spec)
	if err != nil {
		t.Fatal(err)
	}
	if newTree != tree {
		t.Fatalf("symlink entry must be a no-op: %s != %s", newTree, tree)
	}
}

// TestRedactBlobsInTree_alreadyEmptyFileIsNoop covers redactLines' early
// return for a blob that is already 0 bytes (as opposed to becoming empty
// via redaction, covered above): splitting "" on "\n" yields one empty
// line, which must NOT be treated as an actual (matchable) line.
func TestRedactBlobsInTree_alreadyEmptyFileIsNoop(t *testing.T) {
	r, tree := redactTestRepo(t, "")
	spec := model.RedactSpec{Match: "sk-123", Mode: model.RedactRemove,
		Targets: map[string][]string{"cfg.yaml": {"token: sk-123"}}}
	newTree, err := r.RedactBlobsInTree(tree, spec)
	if err != nil {
		t.Fatal(err)
	}
	if newTree != tree {
		t.Fatalf("already-empty file must be a no-op: %s != %s", newTree, tree)
	}
}

// TestRedactBlobsInTree_errorsOnUnresolvableTree pins the ls-tree failure
// path: an unresolvable tree must propagate rather than read as "path
// absent" (which would silently no-op an entire redact).
func TestRedactBlobsInTree_errorsOnUnresolvableTree(t *testing.T) {
	r, _ := purgeTestRepo(t)
	spec := model.RedactSpec{Match: "sk-123", Mode: model.RedactRemove,
		Targets: map[string][]string{"cfg.yaml": {"token: sk-123"}}}
	_, err := r.RedactBlobsInTree("0000000000000000000000000000000000dead", spec)
	if err == nil {
		t.Fatal("RedactBlobsInTree on an unresolvable tree should fail, got nil error")
	}
}

// TestRedactBlobsInTree_binaryBlobIsSkipped pins the NUL-byte defensive skip:
// a target path whose blob contains a NUL byte (git's own "binary" signal,
// matching RemovePathsFromTree's convention) must be left untouched even
// though its content happens to also contain the literal spec targets.
func TestRedactBlobsInTree_binaryBlobIsSkipped(t *testing.T) {
	r, dir := purgeTestRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "bin.dat"),
		[]byte("before\x00token: sk-123\x00after"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, dir, "add", "bin.dat")
	gitT(t, dir, "commit", "-q", "-m", "add binary")
	tree := headTree(t, r)
	spec := model.RedactSpec{Match: "sk-123", Mode: model.RedactRemove,
		Targets: map[string][]string{"bin.dat": {"token: sk-123"}}}
	newTree, err := r.RedactBlobsInTree(tree, spec)
	if err != nil {
		t.Fatal(err)
	}
	if newTree != tree {
		t.Fatalf("a binary blob must be skipped defensively: %s != %s", newTree, tree)
	}
}

// TestRedactBlobsInTree_errorsOnBadReplaceRule pins the Compile-failure path
// (both redactLines' own return and RedactBlobsInTree's propagation of it): a
// spec.Rule with an empty Pattern is rejected by RedactSpec.Validate at the
// safety layer before ever reaching here, but RedactBlobsInTree itself does
// not re-validate — so a caller that skips validation must still fail
// closed, not silently leave the target line untouched.
func TestRedactBlobsInTree_errorsOnBadReplaceRule(t *testing.T) {
	r, tree := redactTestRepo(t, "a\ntoken: sk-123\nb\n")
	spec := model.RedactSpec{Match: "sk-123", Mode: model.RedactReplace,
		Rule:    model.RedactRule{Pattern: "", Replacement: "x"}, // invalid: empty pattern
		Targets: map[string][]string{"cfg.yaml": {"token: sk-123"}}}
	_, err := r.RedactBlobsInTree(tree, spec)
	if err == nil {
		t.Fatal("RedactBlobsInTree with an empty replace pattern should fail, got nil error")
	}
}

// TestRedactBlobsInTree_errorsOnDanglingBlob pins the cat-file failure path:
// a tree whose entry points at an oid that was never actually written to the
// object store (constructed here via a raw `mktree`, which does not itself
// validate that referenced blobs exist) must surface as an error rather than
// silently skip the path — the shape a partial fetch or corrupted object
// store can produce.
func TestRedactBlobsInTree_errorsOnDanglingBlob(t *testing.T) {
	r, _ := purgeTestRepo(t)
	// mktree validates the oid is a syntactically well-formed 40-char SHA-1
	// (unlike rev-parse, which merely treats an ill-formed one as "unknown
	// revision"), so this must be full-length even though it names no real
	// object in the store.
	danglingOid := "deaddeaddeaddeaddeaddeaddeaddeaddeaddead"
	entry := "100644 blob " + danglingOid + "\tcfg.yaml\n"
	// --missing: mktree otherwise refuses to build a tree entry whose object
	// isn't in the store at all, which is exactly the corrupted-store shape
	// this test needs to construct.
	tree, err := r.gitWithEnvStdin(nil, entry, "mktree", "--missing")
	if err != nil {
		t.Fatalf("mktree with a dangling blob oid: %v", err)
	}
	spec := model.RedactSpec{Match: "sk-123", Mode: model.RedactRemove,
		Targets: map[string][]string{"cfg.yaml": {"token: sk-123"}}}
	_, err = r.RedactBlobsInTree(tree, spec)
	if err == nil {
		t.Fatal("RedactBlobsInTree over a tree with a dangling blob oid should fail, got nil error")
	}
}
