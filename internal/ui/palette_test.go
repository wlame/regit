package ui

import (
	"strings"
	"testing"
)

func TestHHMM(t *testing.T) {
	cases := map[int]string{0: "00:00", 9 * 60: "09:00", 13*60 + 30: "13:30", 23*60 + 59: "23:59"}
	for min, want := range cases {
		if got := hhmm(min); got != want {
			t.Errorf("hhmm(%d) = %q, want %q", min, got, want)
		}
	}
}

func TestTimeModelHint(t *testing.T) {
	w := timeWindow{start: 9 * 60, end: 18 * 60, lunchStart: 13 * 60, lunchEnd: 14 * 60, jitter: 30}
	cases := map[string]string{
		"Working hours":     "09:00–18:00",
		"Evening hours":     "18:00–23:59",
		"Uniform random":    "00:00–23:59",
		"Preserve original": "original",
	}
	for name, want := range cases {
		if got := timeModelHint(name, w); !strings.Contains(got, want) {
			t.Errorf("timeModelHint(%q) = %q, want it to contain %q", name, got, want)
		}
	}
	// Working hours mentions the lunch gap when set.
	if got := timeModelHint("Working hours", w); !strings.Contains(got, "lunch 13:00–14:00") {
		t.Errorf("Working hours hint should mention the lunch gap, got %q", got)
	}
}
