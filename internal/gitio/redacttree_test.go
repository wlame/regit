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
