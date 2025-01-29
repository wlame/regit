package reword

import (
	"strings"
	"testing"
)

func TestParseVariants_ok(t *testing.T) {
	raw := `prose...{"variants":[{"title":"A","body":"a"},{"title":"B","body":""}]} trailing`
	r, err := ParseVariants(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Variants) != 2 || r.Variants[0].Title != "A" || r.Variants[1].Body != "" {
		t.Fatalf("variants = %+v", r.Variants)
	}
}

func TestParseVariants_rejectsEmpty(t *testing.T) {
	if _, err := ParseVariants(`{"variants":[]}`); err == nil {
		t.Fatal("expected error for zero variants")
	}
}

func TestParseVariants_rejectsBlankTitle(t *testing.T) {
	if _, err := ParseVariants(`{"variants":[{"title":"  ","body":"x"}]}`); err == nil {
		t.Fatal("expected error for blank title")
	}
}

func TestParseVariants_trimsMultilineTitle(t *testing.T) {
	raw := `{"variants":[{"title":"First line\nsecond line","body":""}]}`
	r, err := ParseVariants(raw)
	if err != nil {
		t.Fatal(err)
	}
	if r.Variants[0].Title != "First line" {
		t.Fatalf("title = %q, want %q", r.Variants[0].Title, "First line")
	}
}

func TestBuildMessages_includesContext(t *testing.T) {
	_, user := BuildMessages(Request{
		RepoName: "regit", Readme: "A tool", Diff: "@@ -1 +1 @@\n-a\n+b",
		OldSubject: "old", UserHint: "focus on X", Variants: 3,
	})
	for _, want := range []string{"regit", "A tool", "old", "focus on X", "3"} {
		if !strings.Contains(user, want) {
			t.Fatalf("user prompt missing %q:\n%s", want, user)
		}
	}
}
