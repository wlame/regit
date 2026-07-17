// Package redact implements the redact feature's engine: scanning history for
// a literal secret and turning the operator's selection into a validated
// model.RedactSpec. It is the redact analogue of internal/split and
// internal/squash — a thin engine wrapping gitio's git primitives, never
// talking to git itself (see gitio's package doc) and never importing
// internal/ui.
package redact

import (
	"errors"
	"fmt"

	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/model"
)

// Engine scans and plans redactions against one repository.
type Engine struct {
	Repo *gitio.Repo
}

// ScanOptions configures Scan: which literal to search for, which paths to
// narrow the search to, and whether to follow each hit's line forward
// through later history (rotations, renames) as part of the report.
type ScanOptions struct {
	Match   string
	Globs   []string
	Cascade bool // false = --no-cascade
}

// ScanResult is everything Scan found: redactable text occurrences, plus a
// separate report of pickaxe hits that live inside binary blobs — never
// selectable, since there is no text line to act on.
type ScanResult struct {
	Occurrences []model.RedactOccurrence
	Binary      []string // "path @ short-hash"
}

// introKey is the (Intro.Commit, Path, Line) triple Scan dedups occurrences
// on: the same pickaxe-hit commit can add the identical line to the same
// path twice (e.g. two hunks touching one file), which must collapse to a
// single occurrence — a hit at a different path, or with different line
// content, is a genuinely distinct occurrence and must survive.
type introKey struct {
	commit, path, line string
}

// Scan finds every commit that ever introduced opts.Match, follows each hit
// forward through later history when opts.Cascade is set, and separately
// reports pickaxe hits that live inside binary blobs.
func (e Engine) Scan(opts ScanOptions) (ScanResult, error) {
	hits, err := e.Repo.SecretAdditions(opts.Match, opts.Globs)
	if err != nil {
		return ScanResult{}, err
	}

	var res ScanResult
	seen := make(map[introKey]bool, len(hits))
	for _, hit := range hits {
		key := introKey{commit: hit.Commit, path: hit.Path, line: hit.Line}
		if seen[key] {
			continue
		}
		seen[key] = true

		occ := model.RedactOccurrence{
			Intro: model.RedactVersion{
				Commit: hit.Commit,
				Path:   hit.Path,
				Line:   hit.Line,
			},
			Context: hit.Context,
		}
		if opts.Cascade {
			desc, note, err := e.cascade(hit)
			if err != nil {
				return ScanResult{}, err
			}
			occ.Descendants = desc
			occ.ChainNote = note
		}
		res.Occurrences = append(res.Occurrences, occ)
	}

	binHits, err := e.Repo.SecretAdditionsBinary(opts.Match, opts.Globs)
	if err != nil {
		return ScanResult{}, err
	}
	for _, b := range binHits {
		res.Binary = append(res.Binary, fmt.Sprintf("%s @ %s", b.Path, shortHash(b.Commit)))
	}

	return res, nil
}

// cascade wraps LineDescendants to absorb its ONE expected failure mode: a
// SecretHit whose commit sits off HEAD's first-parent chain. SecretAdditions
// walks ALL parents (not just first-parent), so a hit legitimately can name
// such a commit — e.g. a secret introduced on a side branch that was later
// merged into HEAD by an ordinary, non-first-parent merge. LineDescendants
// only ever tracks a line forward along HEAD's first-parent chain, so a hit
// that starts off that chain has no well-defined "next commit" to step to,
// and it reports that via gitio.ErrLineOffFirstParentChain. Rather than
// aborting the whole Scan over one untrackable hit, that ONE occurrence is
// reported with no descendants and a ChainNote explaining why: it is still a
// real, redactable occurrence — its own commit's tree still holds the
// secret, and gitio.RedactBase finds targets by tree CONTENT (starting from
// wherever that content first appears on the first-parent chain, e.g. the
// merge commit), not by requiring this exact commit hash to be on that chain
// — it just cannot be cascaded forward, since there is no well-defined "next
// state" to walk from off-chain. Any OTHER error from LineDescendants is a
// genuine failure (a broken repo, a bad ref) and still aborts the scan.
func (e Engine) cascade(hit gitio.SecretHit) ([]model.RedactVersion, string, error) {
	desc, note, err := e.Repo.LineDescendants(hit.Commit, hit.Path, hit.Line)
	if err != nil {
		if errors.Is(err, gitio.ErrLineOffFirstParentChain) {
			return nil, fmt.Sprintf(
				"introduced on a side branch merged into HEAD via a non-first-parent merge "+
					"(%s): its later states cannot be tracked forward for cascade; this "+
					"occurrence can still be redacted on its own", shortHash(hit.Commit),
			), nil
		}
		return nil, "", err
	}
	return desc, note, nil
}

// shortHash trims a hash for display, matching gitio's own short-hash
// convention (first 8 characters) — reimplemented locally since gitio does
// not export one, and adding an export just for display formatting here
// would widen gitio's surface for no benefit to its own callers.
func shortHash(hash string) string {
	if len(hash) > 8 {
		return hash[:8]
	}
	return hash
}

// Selection is one operator's choice for one scanned occurrence: whether to
// act on the introduction itself, and which of its Descendants (parallel to
// ScanResult.Occurrences[i].Descendants) to also act on.
type Selection struct {
	Occurrence  bool
	Descendants []bool // parallel to Occurrences[i].Descendants
}

// Plan flattens the operator's checked occurrences and descendants into a
// validated model.RedactSpec: path -> unique line contents to act on. A
// descendant contributes at ITS OWN (possibly renamed) path, never the
// intro's — that is what makes a rename-then-rotate chain redactable at
// both ends. sel must be parallel to res.Occurrences, and each
// sel[i].Descendants parallel to res.Occurrences[i].Descendants; a length
// mismatch is rejected rather than silently truncated or index-panicking.
func (e Engine) Plan(res ScanResult, sel []Selection, mode model.RedactMode,
	rule model.RedactRule, match string) (model.RedactSpec, error) {
	if len(sel) != len(res.Occurrences) {
		return model.RedactSpec{}, fmt.Errorf(
			"redact: selection has %d entries, want %d (one per scanned occurrence)",
			len(sel), len(res.Occurrences))
	}

	targets := map[string][]string{}
	addTarget := func(path, line string) {
		for _, existing := range targets[path] {
			if existing == line {
				return // already present: Targets holds unique lines per path
			}
		}
		targets[path] = append(targets[path], line)
	}

	for i, occ := range res.Occurrences {
		s := sel[i]
		if len(s.Descendants) != len(occ.Descendants) {
			return model.RedactSpec{}, fmt.Errorf(
				"redact: occurrence %d selection has %d descendant entries, want %d",
				i, len(s.Descendants), len(occ.Descendants))
		}
		if s.Occurrence {
			addTarget(occ.Intro.Path, occ.Intro.Line)
		}
		for j, checked := range s.Descendants {
			if checked {
				d := occ.Descendants[j]
				addTarget(d.Path, d.Line)
			}
		}
	}

	spec := model.RedactSpec{
		Match:   match,
		Mode:    mode,
		Rule:    rule,
		Targets: targets,
	}
	if err := spec.Validate(); err != nil {
		return model.RedactSpec{}, err
	}
	return spec, nil
}
