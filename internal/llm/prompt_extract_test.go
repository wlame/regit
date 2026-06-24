package llm

import "testing"

// TestExtractJSON_invalidInputReturnsEmpty verifies ExtractJSON returns ""
// when the input contains no JSON object delimiters or they are in the wrong
// order. (Moved from the squash package when its extractJSON clone was folded
// into this one.)
func TestExtractJSON_invalidInputReturnsEmpty(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"empty string", ""},
		{"no braces", "just plain text"},
		{"only open brace", "{ no close"},
		{"close before open", "} { misorder "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExtractJSON(tc.input); got != "" {
				t.Errorf("ExtractJSON(%q) = %q, want empty string", tc.input, got)
			}
		})
	}
}

// TestExtractJSON_toleratesSurroundingProse verifies the first-{ to last-}
// span survives reasoning prose around the object.
func TestExtractJSON_toleratesSurroundingProse(t *testing.T) {
	raw := "Sure! Here is the JSON:\n{\"a\": 1}\nHope that helps."
	if got := ExtractJSON(raw); got != `{"a": 1}` {
		t.Errorf("ExtractJSON = %q", got)
	}
}
