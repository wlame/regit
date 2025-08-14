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
