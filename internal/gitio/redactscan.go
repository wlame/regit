package gitio

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/wlame/regit/internal/model"
)

// SecretHit is one added line found by the pickaxe scan, with hunk context.
type SecretHit struct {
	Commit  string   // full hash of the commit whose diff ADDED the line
	Path    string   // path at that commit
	Line    string   // the added line's content, EOL excluded
	Context []string // up to 2 hunk lines either side, prefixed " ", "+", "-"
}

// hunkLine is one line inside a parsed unified-diff hunk body. op is git's
// leading marker byte: ' ' (context, unchanged), '+' (added), or '-' (removed).
type hunkLine struct {
	op   byte
	text string
}

// hunk is one "@@ -a,b +c,d @@" section of a SINGLE file's unified diff.
// header is stored verbatim, including any trailing "function context" text
// git appends after the second "@@" — callers never need to parse it, only
// the added/removed/context lines that follow.
type hunk struct {
	header string
	lines  []hunkLine
}

// parseHunks extracts the @@ hunks out of a unified-diff TEXT scoped to one
// file (the whole per-file section, or just its hunk bodies — both work).
// Everything before the first "@@ " line is skipped: the "diff --git"/mode/
// index/"--- "/"+++ " file-header block, and (when the caller passes
// `diff-tree`'s raw output before stripping it — see secretAdditionsInCommit)
// its leading bare commit-hash line. A line starting with '\' is git's
// "\ No newline at end of file" marker: it can appear MID-hunk (immediately
// after EITHER a '-' or a '+' line, whenever that side of the change lacks a
// trailing newline — verified against real git for a same-line rotation), so
// it must be skipped WITHOUT ending collection into the current hunk; a
// version that treated it as "end this hunk" silently dropped every '+' line
// that came after it, which is exactly where a rotated secret's new value
// lives. Only a line starting with none of ' ', '+', '-', '\' (a NEXT file's
// "diff --git" header, if a multi-file patch is ever passed whole) ends
// collection into the CURRENT hunk without discarding what was already
// gathered; only a fresh "@@ " line starts a new one. This is the ONE parser
// shared by SecretAdditions (-U2 output, added-line extraction with context)
// and LineDescendants (-U0 output, removed→added mapping) — see
// TestParseHunks for the exact shape it produces on a literal patch string.
func parseHunks(patch string) []hunk {
	var hunks []hunk
	var cur *hunk
	for _, line := range strings.Split(patch, "\n") {
		if strings.HasPrefix(line, "@@ ") {
			hunks = append(hunks, hunk{header: line})
			cur = &hunks[len(hunks)-1]
			continue
		}
		if cur == nil || line == "" {
			continue
		}
		switch line[0] {
		case ' ', '+', '-':
			cur.lines = append(cur.lines, hunkLine{op: line[0], text: line[1:]})
		case '\\':
			continue // "\ No newline at end of file": not hunk content, keep collecting
		default:
			cur = nil
		}
	}
	return hunks
}

