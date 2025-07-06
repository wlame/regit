package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/wlame/regit/internal/config"
	"github.com/wlame/regit/internal/strategy"
)

// EvenSpreadFixture returns the Even Spread strategy (the first in the registry).
func EvenSpreadFixture() strategy.Strategy {
	return strategy.Strategies()[0]
}

func TestParamsForm_ready(t *testing.T) {
	cfg := config.Default()
	cfg.Timezone = "UTC"
	f := newParamsForm(EvenSpreadFixture(), cfg, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	if f.ready() {
		t.Error("a fresh date-range form (unset dates) should not be ready")
	}
	f.inputs[0].SetValue("2026-06-01")
	f.inputs[1].SetValue("2026-06-30")
	if !f.ready() {
		t.Error("both dates set -> ready")
	}
}

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
	// With fixed==0, Jitter centers on the working-window midpoint (13:30) ± jitter.
	if got := timeModelHint("Jitter", w); !strings.Contains(got, "13:30") || !strings.Contains(got, "30 min") {
		t.Errorf("Jitter hint should center on the midpoint ± jitter, got %q", got)
	}
	// With fixed==0, Fixed time falls back to 00:00.
	if got := timeModelHint("Fixed time", w); got != "at 00:00" {
		t.Errorf("Fixed time hint = %q, want \"at 00:00\"", got)
	}
}
