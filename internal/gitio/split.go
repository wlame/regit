// internal/gitio/split.go
package gitio

import (
	"fmt"
	"strings"
	"time"

	"github.com/wlame/regit/internal/model"
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
// new HEAD hash is returned.
func (r *Repo) ApplySplit(parentHash string, subs []SubCommit, tail []model.Commit, sign SignMode) (string, error) {
	if len(subs) == 0 {
		return "", fmt.Errorf("ApplySplit: no sub-commits")
	}

	// Emit the mini-chain: C1 on parentHash, C2 on C1, …, Ck on C(k-1).
	prev := parentHash
	var newHead string
	for _, s := range subs {
		hash, err := r.commitSub(s, prev, sign)
		if err != nil {
			return "", err
		}
		prev = hash
		newHead = hash
	}

	// Replay the tail with the existing remap mechanism. The first tail commit's
	// first parent is the ORIGINAL split commit hash; we map it to the last
	// sub-commit (newHead) so the tail grafts on cleanly.
	remap := map[string]string{}
	if len(tail) > 0 {
		// tail[0].ParentHashes[0] is the original split commit hash.
		remap[tail[0].ParentHashes[0]] = newHead
	}

	for _, c := range tail {
		newParents := remapParents(c.ParentHashes, remap)
		message := strings.TrimRight(c.Message, "\n") + "\n"
		env := []string{
			"GIT_AUTHOR_NAME=" + c.AuthorName,
			"GIT_AUTHOR_EMAIL=" + c.AuthorEmail,
			"GIT_AUTHOR_DATE=" + c.OrigAuthorDate.Format(time.RFC3339),
			"GIT_COMMITTER_NAME=" + c.CommitterName,
			"GIT_COMMITTER_EMAIL=" + c.CommitterEmail,
			"GIT_COMMITTER_DATE=" + c.OrigCommitDate.Format(time.RFC3339),
		}
		args := []string{"commit-tree", c.TreeHash}
		for _, p := range newParents {
			args = append(args, "-p", p)
		}
		if sign == ReSign {
			args = append(args, "-S")
		}
		// Message is fed on stdin (no -m) so multi-line bodies need no escaping.
		hash, err := r.gitWithEnvStdin(env, message, args...)
		if err != nil {
			return "", fmt.Errorf("replay tail %s: %w", short(c.Hash), err)
		}
		remap[c.Hash] = hash
		newHead = hash
	}

	return newHead, nil
}

// commitSub creates a single sub-commit object on top of parent. The message is
// assembled from the SubCommit's subject and optional body, then fed to
// commit-tree via stdin to avoid shell-escaping pitfalls.
func (r *Repo) commitSub(s SubCommit, parent string, sign SignMode) (string, error) {
	message := s.Subject + "\n"
	if s.Body != "" {
		message = s.Subject + "\n\n" + s.Body + "\n"
	}
	env := []string{
		"GIT_AUTHOR_NAME=" + s.AuthorName,
		"GIT_AUTHOR_EMAIL=" + s.AuthorEmail,
		"GIT_AUTHOR_DATE=" + s.AuthorDate.Format(time.RFC3339),
		"GIT_COMMITTER_NAME=" + s.CommitterName,
		"GIT_COMMITTER_EMAIL=" + s.CommitterEmail,
		"GIT_COMMITTER_DATE=" + s.CommitDate.Format(time.RFC3339),
	}
	args := []string{"commit-tree", s.Tree, "-p", parent}
	if sign == ReSign {
		args = append(args, "-S")
	}
	return r.gitWithEnvStdin(env, message, args...)
}