// splitNUL splits the output of a `git ... -z` invocation into its tokens.
// -z NUL-terminates EVERY record (including the last), so a naive
// strings.Split leaves one trailing empty element; that (and the "no records
// at all" empty-string case) are the only two things this trims away.
func splitNUL(out string) []string {
	if out == "" {
		return nil
	}
	parts := strings.Split(out, "\x00")
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

// scanDiffFlags pins the diff output shape against a user's ambient git
// config, exactly like diffFlags in diff.go: --no-color/--no-ext-diff stop
// injected ANSI escapes or a configured third-party diff tool's own format
// from corrupting the patch text parseHunks/parsePaths below rely on, and the
// explicit src/dst prefixes stop a repo-level diff.noprefix=true from
// stripping the "a/"/"b/" markers parsePaths expects. Tests don't exercise
// this (gittest.Env pins config away from such settings), but a real user
// repository can have any of the three configured.
var scanDiffFlags = []string{"--no-color", "--no-ext-diff", "--src-prefix=a/", "--dst-prefix=b/"}

// secretContextRadius bounds how many hunk lines either side of a matched
// '+' line become that hit's Context. It happens to equal the -U2 width
// SecretAdditions requests from git, which is intentional: a match sitting
// right at a hunk's edge should still get the full radius the -U2 diff
// already captured, not be clipped further by a smaller radius here.
const secretContextRadius = 2

// SecretAdditions finds every commit that ever ADDED a line containing the
// literal match, on the current branch's history (globs, if non-empty,
// narrow both which commits count as hits and which files are inspected for
// them). -S is git's "pickaxe" search: it flags commits where the NUMBER of
// occurrences of match changed, in EITHER direction — so a hit commit's diff
// may only have REMOVED match (e.g. a later rotation); such commits
// naturally contribute zero SecretHits below, since only '+' lines are
// extracted. -S is deliberately never combined with --pickaxe-regex: match is
// a literal secret value, and a regex-special byte inside it (a literal "."
// in an API key, say) must never be reinterpreted as a wildcard. --no-merges
// on the log skips merge commits entirely — their redaction is guard-blocked
// one layer up regardless, so there is nothing useful to extract from them
// here. --no-show-signature matches read.go's convention: without it, a
// repo with log.showSignature=true injects gpg verification lines into the
// output, which (combined with -z's NUL-delimited records here) would
// corrupt the hash tokens this parses.
func (r *Repo) SecretAdditions(match string, globs []string) ([]SecretHit, error) {
	hashes, err := r.pickaxeCommitHashes(match, globs)
	if err != nil {
		return nil, err
	}

	var hits []SecretHit
	for _, hash := range hashes {
		commitHits, err := r.secretAdditionsInCommit(hash, match, globs)
		if err != nil {
			return nil, err
		}
		hits = append(hits, commitHits...)
	}
	return hits, nil
}

// pickaxeCommitHashes runs the `git log -S` search shared by SecretAdditions
// and SecretAdditionsBinary: every commit on HEAD's history (globs, if
// non-empty, narrow which commits count) whose diff changed how many times
// match occurs. Factored out so the two callers can never drift onto
// different commit sets for what is meant to be the same underlying scan.
func (r *Repo) pickaxeCommitHashes(match string, globs []string) ([]string, error) {
	args := []string{
		"log", "-z", "--no-show-signature", "--format=%H", "--no-merges", "-S" + match, "HEAD",
	}
	if len(globs) > 0 {
		args = append(args, "--")
		args = append(args, globs...)
	}
	out, err := r.gitRaw(args...)
	if err != nil {
		return nil, fmt.Errorf("pickaxe search %q: %w", match, err)
	}
	return splitNUL(out), nil
}

// secretAdditionsInCommit extracts every '+' line containing match from one
// pickaxe-hit commit's own diff. --root makes diff-tree show a ROOT commit's
// diff as a full addition against the empty tree (without it, diff-tree
// prints nothing at all for a parentless commit). --no-commit-id suppresses
// diff-tree's leading bare-hash line — parseHunks tolerates it either way
// (see its doc comment), but omitting it keeps splitSections' file-section
// split (reused from diff.go) from ever seeing a phantom hash-only "section"
// in the first place. The same globs used to narrow the pickaxe search are
// applied here too, so a hit commit that ALSO touches files outside globs in
// this same commit never contributes an out-of-scope hit.
func (r *Repo) secretAdditionsInCommit(hash, match string, globs []string) ([]SecretHit, error) {
	args := []string{"diff-tree", "--no-commit-id", "-p", "-U2", "--root"}
	args = append(args, scanDiffFlags...)
	args = append(args, hash)
	if len(globs) > 0 {
		args = append(args, "--")
		args = append(args, globs...)
	}
	patch, err := r.gitRaw(args...)
	if err != nil {
		return nil, fmt.Errorf("diff-tree %s: %w", short(hash), err)
	}

	var hits []SecretHit
	for _, sec := range splitSections(patch) {
		path, _ := parsePaths(sec)
		if path == "" {
			continue
		}
		for _, h := range parseHunks(sec) {
			for i, ln := range h.lines {
				if ln.op != '+' || !strings.Contains(ln.text, match) {
					continue
				}
				hits = append(hits, SecretHit{
					Commit:  hash,
					Path:    path,
					Line:    ln.text,
					Context: hunkContext(h.lines, i),
				})
			}
		}
	}
	return hits, nil
}

// hunkContext renders up to secretContextRadius hunkLines on EACH side of
// index i (clamped to the hunk's bounds, and never including line i itself —
// that line is already captured separately as the SecretHit's Line) back
// into their original "<op><text>" diff-line form, e.g. " context", "+added".
func hunkContext(lines []hunkLine, i int) []string {
	start := i - secretContextRadius
	if start < 0 {
		start = 0
	}
	end := i + secretContextRadius + 1
	if end > len(lines) {
		end = len(lines)
	}
	ctx := make([]string, 0, end-start)
	for j := start; j < end; j++ {
		if j == i {
			continue
		}
		ctx = append(ctx, string(lines[j].op)+lines[j].text)
	}
	return ctx
}

// BinaryHit is one "commit touched a binary blob containing match" pairing —
// SecretAdditionsBinary's counterpart to SecretHit, minus Line/Context: a
// binary blob has no text line to extract.
type BinaryHit struct {
	Commit string
	Path   string
}

// SecretAdditionsBinary complements SecretAdditions over the SAME pickaxe-hit
// commit set (pickaxeCommitHashes): git renders a binary file's change as
// "Binary files a/... and b/... differ" with no "@@ " hunks at all (verified
// against real git), so secretAdditionsInCommit's hunk parser silently sees
// ZERO added lines for such a commit — a secret hiding inside a binary blob
// would otherwise vanish from the scan without a trace. This checks each hit
// commit's own touched blobs directly (cat-file, not diff parsing) for match
// as a raw byte sequence, reporting only blobs that also contain a NUL byte
// (RedactBlobsInTree's own definition of "binary" — see its doc comment) so a
// TEXT hit that also happens to be pickaxe-flagged is never double-reported
// here.
func (r *Repo) SecretAdditionsBinary(match string, globs []string) ([]BinaryHit, error) {
	hashes, err := r.pickaxeCommitHashes(match, globs)
	if err != nil {
		return nil, err
	}

	var hits []BinaryHit
	for _, hash := range hashes {
		paths, err := r.binaryBlobsInCommit(hash, match, globs)
		if err != nil {
			return nil, err
		}
		for _, p := range paths {
			hits = append(hits, BinaryHit{Commit: hash, Path: p})
		}
	}
	return hits, nil
}

// binaryBlobsInCommit lists hash's own touched paths (--root makes this work
// for a root commit too, matching CommitTouchedFiles' convention; the same
// globs pathspec as the enclosing pickaxe search narrows which paths are
// even listed) and returns the ones whose blob AT hash both contains match
// and a NUL byte.
func (r *Repo) binaryBlobsInCommit(hash, match string, globs []string) ([]string, error) {
	args := []string{"diff-tree", "-r", "-z", "--name-only", "--no-commit-id", "--root", hash}
	if len(globs) > 0 {
		args = append(args, "--")
		args = append(args, globs...)
	}
	out, err := r.gitRaw(args...)
	if err != nil {
		return nil, fmt.Errorf("diff-tree name-only %s: %w", short(hash), err)
	}

	var paths []string
	for _, path := range splitNUL(out) {
		content, err := r.gitRaw("cat-file", "blob", hash+":"+path)
		if err != nil {
			continue // path deleted at hash (no blob to inspect): not a hit
		}
		if strings.Contains(content, match) && strings.ContainsRune(content, '\x00') {
			paths = append(paths, path)
		}
	}
	return paths, nil
}

// forwardFirstParentChain returns the commits from fromCommit to HEAD
// (inclusive of both ends) along the current branch's FIRST-PARENT lineage,
// oldest first. git has no native "children" pointer (only parent links), so
// this is computed as a REVERSE rev-list walk from HEAD, excluding everything
// reachable from fromCommit: `rev-list --first-parent --reverse
// fromCommit..HEAD` — valid precisely because LineDescendants' guard (see its
// call to isOnFirstParentChain) already confirmed fromCommit IS on HEAD's
// first-parent ancestry before this runs. No -z here: the output is a plain
// hash-per-line list (no paths, nothing that needs NUL-safety), and `-z` on
// `rev-list` needs a newer git than every other rev-list call in this repo
// assumes — plain output plus whitespace splitting, like FirstParents in
// read.go, works on any supported git version.
func (r *Repo) forwardFirstParentChain(fromCommit string) ([]string, error) {
	out, err := r.git("rev-list", "--first-parent", "--reverse", fromCommit+"..HEAD")
	if err != nil {
		return nil, fmt.Errorf("forward chain from %s: %w", short(fromCommit), err)
	}
	chain := []string{fromCommit}
	if out != "" {
		chain = append(chain, strings.Fields(out)...)
	}
	return chain, nil
}

// findPathSection scans a multi-file diff's per-file sections (as split by
// splitSections in diff.go) for the one currently tracking trackPath — either
// an ordinary edit at that exact path, or a rename whose OLD side is
// trackPath. Returns the matching section's raw text, the path to track
// going forward (the renamed target, or trackPath unchanged), and whether a
// section was found at all: a commit pair that never touched trackPath has
// no match, and the caller keeps tracking the same path/line untouched.
func findPathSection(patch, trackPath string) (section, newPath string, found bool) {
	for _, sec := range splitSections(patch) {
		file, oldPath := parsePaths(sec)
		if oldPath == trackPath || (oldPath == "" && file == trackPath) {
			return sec, file, true
		}
	}
	return "", "", false
}

// findRemovedLines returns every hunk (by value — hunk is small) whose
// removed ('-') lines include trackLine. Ordinarily this is 0 or 1 hunks; a
// SECOND match means trackLine's exact text occurs more than once in the
// file and BOTH occurrences changed in this commit pair — LineDescendants
// treats that as ambiguous (see its doc comment) rather than guessing which
// hunk's fate belongs to the occurrence being tracked. A hunk containing
// trackLine as a removed line more than once (duplicated within itself) only
// counts once here — the ambiguity that matters is "which hunk", not "how
// many times within one hunk".
func findRemovedLines(hunks []hunk, trackLine string) []hunk {
	var matches []hunk
	for _, h := range hunks {
		for _, ln := range h.lines {
			if ln.op == '-' && ln.text == trackLine {
				matches = append(matches, h)
				break
			}
		}
	}
	return matches
}

// countOp counts how many lines in a hunk carry the given op byte.
func countOp(lines []hunkLine, op byte) int {
	n := 0
	for _, ln := range lines {
		if ln.op == op {
			n++
		}
	}
	return n
}

// firstOp returns the text of the first line in a hunk carrying the given op
// byte, or "" if none do.
func firstOp(lines []hunkLine, op byte) string {
	for _, ln := range lines {
		if ln.op == op {
			return ln.text
		}
	}
	return ""
}

// isOnFirstParentChain reports whether commit appears somewhere along HEAD's
// first-parent lineage (a "primary line" ancestor reached by following ONLY
// first-parent links from HEAD) rather than merely being reachable from HEAD
// by some path. The distinction matters: a commit on a side branch that was
// merged into HEAD by an ordinary (non-first-parent) merge IS reachable from
// HEAD — `git merge-base --is-ancestor` would say yes — but is NOT on the
// chain LineDescendants walks, and there is no dedicated git flag for "is
// this specifically a first-parent ancestor" (`git merge-base` has no
// --first-parent option, verified against real git). So this does the only
// fully correct check: read HEAD's whole first-parent chain and look for an
// exact match. One `git rev-list` process is cheap enough for a guard that
// runs once per LineDescendants call, even over a large linear history.
func (r *Repo) isOnFirstParentChain(commit string) (bool, error) {
	out, err := r.git("rev-list", "--first-parent", "HEAD")
	if err != nil {
		return false, fmt.Errorf("read first-parent chain: %w", err)
	}
	for _, h := range strings.Fields(out) {
		if h == commit {
			return true, nil
		}
	}
	return false, nil
}

// ErrLineOffFirstParentChain is the sentinel wrapped into the error
// LineDescendants returns when fromCommit is not on HEAD's first-parent
// chain. A caller that walks all parents, such as a redact scan built on
// SecretAdditions, may legitimately see such a commit; it checks this with
// errors.Is to tell "this one occurrence cannot be cascaded" apart from
// every other failure mode (a broken repo, a bad ref), which must still
// abort outright rather than being swallowed alongside it.
var ErrLineOffFirstParentChain = errors.New(
	"commit is not on HEAD's first-parent chain")

// LineDescendants walks path's tracked line FORWARD from fromCommit along the
// current branch's first-parent chain to HEAD, returning every later state
// the line took (oldest first) and a note explaining why the chain ended
// early (empty when it simply ran a rotation-free course to a deletion or to
// HEAD unedited).
//
// fromCommit MUST be on HEAD's first-parent chain: SecretAdditions (the
// usual source of a fromCommit) walks ALL parents, not just first-parent, so
// a SecretHit can legitimately name a side-branch commit that was merged in
// — feeding that straight into the rev-list range below would silently
// compute a nonsensical "forward chain", not an error, so it is checked
// explicitly up front via isOnFirstParentChain and reported via
// ErrLineOffFirstParentChain (see its doc for why that's a sentinel).
//
// The chain is stepped one commit pair at a time. Each pair's diff uses -U0
// (zero context: the cheapest way to see EXACTLY which lines were added or
// removed, with no risk of an unrelated context line being mistaken for a
// match) and -M (rename detection). Crucially the diff is NOT restricted with
// a `-- path` pathspec: verified against real git that limiting the diff to
// the OLD path alone defeats -M's rename pairing entirely — git has nothing
// on the "new" side of the pathspec-filtered diff to compare against, so it
// reports a plain file deletion instead of a rename (see
// TestLineDescendants_rotationAfterRenameTracksNewPath, which — unlike
// TestLineDescendants_followsRotationRenameAndDeletion's rename-then-delete
// shape — actually distinguishes a correct rename-follow from that
// regression). So every pair diffs the WHOLE tree, and findPathSection below
// searches its per-file sections for the one currently tracking our path.
func (r *Repo) LineDescendants(fromCommit, path, line string) (
	[]model.RedactVersion, string, error) {
	onChain, err := r.isOnFirstParentChain(fromCommit)
	if err != nil {
		return nil, "", err
	}
	if !onChain {
		return nil, "", fmt.Errorf(
			"commit %s is not on HEAD's first-parent chain: LineDescendants can only "+
				"track a line forward along the primary branch line: %w",
			short(fromCommit), ErrLineOffFirstParentChain)
	}

	chain, err := r.forwardFirstParentChain(fromCommit)
	if err != nil {
		return nil, "", err
	}

	var descendants []model.RedactVersion
	trackPath, trackLine := path, line
	for i := 0; i+1 < len(chain); i++ {
		a, b := chain[i], chain[i+1]
		args := append([]string{"diff", "-U0", "-M"}, scanDiffFlags...)
		args = append(args, a, b)
		patch, err := r.gitRaw(args...)
		if err != nil {
			return nil, "", fmt.Errorf("diff %s..%s: %w", short(a), short(b), err)
		}

		sec, newPath, found := findPathSection(patch, trackPath)
		if !found {
			continue // trackPath untouched by this step: keep tracking as-is
		}
		trackPath = newPath

		matches := findRemovedLines(parseHunks(sec), trackLine)
		switch len(matches) {
		case 0:
			// The path changed (a rename, or unrelated lines in it) but our
			// tracked line itself was never removed: it survives unchanged.
			continue
		case 1:
			// handled below
		default:
			note := fmt.Sprintf(
				"line's content occurs more than once and changed in multiple hunks of %s; "+
					"review that commit manually", short(b))
			return descendants, note, nil
		}

		h := matches[0]
		switch added := countOp(h.lines, '+'); added {
		case 0:
			return descendants, "", nil // deleted: chain ends, no note
		case 1:
			trackLine = firstOp(h.lines, '+')
			descendants = append(descendants, model.RedactVersion{
				Commit: b, Path: trackPath, Line: trackLine,
			})
		default:
			note := fmt.Sprintf(
				"line rewritten as a multi-line change in %s; review that commit manually", short(b))
			return descendants, note, nil
		}
	}
	return descendants, "", nil
}

// GrepTree reports whether literal occurs anywhere in tree (a tree-ish: a
// commit, branch, or raw tree object all work), via git's own fixed-string
// search rather than us reading and scanning every blob ourselves.
//
// -F makes the search a literal substring match, never a regex — literal is
// a secret value and may contain regex-special bytes. -e (rather than a bare
// trailing positional argument) is required: verified against real git that
// a literal starting with '-' (plausible for a token/API-key alphabet) is
// otherwise parsed as an unknown git-grep FLAG and fails with a usage error
// (exit 129), not the "absent" exit code 1 an unlucky caller might assume —
// see TestGrepTree_dashPrefixedLiteralDoesNotBreakParsing.
//
// --quiet suppresses all match output; only the exit code matters: 0 = found,
// 1 = absent (both success outcomes, NOT an error). Anything else (an
// unresolvable tree, a real git failure) is a genuine error and must
// propagate, so the absence check this backs degrades CLOSED rather than
// silently reporting "absent" for what was actually a broken invocation.
func (r *Repo) GrepTree(tree, literal string) (bool, error) {
	_, err := r.git("grep", "-F", "--quiet", "-e", literal, tree)
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("grep tree %s: %w", short(tree), err)
}

// BlobContent returns path's raw blob content at tree (a commit-ish or a raw
// tree both work, same as GrepTree). ok is false when path is absent from
// tree — a normal outcome (e.g. a path a commit never carried), not an
// error — mirroring RedactBlobsInTree's own ls-tree presence check.
//
// This is a deliberately RAW read: no line-splitting, no redaction logic, no
// git-grep pattern matching — just the exact bytes git has stored. It exists
// so a caller verifying a rewrite's OUTCOME (see safety.Manager's
// redactSurvivingLine) can compare against the real content directly, rather
// than through RedactBlobsInTree/redactLines — the SAME matching logic that
// performed the rewrite, and so a bug there could otherwise hide itself from
// a check meant to catch exactly that kind of bug.
func (r *Repo) BlobContent(tree, path string) (content string, ok bool, err error) {
	entry, err := r.gitRaw("ls-tree", "-z", tree, "--", path)
	if err != nil {
		return "", false, fmt.Errorf("ls-tree %s:%s: %w", short(tree), path, err)
	}
	if entry == "" {
		return "", false, nil
	}
	content, err = r.gitRaw("cat-file", "blob", tree+":"+path)
	if err != nil {
		return "", false, fmt.Errorf("cat-file %s:%s: %w", short(tree), path, err)
	}
	return content, true, nil
}

// SecretAddedFrom returns every commit hash reachable from rev that ever
// changed the occurrence count of match — the backbone of the absence check:
// an EMPTY result means the literal was never added anywhere in rev's
// history, so there is nothing left to guard against. --full-history turns
// off git's default history simplification (which would otherwise skip
// commits whose change to match's occurrence count gets "simplified away" on
// a merged-in side branch — the same blind spot PurgeBase/PathsReachableFrom
// close for path-based purge; see purge_test.go's purgeMergeInPrefixRepo).
// Output is a plain %H-per-line list (no paths, no message body), so unlike
// the -z-delimited commands elsewhere in this file, ordinary whitespace
// splitting is safe here — the same choice FirstParents makes for the same
// reason. --no-show-signature is the same read.go convention as
// SecretAdditions above: without it, a signed-commit repo with
// log.showSignature=true would inject gpg lines into the hash list.
func (r *Repo) SecretAddedFrom(rev, match string) ([]string, error) {
	out, err := r.git("log", "--no-show-signature", "--format=%H", "--full-history", "-S"+match, rev)
	if err != nil {
		return nil, fmt.Errorf("secret history search %q: %w", match, err)
	}
	if out == "" {
		return nil, nil
	}
	return strings.Fields(out), nil
}

// SecretInSideHistory reports whether match was ever added or removed by a
// commit reachable from HEAD but NOT on commits' first-parent chain (a "side
// branch" commit) — the content analog of PathsInSideHistory, built directly
// on SecretAddedFrom (whose --full-history walk from HEAD already visits
// every reachable commit, side branches included) instead of a
// pathspec-limited rev-list. commits is the FULL first-parent history
// (root-first, as ReadCommits returns it).
//
// regit only ever rewrites the first-parent chain (see ApplyRedact), so a
// literal with side-branch history can never be fully redacted from HEAD's
// reachable history: the text stays reachable via the side branch's own,
// untouched commits no matter what happens to commits itself. Unlike purge's
// PathsInSideHistory — which BLOCKS the rewrite up front — this is advisory
// only: safety.Manager.ApplyRedact never calls it (its own doc comment
// explains why the two operations differ here); it exists for the redact
// verb/TUI layer to report a warning alongside an otherwise-successful
// rewrite.
func (r *Repo) SecretInSideHistory(commits []model.Commit, match string) (bool, error) {
	onChain := make(map[string]bool, len(commits))
	for _, c := range commits {
		onChain[c.Hash] = true
	}
	hashes, err := r.SecretAddedFrom("HEAD", match)
	if err != nil {
		return false, fmt.Errorf("side-history secret search: %w", err)
	}
	for _, h := range hashes {
		if !onChain[h] {
			return true, nil
		}
	}
	return false, nil
}
