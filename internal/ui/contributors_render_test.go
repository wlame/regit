package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/wlame/regit/internal/model"
)

// twoAuthorModel builds a model with three commits by two distinct authors and
// rebuilds the contributor index (tests that overwrite m.plan must rebuild, just
// as New/reload do). Author column is turned on so the list renders names.
func twoAuthorModel(t *testing.T) Model {
	t.Helper()
	m := newTestModelN(t, 1) // reuse scaffolding; overwrite the plan below
	day := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	mk := func(hash, name, email string) model.Commit {
		return model.Commit{Hash: hash, Subject: "s " + name, AuthorName: name,
			AuthorEmail: email, OrigAuthorDate: day, OrigCommitDate: day}
	}
	m.plan = model.NewPlan([]model.Commit{
		mk("aaaa111111111111111111111111111111111111", "alice", "alice@x.com"),
		mk("bbbb222222222222222222222222222222222222", "bob", "bob@x.com"),
		mk("cccc333333333333333333333333333333333333", "alice", "alice@x.com"),
	})
	m.rebuildContributors()
	m.showAuthor = true
	m.listCursor = 0
	return m
}

func TestStyleAuthor_colorsDistinctContributorsDifferently(t *testing.T) {
	orig := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(orig)

	m := twoAuthorModel(t)
	alice := m.plan.Commits[0] // alice@x.com
	bob := m.plan.Commits[1]   // bob@x.com
	aOut := m.styleAuthor(alice, "alice ")
	bOut := m.styleAuthor(bob, "bob   ")
	if aOut == "" || bOut == "" {
		t.Fatal("styled author cells should be non-empty")
	}
	if stripANSI(aOut) == aOut {
		t.Fatal("expected color escape codes in the styled author cell")
	}
	// Different contributors → different color codes → different rendered bytes
	// even after normalizing the (different) label text.
	if colorCodesOf(aOut) == colorCodesOf(bOut) {
		t.Errorf("two contributors must render in different colors:\n a=%q\n b=%q", aOut, bOut)
	}
}

func TestStyleAuthor_emptyTextStaysEmpty(t *testing.T) {
	m := twoAuthorModel(t)
	if got := m.styleAuthor(m.plan.Commits[0], ""); got != "" {
		t.Errorf("empty author cell must stay empty, got %q", got)
	}
}

// stripANSI removes CSI SGR sequences so tests can compare on visible text.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b { // ESC — skip to the terminating 'm'
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// colorCodesOf returns just the escape sequences of s (its color "fingerprint"),
// so two renders can be compared for color regardless of their visible text.
func colorCodesOf(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				b.WriteByte(s[i])
				i++
			}
			if i < len(s) {
				b.WriteByte('m')
			}
		}
	}
	return b.String()
}

func TestHashStyleFor_tintsRowsSharingCursorAuthor(t *testing.T) {
	orig := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(orig)

	m := twoAuthorModel(t)
	// Commits (root-first): [0]=alice, [1]=bob, [2]=alice. Newest (display 0) is [2] alice.
	m.listCursor = 0 // cursor on newest = alice
	alice := m.plan.Commits[2]
	bob := m.plan.Commits[1]

	aliceHash := m.hashStyleFor(alice).Render("cccc333")
	bobHash := m.hashStyleFor(bob).Render("bbbb222")
	defaultHash := m.st.hash.Render("x")

	// Alice shares the cursor author → tinted (not the default hash color).
	if colorCodesOf(aliceHash) == colorCodesOf(m.st.hash.Render("cccc333")) {
		t.Error("a row sharing the cursor author should not use the default hash color")
	}
	// Bob does not share it → default hash color.
	if colorCodesOf(bobHash) != colorCodesOf(defaultHash) {
		t.Errorf("a non-matching row should keep the default hash color:\n b=%q def=%q",
			bobHash, defaultHash)
	}
}

func TestHashStyleFor_followsCursorMove(t *testing.T) {
	orig := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(orig)

	m := twoAuthorModel(t)
	bob := m.plan.Commits[1]
	// With the cursor on an alice commit, bob's hash is the default color.
	m.listCursor = 0
	before := colorCodesOf(m.hashStyleFor(bob).Render("h"))
	// Move the cursor to bob's row (display index 1 = commit [1] = bob).
	m.listCursor = 1
	after := colorCodesOf(m.hashStyleFor(bob).Render("h"))
	if before == after {
		t.Error("moving the cursor onto bob should change bob's hash tint")
	}
}
