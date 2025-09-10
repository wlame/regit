// internal/gitio/squash.go
package gitio

import (
	"fmt"

	"github.com/wlame/regit/internal/model"
)

// SquashRemap reports how a squash mapped old commits to new ones for
// verification. Boundary maps each chain's LAST original commit hash to its
// squashed new hash (their trees must be equal). Passthrough maps each replayed
// untouched commit's old hash to its new hash (must be byte-identical). Absorbed
// members (every member except a chain's last) have no entry — they vanish.
type SquashRemap struct {
	Boundary    map[string]string
	Passthrough map[string]string
}

// ApplySquash rebuilds history by collapsing each chain into one commit and
// replaying every other commit byte-identically on the new parent chain. It walks
// the full root-first history once, starting at the earliest affected commit;
// commits before it keep their original hashes (not re-emitted). No ref is moved.
//
// For each commit from the first affected onward:
//   - a NON-LAST chain member is skipped (absorbed into its chain's squash),
//   - a chain's LAST member emits the squashed commit (tree = member's tree, the
//     chain's chosen message/identity, committer = author),
//   - any other commit is re-emitted with its original tree/message/identity/dates
//     and its first parent remapped to the previously emitted commit.
func (r *Repo) ApplySquash(commits []model.Commit, plan model.SquashPlan, sign SignMode) (newHead string, remap SquashRemap, err error) {
	if len(plan.Chains) == 0 {
		return "", SquashRemap{}, fmt.Errorf("ApplySquash: no chains")
	}

	pos := make(map[string]int, len(commits))
	for i, c := range commits {
		pos[c.Hash] = i
	}

	// Classify: lastOfChain[hash] => the chain whose last member is hash; absorbed
	// holds every non-last member. firstAffected is the earliest chain's first member.
	lastOfChain := make(map[string]model.SquashChain)
	absorbed := make(map[string]bool)
	firstAffected := len(commits)
	for _, ch := range plan.Chains {
		if p, ok := pos[ch.First().Hash]; ok && p < firstAffected {
			firstAffected = p
		}
		lastOfChain[ch.Last().Hash] = ch
		for _, m := range ch.Members[:len(ch.Members)-1] {
			absorbed[m.Hash] = true
		}
	}
	if firstAffected >= len(commits) {
		return "", SquashRemap{}, fmt.Errorf("ApplySquash: chains not found on history")
	}

	remap = SquashRemap{Boundary: map[string]string{}, Passthrough: map[string]string{}}

	// prevNew is the hash the next emitted commit parents on. It starts as the
	// ORIGINAL parent of the first affected commit ("" when that commit is the
	// root), because everything before firstAffected keeps its original hash.
	var prevNew string
	if fp := commits[firstAffected].ParentHashes; len(fp) > 0 {
		prevNew = fp[0]
	}

	for i := firstAffected; i < len(commits); i++ {
		c := commits[i]
		if absorbed[c.Hash] {
			continue // folded into its chain's squash; prevNew unchanged
		}
		if ch, ok := lastOfChain[c.Hash]; ok {
			hash, e := r.commitSquash(squashSpec(ch, c), prevNew, sign)
			if e != nil {
				return "", SquashRemap{}, fmt.Errorf("squash %s: %w", short(c.Hash), e)
			}
			remap.Boundary[c.Hash] = hash
			prevNew, newHead = hash, hash
			continue
		}
		// Untouched commit: re-emit byte-identically with a remapped first parent.
		newParents := remapFirstParent(c.ParentHashes, prevNew)
		args := []string{"commit-tree", c.TreeHash}
		for _, p := range newParents {
			args = append(args, "-p", p)
		}
		if sign == ReSign {
			args = append(args, "-S")
		}
		hash, e := r.gitWithEnvStdin(buildDateEnv(c, model.Edit{}), buildMessage(c, model.Edit{}), args...)
		if e != nil {
			return "", SquashRemap{}, fmt.Errorf("replay %s: %w", short(c.Hash), e)
		}
		remap.Passthrough[c.Hash] = hash
		prevNew, newHead = hash, hash
	}
	return newHead, remap, nil
}

// squashSpec turns a chain's chosen config into a SubCommit. The squashed commit
// reuses the last member's tree, and its committer mirrors its author (identity +
// date), per the linked-dates default.
func squashSpec(ch model.SquashChain, last model.Commit) SubCommit {
	return SubCommit{
		Tree:           last.TreeHash,
		Subject:        ch.Subject,
		Body:           ch.Body,
		AuthorName:     ch.AuthorName,
		AuthorEmail:    ch.AuthorEmail,
		CommitterName:  ch.AuthorName,
		CommitterEmail: ch.AuthorEmail,
		AuthorDate:     ch.AuthorDate,
		CommitDate:     ch.AuthorDate,
	}
}

// commitSquash creates the single squashed commit object on top of parent ("" =
// root). The message is fed on stdin to avoid shell-escaping multi-line bodies.
func (r *Repo) commitSquash(s SubCommit, parent string, sign SignMode) (string, error) {
	message := s.Subject + "\n"
	if s.Body != "" {
		message = s.Subject + "\n\n" + s.Body + "\n"
	}
	env := []string{
		"GIT_AUTHOR_NAME=" + s.AuthorName,
		"GIT_AUTHOR_EMAIL=" + s.AuthorEmail,
		"GIT_AUTHOR_DATE=" + s.AuthorDate.Format(gitDateFormat),
		"GIT_COMMITTER_NAME=" + s.CommitterName,
		"GIT_COMMITTER_EMAIL=" + s.CommitterEmail,
		"GIT_COMMITTER_DATE=" + s.CommitDate.Format(gitDateFormat),
	}
	args := []string{"commit-tree", s.Tree}
	if parent != "" {
		args = append(args, "-p", parent)
	}
	if sign == ReSign {
		args = append(args, "-S")
	}
	return r.gitWithEnvStdin(env, message, args...)
}

// remapFirstParent replaces a commit's first parent with newFirst (the previously
// emitted commit), leaving any merge parents untouched. An untouched commit always
// has a previously-emitted predecessor, so newFirst is non-empty here.
func remapFirstParent(parents []string, newFirst string) []string {
	if len(parents) == 0 {
		return nil
	}
	out := make([]string, len(parents))
	copy(out, parents)
	out[0] = newFirst
	return out
}
