package gitio

import (
	"fmt"
	"os"
	"strings"

	"github.com/wlame/regit/internal/model"
)

// redactLines applies the spec's transform to one blob's content, returning
// the new content and whether anything changed. Content is treated as
// \n-separated; a line's COMPARED text excludes a trailing \r (CRLF files),
// but untouched lines keep their bytes exactly, and the file's final-newline
// presence is preserved. Lines are the unit of change (git's own minimal
// entity), so this is pure string surgery — no diff machinery.
func redactLines(content string, targets []string, mode model.RedactMode,
	rule *model.RedactRule) (string, bool, error) {
	want := make(map[string]bool, len(targets))
	for _, tgt := range targets {
		want[tgt] = true
	}
	// An empty file has no lines to act on at all (not one empty line).
	if content == "" {
		return content, false, nil
	}
	hadFinalNL := strings.HasSuffix(content, "\n")
	body := strings.TrimSuffix(content, "\n")
	lines := strings.Split(body, "\n")

	out := make([]string, 0, len(lines))
	changed := false
	for _, raw := range lines {
		cmp := strings.TrimSuffix(raw, "\r")
		if !want[cmp] {
			out = append(out, raw)
			continue
		}
		changed = true
		switch mode {
		case model.RedactRemove:
			// Drop the line: append nothing. Its preceding "\n" separator (or,
			// for the first line, nothing) goes with it via the final Join below.
		case model.RedactReplace:
			re, err := rule.Compile()
			if err != nil {
				return "", false, err
			}
			eol := raw[len(cmp):] // "" or "\r" — reattach after the rewrite
			out = append(out, re.ReplaceAllString(cmp, rule.Replacement)+eol)
		}
	}
	if !changed {
		return content, false, nil
	}
	res := strings.Join(out, "\n")
	if hadFinalNL && len(out) > 0 {
		res += "\n"
	}
	return res, true, nil
}

// RedactBlobsInTree returns the hash of tree with the spec's line edits
// applied. It follows RemovePathsFromTree's contract exactly: a private
// temporary index (the user's real index/worktree are never touched), and a
// guaranteed no-op — the INPUT tree hash comes back — when nothing matched,
// which ApplyRedact's drop-empty walk relies on. Symlinks, gitlinks, and
// binary blobs (NUL byte) are skipped: only regular text files are edited.
func (r *Repo) RedactBlobsInTree(tree string, spec model.RedactSpec) (string, error) {
	type edit struct{ path, oid, mode string }
	var edits []edit
	for path, targets := range spec.Targets {
		// ls-tree for the entry's mode + presence; a missing path is a no-op.
		// Output (verified against a real `git ls-tree -z`, od -c'd byte-for-byte):
		// "<mode> <type> <oid>\t<path>\0" — mode/type/oid are space-separated,
		// then a TAB, then the path, then the -z NUL terminator. strings.Fields
		// splits on any whitespace run (including the TAB), so fields[0] is
		// always the mode regardless of spaces in the path — we never need to
		// recover the path from this output, path is already the loop variable.
		entry, err := r.gitRaw("ls-tree", "-z", tree, "--", path)
		if err != nil {
			return "", fmt.Errorf("ls-tree %s:%s: %w", short(tree), path, err)
		}
		if entry == "" {
			continue // path absent from this tree: no-op
		}
		fields := strings.Fields(strings.SplitN(entry, "\x00", 2)[0])
		if len(fields) < 3 || (fields[0] != "100644" && fields[0] != "100755") {
			continue // symlink (120000), gitlink (160000), or dir: never line-edited
		}
		content, err := r.gitRaw("cat-file", "blob", tree+":"+path)
		if err != nil {
			return "", fmt.Errorf("cat-file %s:%s: %w", short(tree), path, err)
		}
		if strings.ContainsRune(content, '\x00') {
			continue // binary: the engine never targets these; skip defensively
		}
		newContent, changed, err := redactLines(content, targets, spec.Mode, &spec.Rule)
		if err != nil {
			return "", fmt.Errorf("redact %s: %w", path, err)
		}
		if !changed {
			continue
		}
		oid, err := r.gitWithEnvStdin(nil, newContent, "hash-object", "-w", "--stdin")
		if err != nil {
			return "", fmt.Errorf("hash-object %s: %w", path, err)
		}
		edits = append(edits, edit{path: path, oid: oid, mode: fields[0]})
	}
	if len(edits) == 0 {
		return tree, nil
	}

	idx, err := os.CreateTemp("", "regit-redact-index-*")
	if err != nil {
		return "", fmt.Errorf("redact index: %w", err)
	}
	idxPath := idx.Name()
	idx.Close()
	defer os.Remove(idxPath)
	os.Remove(idxPath) // read-tree wants to create the file itself

	env := []string{"GIT_INDEX_FILE=" + idxPath}
	if _, err := r.gitWithEnvStdin(env, "", "read-tree", tree); err != nil {
		return "", fmt.Errorf("read-tree %s: %w", short(tree), err)
	}
	for _, e := range edits {
		info := fmt.Sprintf("%s,%s,%s", e.mode, e.oid, e.path)
		if _, err := r.gitWithEnvStdin(env, "", "update-index", "--cacheinfo", info); err != nil {
			return "", fmt.Errorf("update-index %s: %w", e.path, err)
		}
	}
	newTree, err := r.gitWithEnvStdin(env, "", "write-tree")
	if err != nil {
		return "", fmt.Errorf("write-tree: %w", err)
	}
	return newTree, nil
}
