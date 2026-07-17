package gitio

import (
	"fmt"
	"os"
	"strings"

	"github.com/wlame/regit/internal/model"
)

// redactLines applies the spec's transform to one blob's content, returning
// the new content and whether the content's BYTES actually differ from the
// input — not merely whether a target line was matched. A RedactReplace
// whose pattern doesn't occur within a matched line reproduces that line
// byte-for-byte and must report false here: the caller uses this signal to
// decide whether a line was genuinely redacted, and a false "yes" here would
// wrongly tell it (or a future reporting/verification layer) a secret was
// removed when the rule never actually touched it. A RedactRemove match is
// always a change: the line is gone. Content is treated as \n-separated; a
// line's COMPARED text excludes a trailing \r (CRLF files), but untouched
// lines keep their bytes exactly, and the file's final-newline presence is
// preserved. Lines are the unit of change (git's own minimal entity), so
// this is pure string surgery — no diff machinery.
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
		switch mode {
		case model.RedactRemove:
			// Drop the line: append nothing. Its preceding "\n" separator (or,
			// for the first line, nothing) goes with it via the final Join below.
			// A vanished line is always a change.
			changed = true
		case model.RedactReplace:
			re, err := rule.Compile()
			if err != nil {
				return "", false, err
			}
			eol := raw[len(cmp):] // "" or "\r" — reattach after the rewrite
			newLine := re.ReplaceAllString(cmp, rule.Replacement) + eol
			out = append(out, newLine)
			// A target line whose content doesn't actually contain rule.Pattern
			// comes back byte-identical to raw; that must NOT register as a
			// change — see the function doc for why the signal matters even
			// though git's content-addressed blob store means an accidental
			// rehash of identical bytes is otherwise harmless on its own.
			changed = changed || newLine != raw
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
		// Three separate args (not the comma-joined "<mode>,<oid>,<path>" form):
		// a path containing a literal comma would otherwise be ambiguous.
		if _, err := r.gitWithEnvStdin(env, "", "update-index", "--cacheinfo",
			e.mode, e.oid, e.path); err != nil {
			return "", fmt.Errorf("update-index %s: %w", e.path, err)
		}
	}
	newTree, err := r.gitWithEnvStdin(env, "", "write-tree")
	if err != nil {
		return "", fmt.Errorf("write-tree: %w", err)
	}
	return newTree, nil
}
