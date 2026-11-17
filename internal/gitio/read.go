package gitio

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/wlame/regit/internal/model"
)

// logFieldSep is git's `%x00` placeholder, which git expands to a literal NUL
// byte between the fields of one commit's formatted output. NUL is the ONE byte
// that can never appear inside a git object: commit messages, author names, and
// paths may contain newlines, tabs, the ASCII unit/record separators (0x1f /
// 0x1e), or any other control byte — but never NUL. An earlier version used
// 0x1f/0x1e and wrongly assumed those bytes "can never appear in commit
// metadata"; a message carrying a 0x1f was silently truncated on rewrite and a
// 0x1e made the repo unreadable. NUL delimiting removes that whole class of bug.
const logFieldSep = "%x00"

// logFields lists the fields we read per commit, in order. %H hash, %T tree,
// %P parents, author/committer identity and ISO dates, %e encoding header,
// %G? signature status, %B raw message.
//
// The message is read as raw %B on purpose: git's %s folds a multi-line first
// paragraph into one space-joined line and %b drops everything before the first
// blank line, which silently corrupted messages on rewrite. %B stays last only
// for readability now — with NUL delimiting its position no longer matters for
// correctness, because no field can contain the separator.
var logFields = []string{
	"%H", "%T", "%P",
	"%an", "%ae", "%cn", "%ce",
	"%aI", "%cI",
	"%e",
	"%G?",
	"%B",
}

// numLogFields is how many tokens one commit contributes to the NUL-delimited
// stream; the parser regroups the flat token list in chunks of this size, so it
// MUST equal len(logFields).
const numLogFields = 12

var logFormat = strings.Join(logFields, logFieldSep)

// ReadState gathers the repository status the safety guards need.
func (r *Repo) ReadState() (model.RepoState, error) {
	st := model.RepoState{Root: r.root}

	if head, err := r.ResolveRef("HEAD"); err == nil {
		st.HeadHash = head
	}

	if branch, err := r.CurrentBranch(); err == nil {
		st.Branch = branch
	}

	worktree, err := r.WorktreeStatus()
	if err != nil {
		return st, err
	}
	st.WorktreeStatus = worktree

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

// untrackedStatusPrefix starts a `git status --porcelain` line for a file git
// does not track. Every other line is a staged or unstaged change to a tracked
// file.
const untrackedStatusPrefix = "??"

// WorktreeStatus reports what is uncommitted right now, from one
// `git status --porcelain` call. Safety re-reads it at apply time instead of
// trusting a snapshot. Ignored files are not listed by git and do not count.
func (r *Repo) WorktreeStatus() (model.WorktreeStatus, error) {
	out, err := r.git("status", "--porcelain")
	if err != nil {
		return model.WorktreeStatus{}, err
	}
	var status model.WorktreeStatus
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.TrimSpace(line) == "":
			// no entry
		case strings.HasPrefix(line, untrackedStatusPrefix):
			status.HasUntrackedFiles = true
		default:
			status.HasTrackedChanges = true
		}
	}
	return status, nil
}

// FirstParents returns up to n commit hashes of rev's first-parent chain,
// newest first (rev itself is entry 0). A chain shorter than n (rev is close
// to a root) returns just the commits that exist. The safety verifier uses it
// for the structural chain check that complements per-pair tree equality.
func (r *Repo) FirstParents(rev string, n int) ([]string, error) {
	if n <= 0 {
		return nil, nil
	}
	out, err := r.git("rev-list", "--first-parent", "-n", strconv.Itoa(n), rev)
	if err != nil {
		return nil, fmt.Errorf("rev-list --first-parent %s: %w", short(rev), err)
	}
	return strings.Fields(out), nil
}

