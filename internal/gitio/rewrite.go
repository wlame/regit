package gitio

import (
	"fmt"
	"time"

	"github.com/wlame/ghist/internal/model"
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
func (r *Repo) Replay(plan *model.Plan, sign SignMode) (newHead string, err error) {
	if len(plan.Commits) == 0 {
		return "", fmt.Errorf("nothing to replay: plan has no commits")
	}

	remap := make(map[string]string, len(plan.Commits)) // old hash -> new hash

	for _, c := range plan.Commits {
		edit, _ := plan.EditFor(c.Hash)

		newParents := remapParents(c.ParentHashes, remap)
		message := buildMessage(c, edit)
		env := buildDateEnv(c, edit)

		args := []string{"commit-tree", c.TreeHash}
		for _, p := range newParents {
			args = append(args, "-p", p)
		}
		if sign == ReSign {
			args = append(args, "-S")
		}
		// Message is fed on stdin (no -m) so multi-line bodies need no escaping.
		newHash, err := r.gitWithEnvStdin(env, message, args...)
		if err != nil {
			return "", fmt.Errorf("commit-tree for %s: %w", short(c.Hash), err)
		}
		remap[c.Hash] = newHash
		newHead = newHash
	}

	return newHead, nil
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
func buildMessage(c model.Commit, e model.Edit) string {
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

// buildDateEnv produces the GIT_*_DATE / identity environment for one commit.
// Identity (name/email) is always preserved from the original commit; only the
// dates are overridden, and only when the edit specifies them.
func buildDateEnv(c model.Commit, e model.Edit) []string {
	authorDate := e.EffectiveAuthorDate(c)
	commitDate := e.EffectiveCommitDate(c)

	return []string{
		"GIT_AUTHOR_NAME=" + c.AuthorName,
		"GIT_AUTHOR_EMAIL=" + c.AuthorEmail,
		"GIT_AUTHOR_DATE=" + authorDate.Format(gitDateFormat),
		"GIT_COMMITTER_NAME=" + c.CommitterName,
		"GIT_COMMITTER_EMAIL=" + c.CommitterEmail,
		"GIT_COMMITTER_DATE=" + commitDate.Format(gitDateFormat),
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
