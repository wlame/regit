// internal/gitio/split.go
package gitio

import (
	"fmt"
	"strings"
	"time"

	"github.com/wlame/regit/internal/model"
	"github.com/wlame/regit/internal/termsafe"
)

// SubCommit describes one piece of a split: a fully-built tree plus the message,
// identity, and dates it should be committed with. The last SubCommit's Tree
// must equal the original commit's tree (the caller verifies this invariant).
type SubCommit struct {
	Tree                          string
	Subject, Body                 string
	AuthorName, AuthorEmail       string
	CommitterName, CommitterEmail string
	AuthorDate, CommitDate        time.Time
}

// SplitRemap reports how a split mapped old commits to new ones so the safety
// layer can verify each pair. LastSub is the hash of the final sub-commit (Cₖ),
// whose tree must equal the original split commit's tree. Tail maps each original
// tail commit hash to its replayed counterpart.
type SplitRemap struct {
	LastSub string
	Tail    map[string]string
}

// ApplySplit rebuilds history by replacing one commit with subs (C1..Ck) and
// then replaying the unchanged tail on top. The remap chain works as follows:
//
//	C1 is committed on top of parentHash.
//	C2 is committed on top of C1.
//	…
//	Ck (the last sub-commit) is committed on top of C(k-1).
//	tail[0]'s first parent (the original split commit) is remapped to Ck.
//	Each subsequent tail commit's first parent is remapped to the preceding replayed tail commit.
//
// Tail commits reuse their ORIGINAL tree, message, dates, and identity — only
// their first parent (and therefore their hash) change. No ref is moved; the
// new HEAD hash and a SplitRemap describing the boundary and tail pairs are returned.
func (r *Repo) ApplySplit(parentHash string, subs []SubCommit, tail []model.Commit, sign SignMode) (newHead string, remap SplitRemap, err error) {
	if len(subs) == 0 {
		return "", SplitRemap{}, fmt.Errorf("ApplySplit: no sub-commits")
	}

	// Emit the mini-chain: C1 on parentHash, C2 on C1, …, Ck on C(k-1).
	prev := parentHash
	for _, s := range subs {
		hash, err := r.commitSub(s, prev, sign)
		if err != nil {
			return "", SplitRemap{}, err
		}
		prev = hash
		newHead = hash
	}
	lastSub := newHead // Ck — the final sub-commit, before any tail is replayed.

	// Replay the tail. The first tail commit's first parent is the ORIGINAL split
	// commit hash; map it to Ck so the tail grafts on cleanly. tailRemap records
	// only the original->new tail pairs (not the graft entry) for verification.
	graft := map[string]string{}
	tailRemap := map[string]string{}
	if len(tail) > 0 {
		graft[tail[0].ParentHashes[0]] = newHead
	}

	for _, c := range tail {
		newParents := remapParents(c.ParentHashes, graft)
		message := strings.TrimRight(c.Message, "\n") + "\n"
		env := []string{
			"GIT_AUTHOR_NAME=" + c.AuthorName,
			"GIT_AUTHOR_EMAIL=" + c.AuthorEmail,
			"GIT_AUTHOR_DATE=" + c.OrigAuthorDate.Format(gitDateFormat),
			"GIT_COMMITTER_NAME=" + c.CommitterName,
			"GIT_COMMITTER_EMAIL=" + c.CommitterEmail,
			"GIT_COMMITTER_DATE=" + c.OrigCommitDate.Format(gitDateFormat),
		}
		args := commitTreeArgs(c.TreeHash, newParents, sign)
		// Message is fed on stdin (no -m) so multi-line bodies need no escaping.
		hash, err := r.gitWithEnvStdin(env, message, args...)
		if err != nil {
			return "", SplitRemap{}, fmt.Errorf("replay tail %s: %w", short(c.Hash), err)
		}
		graft[c.Hash] = hash     // so the next tail commit's parent remaps
		tailRemap[c.Hash] = hash // recorded for verification
		newHead = hash
	}

	return newHead, SplitRemap{LastSub: lastSub, Tail: tailRemap}, nil
}

// subCommitMessage assembles the commit message for a synthesized (split or
// squash) commit. The subject is LLM- or user-supplied, so it is normalized at
// CREATION time, not just at display: escape sequences and control bytes are
// stripped and embedded newlines fold to spaces (termsafe.Line), then the
// result is whitespace-trimmed. Without this, a model reply containing "\n"
// would write a multi-line subject into the real commit object — every later
// `git log --oneline` would show only its first fragment. The body keeps its
// newlines untouched; multi-line bodies are legitimate structure.
func subCommitMessage(subject, body string) string {
	subj := strings.TrimSpace(termsafe.Line(subject))
	if body != "" {
		return subj + "\n\n" + body + "\n"
	}
	return subj + "\n"
}

// commitSub creates a single sub-commit object on top of parent. The message is
// assembled from the SubCommit's subject and optional body (see
// subCommitMessage), then fed to commit-tree via stdin to avoid shell-escaping
// pitfalls.
func (r *Repo) commitSub(s SubCommit, parent string, sign SignMode) (string, error) {
	message := subCommitMessage(s.Subject, s.Body)
	env := []string{
		"GIT_AUTHOR_NAME=" + s.AuthorName,
		"GIT_AUTHOR_EMAIL=" + s.AuthorEmail,
		"GIT_AUTHOR_DATE=" + s.AuthorDate.Format(gitDateFormat),
		"GIT_COMMITTER_NAME=" + s.CommitterName,
		"GIT_COMMITTER_EMAIL=" + s.CommitterEmail,
		"GIT_COMMITTER_DATE=" + s.CommitDate.Format(gitDateFormat),
	}
	// An empty parent means this sub-commit is the new root (the split target was
	// itself a root commit); omit -p entirely so `git commit-tree` builds a valid
	// parentless commit instead of failing on `-p ""`. Mirrors commitSquash.
	var parents []string
	if parent != "" {
		parents = []string{parent}
	}
	// A SubCommit carries no source model.Commit, so it has no original
	// encoding header to preserve; its fresh message is always UTF-8.
	args := commitTreeArgs(s.Tree, parents, sign)
	return r.gitWithEnvStdin(env, message, args...)
}
