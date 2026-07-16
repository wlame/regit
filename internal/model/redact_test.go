package model

import "testing"

func TestRedactSpecValidate_acceptsRemoveAndReplace(t *testing.T) {
	rm := RedactSpec{Match: "sk-123", Mode: RedactRemove,
		Targets: map[string][]string{"cfg.yaml": {"token: sk-123"}}}
	if err := rm.Validate(); err != nil {
		t.Fatalf("remove spec: %v", err)
	}
	rp := RedactSpec{Match: "sk-123", Mode: RedactReplace,
		Rule:    RedactRule{Pattern: `sk-\d+`, Replacement: "REDACTED"},
		Targets: map[string][]string{"cfg.yaml": {"token: sk-123"}}}
	if err := rp.Validate(); err != nil {
		t.Fatalf("replace spec: %v", err)
	}
}

func TestRedactSpecValidate_rejectsBadInput(t *testing.T) {
	cases := []struct {
		name string
		s    RedactSpec
	}{
		{"empty match", RedactSpec{Mode: RedactRemove,
			Targets: map[string][]string{"a": {"x"}}}},
		{"no targets", RedactSpec{Match: "s", Mode: RedactRemove}},
		{"empty path", RedactSpec{Match: "s", Mode: RedactRemove,
			Targets: map[string][]string{"": {"x"}}}},
		{"empty line content", RedactSpec{Match: "s", Mode: RedactRemove,
			Targets: map[string][]string{"a": {""}}}},
		{"replace without rule", RedactSpec{Match: "s", Mode: RedactReplace,
			Targets: map[string][]string{"a": {"x"}}}},
		{"replace with bad regex", RedactSpec{Match: "s", Mode: RedactReplace,
			Rule:    RedactRule{Pattern: `([`},
			Targets: map[string][]string{"a": {"x"}}}},
		{"remove with a rule set", RedactSpec{Match: "s", Mode: RedactRemove,
			Rule:    RedactRule{Pattern: `x`},
			Targets: map[string][]string{"a": {"x"}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.s.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
