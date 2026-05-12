package gitio

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wlame/regit/internal/model"
)

// TestReadReplay_preservesSeparatorBytesInMessage proves a commit message that
// contains the ASCII unit/record separators (0x1f / 0x1e) survives a read→replay
// round-trip byte-for-byte. An earlier delimiter scheme used those bytes as field
// and record separators on the false assumption they "can never appear in commit
// metadata": a 0x1f silently truncated the message and a 0x1e made the repo
// unreadable. NUL delimiting removes that class of bug entirely.
func TestReadReplay_preservesSeparatorBytesInMessage(t *testing.T) {
	dir, r := initRepo(t)
	msg := "Subject with \x1f unit and \x1e record seps\n\nBody one\nbody two with \x1f again\n"
	msgFile := filepath.Join(dir, "msg.txt")
	if err := os.WriteFile(msgFile, []byte(msg), 0o644); err != nil {
		t.Fatal(err)
	}
	// --cleanup=verbatim stores the message exactly so we test the parser, not
	// git's default whitespace stripping.
	gitT(t, dir, "commit", "-q", "--allow-empty", "--cleanup=verbatim", "-F", msgFile)

	commits, err := r.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 1 {
		t.Fatalf("expected 1 commit, got %d", len(commits))
	}
	if got, want := commits[0].Message, strings.TrimRight(msg, "\n"); got != want {
		t.Errorf("message not preserved on read:\n got %q\nwant %q", got, want)
	}

	newHead, _, err := r.Replay(buildRedatePlan(t, commits), DropSignatures)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	raw := gitT(t, dir, "cat-file", "commit", newHead)
	if !strings.Contains(raw, "\x1f") || !strings.Contains(raw, "\x1e") {
		t.Errorf("replayed commit lost separator bytes:\n%q", raw)
	}
}

// buildRedatePlan builds a plan that redates every commit by one hour with no
// message edit — the minimal plan shape shared by tests that only care that
// Replay produces one new commit object per input commit (the encoding
// round-trip test and the signing round-trip test).
func buildRedatePlan(t *testing.T, commits []model.Commit) *model.Plan {
	t.Helper()
	plan := model.NewPlan(commits)
	for _, c := range commits {
		nd := c.OrigAuthorDate.Add(time.Hour)
		plan.SetEdit(c.Hash, model.Edit{NewAuthorDate: &nd, NewCommitDate: &nd})
	}
	return plan
}

// TestReplay_nonUTF8MessageStaysReadable proves the fix for the encoding-corruption
// bug. A commit whose message bytes are genuine ISO-8859-1 (é = 0xE9), declared via
// an `encoding ISO-8859-1` header, must survive a rewrite as READABLE text — not
// mojibake. ReadCommits forces --encoding=UTF-8 so the message arrives as UTF-8, and
// Replay stores those UTF-8 bytes with no encoding header. An earlier version kept
// the ISO-8859-1 header on the already-UTF-8 bytes, so every later `git log`
// transcoded them a second time into "Café" garbage.
func TestReplay_nonUTF8MessageStaysReadable(t *testing.T) {
	dir, r := initRepo(t)
	const want = "Café commit"
	msgFile := filepath.Join(dir, "m")
	// Raw ISO-8859-1 bytes: é is the single byte 0xE9, not the 2-byte UTF-8 seq.
	if err := os.WriteFile(msgFile, []byte{'C', 'a', 'f', 0xE9, ' ', 'c', 'o', 'm', 'm', 'i', 't'}, 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, dir, "-c", "i18n.commitEncoding=ISO-8859-1", "commit", "-q", "--allow-empty", "--cleanup=verbatim", "-F", msgFile)

	commits, err := r.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	if got := commits[len(commits)-1].Message; got != want {
		t.Fatalf("read message = %q, want %q", got, want)
	}

	newHead, _, err := r.Replay(buildRedatePlan(t, commits), DropSignatures)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}

	// The rebuilt commit must NOT carry a misleading encoding header...
	if raw := gitT(t, dir, "cat-file", "commit", newHead); strings.Contains(raw, "encoding ISO-8859-1") {
		t.Errorf("replayed commit kept a misleading ISO-8859-1 header:\n%q", raw)
	}
	// ...and a fresh read of the rewritten history must still render correctly.
	gitT(t, dir, "reset", "--hard", newHead)
	reread, err := r.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	if got := reread[len(reread)-1].Message; got != want {
		t.Errorf("re-read message = %q, want %q (mojibake means a double transcode slipped through)", got, want)
	}
}
