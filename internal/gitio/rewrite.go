package gitio

import (
	"fmt"
	"strings"
	"time"

	"github.com/wlame/regit/internal/model"
)

// SignMode controls what happens to signed commits during a rewrite, since
// rewriting always invalidates an existing signature.
type SignMode int

const (
	// DropSignatures rebuilds commits without any signature (the safe default).
	DropSignatures SignMode = iota
	// ReSign re-signs each rewritten commit with the user's configured key.
	ReSign
)

// gitDateFormat is what we hand git via GIT_AUTHOR_DATE / GIT_COMMITTER_DATE.
// git accepts strict ISO 8601, and RFC3339 is a compatible subset that carries
// the timezone offset so the calendar day lands where the user expects.
const gitDateFormat = time.RFC3339

// commitTreeArgs builds the argument vector for `git commit-tree`, threading the
// signing choice and the original encoding header. When encoding is a non-UTF-8
// value we prepend `-c i18n.commitEncoding=<enc>` so git writes the same
// `encoding` header it read and treats the raw stdin message bytes as that
// charset — otherwise a non-UTF-8 message (fed back verbatim from %B) would be
// mislabeled UTF-8 and display as mojibake. UTF-8 (encoding == "" or "UTF-8")
// needs no override. This is the ONLY place that builds a commit-tree argument
// vector; every commit-emitting call site in this package routes through it so
// the encoding round-trip can never be forgotten at a new site.
func commitTreeArgs(tree string, parents []string, sign SignMode, encoding string) []string {
	var args []string
	if enc := strings.TrimSpace(encoding); enc != "" && !strings.EqualFold(enc, "UTF-8") {
		args = append(args, "-c", "i18n.commitEncoding="+enc)
	}
	args = append(args, "commit-tree", tree)
	for _, p := range parents {
		args = append(args, "-p", p)
	}
	if sign == ReSign {
		args = append(args, "-S")
	}
	return args
}

// Replay rebuilds the plan's commits with new dates/messages and returns the new
// HEAD hash WITHOUT moving any ref (the caller decides when to commit to it).
//
// The guarantee that file contents never change comes from one detail: each new
// commit is created with `git commit-tree <ORIGINAL tree hash>`. We reuse the
// exact tree object, so the snapshot is byte-for-byte identical and only the
// commit headers (dates, message, parent) differ.
//
// Commits are walked root-first. As each is rebuilt, we remember its new hash so
// the next commit can point its first parent at the rewritten ancestor. For
// merge commits (locked in v1) we remap only the first parent and keep the other
// parents pointing at their original, un-rewritten side branches.
func (r *Repo) Replay(plan *model.Plan, sign SignMode) (newHead string, remap map[string]string, err error) {
	if len(plan.Commits) == 0 {
		return "", nil, fmt.Errorf("nothing to replay: plan has no commits")
	}

	// Commits before the plan's base boundary are not replayed at all. The
	// first rebuilt commit finds no remap entry for its parent and therefore
	// keeps pointing at the original, untouched ancestor — which is the hard
	// guarantee that frozen history never changes.
	active := plan.Active()
	if len(active) == 0 {
		return "", nil, fmt.Errorf("nothing to replay: every commit is frozen")
	}

	remap = make(map[string]string, len(active)) // old hash -> new hash

	for _, c := range active {
		edit, _ := plan.EditFor(c.Hash)

		newParents := remapParents(c.ParentHashes, remap)
		message := buildMessage(c, edit)
		env := buildCommitEnv(c, edit)

		args := commitTreeArgs(c.TreeHash, newParents, sign, c.Encoding)
		// Message is fed on stdin (no -m) so multi-line bodies need no escaping.
		newHash, err := r.gitWithEnvStdin(env, message, args...)
		if err != nil {
			return "", nil, fmt.Errorf("commit-tree for %s: %w", short(c.Hash), err)
		}
		remap[c.Hash] = newHash
		newHead = newHash
	}

	return newHead, remap, nil
}

// remapParents replaces the first parent with its rewritten counterpart while
// leaving any additional (merge) parents untouched, since those side branches
// are not part of the rewrite.
func remapParents(parents []string, remap map[string]string) []string {
	if len(parents) == 0 {
		return nil
	}
	out := make([]string, len(parents))
	copy(out, parents)
	if mapped, ok := remap[parents[0]]; ok {
		out[0] = mapped
	}
	return out
}

// buildMessage assembles the commit message from the original commit and any
// edit, honoring the "drop body, keep subject" action.
//
// When the edit does not touch the message at all, the original raw message is
// replayed verbatim (modulo a single trailing newline) — rebuilding it from a
// subject/body split would re-structure messages whose first paragraph spans
// several lines or that lack a blank line after the title.
func buildMessage(c model.Commit, e model.Edit) string {
	if e.NewSubject == nil && e.NewBody == nil && !e.DropBody {
		return strings.TrimRight(c.Message, "\n") + "\n"
	}

	subject := c.Subject
	if e.NewSubject != nil {
		subject = *e.NewSubject
	}

	body := c.Body
	if e.DropBody {
		body = ""
	} else if e.NewBody != nil {
		body = *e.NewBody
	}

	if body == "" {
		return subject + "\n"
	}
	return subject + "\n\n" + body + "\n"
}

// buildCommitEnv produces the GIT_*_DATE and identity environment for one commit.
// Both identity (name/email) and dates come from the edit's effective values, so
// each field is overridden only when the edit specifies it and otherwise replays
// the original. File content is never affected: Replay always reuses the original
// tree object, so this env governs only commit headers.
func buildCommitEnv(c model.Commit, e model.Edit) []string {
	return []string{
		"GIT_AUTHOR_NAME=" + e.EffectiveAuthorName(c),
		"GIT_AUTHOR_EMAIL=" + e.EffectiveAuthorEmail(c),
		"GIT_AUTHOR_DATE=" + e.EffectiveAuthorDate(c).Format(gitDateFormat),
		"GIT_COMMITTER_NAME=" + e.EffectiveCommitterName(c),
		"GIT_COMMITTER_EMAIL=" + e.EffectiveCommitterEmail(c),
		"GIT_COMMITTER_DATE=" + e.EffectiveCommitDate(c).Format(gitDateFormat),
	}
}

// VerifyTreesUnchanged asserts that the rewrite changed no file content by
// diffing the old and new tips. A non-empty diff means something is wrong and
// the rewrite must not be trusted.
func (r *Repo) VerifyTreesUnchanged(oldHead, newHead string) error {
	diff, err := r.git("diff", "--stat", oldHead, newHead)
	if err != nil {
		return err
	}
	if diff != "" {
		return fmt.Errorf("file contents changed during rewrite (this is a bug); diff:\n%s", diff)
	}
	return nil
}

// short trims a hash for error messages.
func short(hash string) string {
	if len(hash) > 8 {
		return hash[:8]
	}
	return hash
}
