package ui

import (
	"strings"
	"testing"

	"github.com/wlame/regit/internal/reword"
)

func sampleRewordResult() reword.Result {
	return reword.Result{CommitHash: "abc", Variants: []reword.Variant{
		{Title: "Add retry to the client", Body: "explain why"},
		{Title: "Make the client resilient", Body: ""},
	}}
}

func TestRenderRewordPicker_showsSelectedTitleAndBody(t *testing.T) {
	view := renderRewordPicker(sampleRewordResult(), reword.Selection{TitleIdx: 0, BodyIdx: 0})
	if !strings.Contains(view, "Add retry to the client") || !strings.Contains(view, "explain why") {
		t.Fatalf("picker missing selected content:\n%s", view)
	}
}

func TestRenderRewordPicker_noBody(t *testing.T) {
	view := renderRewordPicker(sampleRewordResult(), reword.Selection{TitleIdx: 1, BodyIdx: 0, NoBody: true})
	if !strings.Contains(view, "Make the client resilient") {
		t.Fatalf("missing title:\n%s", view)
	}
	if !strings.Contains(strings.ToLower(view), "no body") {
		t.Fatalf("expected a 'no body' indicator:\n%s", view)
	}
}
