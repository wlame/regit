package capability

import (
	"bytes"
	"strings"
	"testing"
)

func TestDeprecatedRewriteYesBecomesExecute(t *testing.T) {
	var warn bytes.Buffer
	got := DeprecatedRewrite("split", []string{"HEAD", "--yes"}, &warn)
	if len(got) != 2 || got[1] != "--execute" {
		t.Errorf("got %v", got)
	}
	if warn.Len() == 0 {
		t.Error("expected a deprecation notice")
	}
}

func TestDeprecatedRewriteDryRunDropped(t *testing.T) {
	var warn bytes.Buffer
	got := DeprecatedRewrite("reword", []string{"--dry-run", "HEAD"}, &warn)
	if len(got) != 1 || got[0] != "HEAD" {
		t.Errorf("got %v", got)
	}
}

func TestDeprecatedRewriteLeavesOtherVerbs(t *testing.T) {
	got := DeprecatedRewrite("redate", []string{"--yes"}, &bytes.Buffer{})
	if len(got) != 1 || got[0] != "--yes" {
		t.Errorf("non-legacy verb should be untouched: %v", got)
	}
}

func TestDeprecatedRewriteYesTrueBecomesExecute(t *testing.T) {
	var warn bytes.Buffer
	got := DeprecatedRewrite("split", []string{"HEAD", "--yes=true"}, &warn)
	if len(got) != 2 || got[0] != "HEAD" || got[1] != "--execute" {
		t.Errorf("got %v", got)
	}
	if warn.Len() == 0 {
		t.Error("expected a deprecation notice")
	}
}

func TestDeprecatedRewriteDryRunTrueDropped(t *testing.T) {
	got := DeprecatedRewrite("reword", []string{"--dry-run=true", "HEAD"}, &bytes.Buffer{})
	if len(got) != 1 || got[0] != "HEAD" {
		t.Errorf("got %v", got)
	}
}

func TestDeprecatedRewriteYesFalseStaysDryRun(t *testing.T) {
	got := DeprecatedRewrite("split", []string{"--yes=false", "HEAD"}, &bytes.Buffer{})
	for _, a := range got {
		if a == "--execute" {
			t.Errorf("--yes=false must not become --execute: %v", got)
		}
	}
}

func TestDeprecatedRewriteBarePositionalNotRewritten(t *testing.T) {
	var warn bytes.Buffer
	// A commit-ish literally named "yes" is a positional, not the deprecated flag:
	// it must pass through unchanged and emit no notice.
	got := DeprecatedRewrite("split", []string{"yes"}, &warn)
	if len(got) != 1 || got[0] != "yes" {
		t.Errorf("bare positional should pass through unchanged, got %v", got)
	}
	if warn.Len() != 0 {
		t.Errorf("bare positional should not trigger a deprecation notice, got %q", warn.String())
	}
}

// TestDeprecatedRewrite_dryRunFalseBecomesExecute verifies the legacy "apply"
// spelling is honored: --dry-run=false translates to --execute instead of
// being silently dropped (which turned an intended apply into a preview).
func TestDeprecatedRewrite_dryRunFalseBecomesExecute(t *testing.T) {
	var warn bytes.Buffer
	got := DeprecatedRewrite("split", []string{"HEAD", "--dry-run=false"}, &warn)
	found := false
	for _, a := range got {
		if a == "--execute" {
			found = true
		}
		if strings.HasPrefix(a, "--dry-run") {
			t.Errorf("--dry-run should not survive the rewrite: %v", got)
		}
	}
	if !found {
		t.Errorf("--dry-run=false should translate to --execute: %v", got)
	}
	if !strings.Contains(warn.String(), "--execute") {
		t.Errorf("expected a translation notice, got: %s", warn.String())
	}
}
