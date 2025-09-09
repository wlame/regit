package gitio

import (
	"fmt"
	"strings"

	"github.com/wlame/regit/internal/model"
)

// CommitTrees resolves each commit hash to its tree-object hash in a single
// batched git call, returning a map keyed by the input commit hash.
//
// This is the cheap heart of empty-expectation verification: two commits with
// the same tree hash are byte-identical snapshots, so no diff is needed.
func (r *Repo) CommitTrees(commits []string) (map[string]string, error) {
	if len(commits) == 0 {
		return map[string]string{}, nil
	}
	args := make([]string, 0, len(commits)+1)
	args = append(args, "rev-parse")
	for _, c := range commits {
		// "<commit>^{tree}" peels a commit object to the tree it points at.
		args = append(args, c+"^{tree}")
	}
	out, err := r.git(args...)
	if err != nil {
		return nil, fmt.Errorf("resolve commit trees: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != len(commits) {
		return nil, fmt.Errorf("rev-parse returned %d trees for %d commits", len(lines), len(commits))
	}
	trees := make(map[string]string, len(commits))
	for i, c := range commits {
		trees[c] = strings.TrimSpace(lines[i])
	}
	return trees, nil
}

// DiffChangeSet computes the actual per-path change-set between two commits using
// git's own content hashes (`git diff --raw`). Rename/copy detection is OFF, so a
// rename appears as a Deleted + Added pair and only A/M/D statuses are produced.
// The result is normalized (sorted by path).
//
// The -z flag NUL-delimits records so paths with spaces or special characters
// parse correctly. The stream alternates [info, path, info, path, …] where each
// info token is ":<srcmode> <dstmode> <srcsha> <dstsha> <STATUS>".
func (r *Repo) DiffChangeSet(oldCommit, newCommit string) (model.ChangeSet, error) {
	out, err := r.gitRaw("diff", "--raw", "-z", oldCommit, newCommit)
	if err != nil {
		return model.ChangeSet{}, fmt.Errorf("diff %s..%s: %w", short(oldCommit), short(newCommit), err)
	}
	tokens := strings.Split(out, "\x00")
	var changes []model.PathChange
	i := 0
	for i < len(tokens) {
		info := tokens[i]
		if info == "" {
			i++
			continue
		}
		if !strings.HasPrefix(info, ":") {
			return model.ChangeSet{}, fmt.Errorf("unexpected diff token %q", info)
		}
		fields := strings.Fields(info)
		if len(fields) < 5 {
			return model.ChangeSet{}, fmt.Errorf("malformed diff info %q", info)
		}
		if i+1 >= len(tokens) {
			return model.ChangeSet{}, fmt.Errorf("diff info %q missing path", info)
		}
		status, err := statusFromLetter(fields[4])
		if err != nil {
			return model.ChangeSet{}, err
		}
		changes = append(changes, model.PathChange{Path: tokens[i+1], Status: status})
		i += 2
	}
	return model.NewChangeSet(changes), nil
}

// statusFromLetter maps a git --raw status letter to a model.ChangeStatus. Only
// A/M/D are expected because rename/copy detection is off.
func statusFromLetter(s string) (model.ChangeStatus, error) {
	if s == "" {
		return 0, fmt.Errorf("empty diff status")
	}
	switch s[0] {
	case 'A':
		return model.Added, nil
	case 'M':
		return model.Modified, nil
	case 'D':
		return model.Deleted, nil
	default:
		return 0, fmt.Errorf("unsupported diff status %q (rename/copy detection is off)", s)
	}
}
