package capability

import (
	"bytes"
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
