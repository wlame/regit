package capability

import (
	"bytes"
	"testing"
	"time"

	"github.com/wlame/regit/internal/config"
)

func newReauthorCtx(out *bytes.Buffer, repoDirN int, t *testing.T) (*RunContext, func() (string, string)) {
	_, repo := newTestRepo(t, repoDirN)
	ctx := &RunContext{Repo: repo, Cfg: config.Default(), Stdout: out, Stderr: out, Now: time.Unix(1000, 0)}
	headIdentity := func() (string, string) {
		commits, _ := repo.ReadCommits()
		head := commits[len(commits)-1] // root-first: last = HEAD
		return head.AuthorName, head.AuthorEmail
	}
	return ctx, headIdentity
}

func TestReauthor_requiresNameOrEmail(t *testing.T) {
	var out bytes.Buffer
	ctx, _ := newReauthorCtx(&out, 2, t)
	a, _ := Parse(mustLookup(t, "reauthor"), []string{"--commits=HEAD"})
	err := runReauthor(ctx, a)
	if err == nil || ExitCode(err) != 1 {
		t.Fatalf("expected usage error (exit 1), got %v", err)
	}
}

func TestReauthor_dryRunPreviewsAndDoesNotMoveHead(t *testing.T) {
	var out bytes.Buffer
	ctx, _ := newReauthorCtx(&out, 3, t)
	before, _ := ctx.Repo.ReadState()
	a, _ := Parse(mustLookup(t, "reauthor"), []string{"--name=wlame", "--email=new@x.com", "--commits=HEAD"})
	if err := runReauthor(ctx, a); err != nil {
		t.Fatalf("runReauthor: %v", err)
	}
	after, _ := ctx.Repo.ReadState()
	if before.HeadHash != after.HeadHash {
		t.Error("dry-run reauthor moved HEAD")
	}
	if !bytes.Contains(out.Bytes(), []byte("new@x.com")) {
		t.Errorf("preview should show the new email:\n%s", out.String())
	}
}

func TestReauthor_executeChangesAuthorAndPreservesContent(t *testing.T) {
	var out bytes.Buffer
	ctx, headIdentity := newReauthorCtx(&out, 3, t)
	a, _ := Parse(mustLookup(t, "reauthor"),
		[]string{"--name=wlame", "--email=new@x.com", "--commits=HEAD", "--execute"})
	if err := runReauthor(ctx, a); err != nil {
		t.Fatalf("runReauthor execute: %v", err)
	}
	name, email := headIdentity()
	if name != "wlame" || email != "new@x.com" {
		t.Errorf("HEAD author = %q <%s>, want wlame <new@x.com>", name, email)
	}
	// applyOrPreview prints the verification line, which includes the
	// trees-unchanged check; its presence confirms content was not altered.
	if !bytes.Contains(out.Bytes(), []byte("Applied.")) {
		t.Errorf("expected an apply confirmation:\n%s", out.String())
	}
}

func TestReauthor_emailOnlyPreservesName(t *testing.T) {
	var out bytes.Buffer
	ctx, headIdentity := newReauthorCtx(&out, 2, t)
	// Fixture authors commits as "t". Change only the email.
	a, _ := Parse(mustLookup(t, "reauthor"),
		[]string{"--email=new@x.com", "--commits=HEAD", "--execute"})
	if err := runReauthor(ctx, a); err != nil {
		t.Fatalf("runReauthor: %v", err)
	}
	name, email := headIdentity()
	if name != "t" {
		t.Errorf("name should be preserved, got %q", name)
	}
	if email != "new@x.com" {
		t.Errorf("email should change, got %q", email)
	}
}

func TestReauthorArgMap_emitsCommitterOnlyWhenFalse(t *testing.T) {
	on := ReauthorArgMap("wlame", "new@x", true, "abc123")
	if _, ok := on["committer"]; ok {
		t.Error("committer=true is the default and must be omitted from the arg map")
	}
	off := ReauthorArgMap("wlame", "new@x", false, "abc123")
	if off["committer"] != "false" {
		t.Errorf("committer=false should be emitted, got %q", off["committer"])
	}
	line := Render("reauthor", off)
	if !bytes.Contains([]byte(line), []byte("--committer=false")) {
		t.Errorf("rendered line should contain --committer=false: %s", line)
	}
}
