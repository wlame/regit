package llm

import (
	"strings"
	"testing"

	"github.com/wlame/regit/internal/model"
)

// TestTruncateForPrompt verifies the prompt-diff cap: text within the budget is
// untouched; oversized text is cut at a line boundary and carries a marker so
// the model knows it is seeing an excerpt.
func TestTruncateForPrompt(t *testing.T) {
	small := "line one\nline two\n"
	if got := TruncateForPrompt(small, 1024); got != small {
		t.Errorf("small input should be unchanged, got %q", got)
	}

	big := strings.Repeat("0123456789abcdef\n", 100) // 1700 bytes
	got := TruncateForPrompt(big, 500)
	if len(got) > 500+80 { // budget + marker line
		t.Errorf("truncated output too long: %d bytes", len(got))
	}
	if !strings.Contains(got, "diff truncated") {
		t.Errorf("truncated output missing the excerpt marker: %q", got)
	}
	// The cut must land on a line boundary: the last content line before the
	// marker is a complete 16-char line, not a fragment.
	lines := strings.Split(got, "\n")
	last := lines[len(lines)-2] // line before the marker
	if last != "0123456789abcdef" {
		t.Errorf("cut did not land on a line boundary: %q", last)
	}

	// Multi-byte runes are never split: a budget landing mid-rune backs up.
	utf := strings.Repeat("é", 300) // 600 bytes, no newlines
	cut := TruncateForPrompt(utf, 501)
	trimmed := strings.SplitN(cut, "\n", 2)[0]
	if !strings.HasSuffix(trimmed, "é") || len(trimmed)%2 != 0 {
		t.Errorf("cut split a UTF-8 sequence: %q…", trimmed[len(trimmed)-8:])
	}
}

// TestDiffShareBytes verifies the per-item budget split and its floor.
func TestDiffShareBytes(t *testing.T) {
	if got := DiffShareBytes(0); got != MaxPromptDiffBytes {
		t.Errorf("DiffShareBytes(0) = %d, want full budget", got)
	}
	if got := DiffShareBytes(2); got != MaxPromptDiffBytes/2 {
		t.Errorf("DiffShareBytes(2) = %d, want half budget", got)
	}
	if got := DiffShareBytes(1_000_000); got != minDiffShareBytes {
		t.Errorf("DiffShareBytes(huge) = %d, want floor %d", got, minDiffShareBytes)
	}
}

// TestBuildMessages_capsHunkBodies verifies a giant hunk cannot blow up the
// split prompt: every hunk header still appears (grouping needs the full ID
// list) but bodies are excerpted under the shared budget.
func TestBuildMessages_capsHunkBodies(t *testing.T) {
	req := SplitRequest{
		CommitMessage: "big",
		MaxCommits:    4,
		Hunks:         hunksWithGiantBody(t),
	}
	_, user := BuildMessages(req)
	if len(user) > MaxPromptDiffBytes+4096 {
		t.Errorf("split prompt not capped: %d bytes", len(user))
	}
	if !strings.Contains(user, "H1") || !strings.Contains(user, "H2") {
		t.Errorf("hunk IDs missing from capped prompt")
	}
	if !strings.Contains(user, "diff truncated") {
		t.Errorf("capped prompt missing the excerpt marker")
	}
}

// hunksWithGiantBody builds two hunks whose combined bodies far exceed the
// prompt budget.
func hunksWithGiantBody(t *testing.T) []model.Hunk {
	t.Helper()
	giant := strings.Repeat("+added line of code\n", 20_000) // ~400 KiB each
	return []model.Hunk{
		{ID: "H1", File: "a.go", Additions: 20000, Body: giant},
		{ID: "H2", File: "b.go", Additions: 20000, Body: giant},
	}
}
