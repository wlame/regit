package gitio

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/wlame/regit/internal/model"
)

// Field and record separators for parsing `git log` output. We use the ASCII
// unit separator (0x1f) between fields and record separator (0x1e) between
// commits because they can never appear in commit metadata, so commit messages
// containing newlines, tabs, or pipes parse correctly.
const (
	fieldSep  = "\x1f"
	recordSep = "\x1e"
)

// logFormat lists the fields we read per commit, in order, joined by fieldSep
// and terminated by recordSep. %H hash, %T tree, %P parents, author/committer
// identity and ISO dates, %e encoding header, %G? signature status, %B raw
// message.
//
// The message is read as raw %B on purpose: git's %s folds a multi-line first
// paragraph into one space-joined line and %b drops everything before the
// first blank line, which silently corrupted messages on rewrite. %e must
// come before %B (not after) because %B is the last field and greedily
// consumes everything to the end of the record when split on fieldSep.
var logFormat = strings.Join([]string{
	"%H", "%T", "%P",
	"%an", "%ae", "%cn", "%ce",
	"%aI", "%cI",
	"%e",
	"%G?",
	"%B",
}, fieldSep) + recordSep

// ReadState gathers the repository status the safety guards need.
func (r *Repo) ReadState() (model.RepoState, error) {
	st := model.RepoState{Root: r.root}

	if head, err := r.ResolveRef("HEAD"); err == nil {
		st.HeadHash = head
	}

	if branch, err := r.CurrentBranch(); err == nil {
		st.Branch = branch
	}

	status, err := r.git("status", "--porcelain")
	if err != nil {
		return st, err
	}
	st.IsDirty = strings.TrimSpace(status) != ""

	// Upstream tracking branch, if any. A failure here just means "no upstream".
	if up, err := r.git("rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}"); err == nil && up != "" {
		st.HasUpstream = true
		st.UpstreamRef = up
		if cnt, err := r.git("rev-list", "--count", up+"..HEAD"); err == nil {
			st.AheadOfRemote, _ = strconv.Atoi(cnt)
		}
	}

	if shallow, err := r.IsShallow(); err == nil {
		st.IsShallow = shallow
	}

	return st, nil
}

// IsShallow reports whether the repository is a shallow clone (created with
// --depth or otherwise grafted). Rewriting shallow history is unsafe because the
// grafted boundary is not a real root, so the safety guards refuse it.
func (r *Repo) IsShallow() (bool, error) {
	out, err := r.git("rev-parse", "--is-shallow-repository")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "true", nil
}

// IsDirty reports whether the working tree has uncommitted changes (including
// untracked files), via one `git status --porcelain` call. Used by safety to
// re-check dirtiness at apply time instead of trusting a stale snapshot.
func (r *Repo) IsDirty() (bool, error) {
	status, err := r.git("status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(status) != "", nil
}

// ReadCommits returns the first-parent linear history of the current branch in
// root-first order (oldest commit at index 0), which is the order the rewrite
// engine replays. Commits already present on any remote-tracking ref are
// flagged IsPushed.
func (r *Repo) ReadCommits() ([]model.Commit, error) {
	out, err := r.gitRaw("log", "--first-parent", "--reverse", "--format="+logFormat, "HEAD")
	if err != nil {
		return nil, err
	}

	pushed, _ := r.pushedHashes() // best-effort; empty set if no upstream

	var commits []model.Commit
	for _, record := range strings.Split(out, recordSep) {
		record = strings.Trim(record, "\n")
		if record == "" {
			continue
		}
		c, err := parseCommitRecord(record)
		if err != nil {
			return nil, err
		}
		c.IsPushed = pushed[c.Hash]
		commits = append(commits, c)
	}
	return commits, nil
}

// pushedHashes returns the set of commit hashes reachable from ANY
// remote-tracking ref (refs/remotes/**), so IsPushed reflects every remote a
// commit was published to — not only the configured @{u} upstream. A commit
// pushed to a non-tracking remote (e.g. a fork) is still flagged, which is the
// safe direction: under-reporting would let a rewrite of remote history proceed
// unwarned.
func (r *Repo) pushedHashes() (map[string]bool, error) {
	// --remotes with no argument means "all refs under refs/remotes"; if there
	// are none, rev-list prints nothing and we return an empty set.
	out, err := r.gitRaw("rev-list", "--remotes")
	if err != nil {
		return map[string]bool{}, nil // best-effort: no remotes / bare → empty
	}
	set := make(map[string]bool)
	for _, h := range strings.Fields(out) {
		set[h] = true
	}
	return set, nil
}

// parseCommitRecord turns one fieldSep-joined log record into a model.Commit.
func parseCommitRecord(record string) (model.Commit, error) {
	f := strings.Split(record, fieldSep)
	if len(f) < 12 {
		return model.Commit{}, fmt.Errorf("malformed log record: expected 12 fields, got %d", len(f))
	}

	authorDate, err := time.Parse(time.RFC3339, f[7])
	if err != nil {
		return model.Commit{}, fmt.Errorf("parse author date %q: %w", f[7], err)
	}
	commitDate, err := time.Parse(time.RFC3339, f[8])
	if err != nil {
		return model.Commit{}, fmt.Errorf("parse commit date %q: %w", f[8], err)
	}

	var parents []string
	if p := strings.TrimSpace(f[2]); p != "" {
		parents = strings.Fields(p)
	}

	message := strings.TrimRight(f[11], "\n")
	subject, body := model.SplitMessage(message)

	return model.Commit{
		Hash:           f[0],
		TreeHash:       f[1],
		ParentHashes:   parents,
		AuthorName:     f[3],
		AuthorEmail:    f[4],
		CommitterName:  f[5],
		CommitterEmail: f[6],
		OrigAuthorDate: authorDate,
		OrigCommitDate: commitDate,
		Encoding:       strings.TrimSpace(f[9]),
		IsSigned:       signatureStatus(f[10]),
		Message:        message,
		Subject:        subject,
		Body:           body,
		IsMerge:        len(parents) > 1,
	}, nil
}

// signatureStatus interprets git's %G? code: G (good), U (good, untrusted), X/Y/R
// (expired/revoked variants) all mean a signature is present; N means none.
func signatureStatus(code string) bool {
	switch strings.TrimSpace(code) {
	case "G", "U", "X", "Y", "R", "E":
		return true
	default:
		return false
	}
}