// CurrentBranchIsUnborn reports whether the CURRENT branch (i.e. HEAD) has no
// commits yet, as opposed to a repository whose current ref exists but is
// broken, or a repository where some OTHER branch is unborn while HEAD is
// fine. Two conditions must hold together; neither is sufficient alone, and
// each was tried alone and found wrong:
//
//  1. "rev-parse --verify --quiet HEAD" fails. This alone looks like the
//     obvious check but is wrong: it fails identically for an unborn branch
//     AND for a corrupt ref (e.g. refs/heads/main containing garbage instead
//     of a hash). Gating on it alone reported "no commits yet" for a
//     corrupted repository, swallowing a real error behind a reassuring lie —
//     worse than the verbose error it replaced.
//
//  2. "rev-list -n 1 --all" exits 0 (no ref in the repo is corrupt). This
//     alone is also wrong, in the opposite direction: it answers a
//     REPO-WIDE question ("does any ref anywhere have commits?") while the
//     caller's question is PER-BRANCH ("is HEAD unborn?"). On an orphan
//     branch created after another branch already has history (e.g. `git
//     checkout --orphan gh-pages` on a repo where main has commits),
//     "rev-list --all" exits 0 with main's commit as output — "has commits"
//     by this check alone — even though HEAD itself does not resolve and
//     `git log HEAD` fails with "ambiguous argument 'HEAD'". Used alone,
//     this let the raw verbose git error leak on exactly the case the
//     friendly message exists to catch.
//
// So: HEAD unresolved AND no ref is corrupt, together, is the correct
// definition of "current branch is unborn". A non-zero exit from rev-list
// (git's own "fatal: bad object ..." detail) means a ref is corrupt and must
// propagate as a real error rather than be folded into false.
func (r *Repo) CurrentBranchIsUnborn() (bool, error) {
	if _, err := r.git("rev-parse", "--verify", "--quiet", "HEAD"); err == nil {
		return false, nil
	}
	if _, err := r.git("rev-list", "-n", "1", "--all"); err != nil {
		return false, err
	}
	return true, nil
}

// ReadCommits returns the first-parent linear history of the current branch in
// root-first order (oldest commit at index 0), which is the order the rewrite
// engine replays. Commits already present on any remote-tracking ref are
// flagged IsPushed.
func (r *Repo) ReadCommits() ([]model.Commit, error) {
	// -z NUL-terminates each commit's formatted output, so combined with the
	// %x00 field separators the whole stream is a flat run of NUL-delimited
	// tokens: N fields per commit, each commit followed by its own terminating
	// NUL. --encoding=UTF-8 forces the message bytes to UTF-8 regardless of the
	// user's i18n.logOutputEncoding, and --no-show-signature stops a repo-level
	// log.showSignature=true from injecting gpg output that would corrupt the
	// first field of each record.
	out, err := r.gitRaw("log", "--first-parent", "--reverse", "-z",
		"--no-show-signature", "--encoding=UTF-8", "--format="+logFormat, "HEAD")
	if err != nil {
		// A branch with no commits is a predictable, user-facing situation, not a
		// plumbing failure. runGit's wrapper is deliberately verbose for debugging
		// — it embeds the whole argv and git's stderr — which is exactly wrong
		// here. CurrentBranchIsUnborn distinguishes "empty branch" from a real git
		// failure, which must keep its detailed message.
		if unborn, uerr := r.CurrentBranchIsUnborn(); uerr == nil && unborn {
			return nil, fmt.Errorf("this branch has no commits yet")
		}
		return nil, err
	}

	// A pushedHashes failure propagates rather than reading as "nothing
	// pushed": IsPushed feeds the pushed-history confirmation gate, and a
	// guard input must fail closed. (No remotes at all is still the benign
	// empty set, not an error.)
	pushed, err := r.pushedHashes()
	if err != nil {
		return nil, err
	}

	tokens := strings.Split(out, "\x00")
	var commits []model.Commit
	for i := 0; i+numLogFields <= len(tokens); i += numLogFields {
		c, err := parseCommitFields(tokens[i : i+numLogFields])
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
	// are none, rev-list prints nothing (exit 0) and we return an empty set.
	// A FAILURE, by contrast, must propagate: swallowing it into an empty set
	// (the old behavior) reported every commit as unpushed, silently switching
	// off the pushed-history confirmation gate — the guard must degrade CLOSED.
	out, err := r.gitRaw("rev-list", "--remotes")
	if err != nil {
		return nil, fmt.Errorf("list pushed commits: %w", err)
	}
	set := make(map[string]bool)
	for _, h := range strings.Fields(out) {
		set[h] = true
	}
	return set, nil
}

// parseCommitFields turns one commit's NUL-delimited field tokens into a
// model.Commit. The caller guarantees exactly numLogFields tokens.
func parseCommitFields(f []string) (model.Commit, error) {
	if len(f) != numLogFields {
		return model.Commit{}, fmt.Errorf("malformed log record: expected %d fields, got %d", numLogFields, len(f))
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
// (expired/revoked variants), E (cannot check), and B (BAD signature) all mean a
// signature is PRESENT — so a rewrite would strip it and the user should be warned.
// N means no signature. B was previously omitted, silently dropping a bad-but-real
// signature without the heads-up every other present-signature state gets.
func signatureStatus(code string) bool {
	switch strings.TrimSpace(code) {
	case "G", "U", "X", "Y", "R", "E", "B":
		return true
	default:
		return false
	}
}
