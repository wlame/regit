package capability

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wlame/regit/internal/strategy"
)

func TestRedateArgMapRoundTrips(t *testing.T) {
	p := strategy.Params{
		Start:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		End:    time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		Quotas: map[string]int{"2026-03-02": 2},
	}
	line := Render("redate", RedateArgMap("Manual Pin", "Working hours", p, true, ""))
	parsed, err := Parse(mustLookup(t, "redate"), tokenize(line)[2:])
	if err != nil {
		t.Fatalf("re-parse %q: %v", line, err)
	}
	if parsed.String("strategy") != "Manual-Pin" {
		t.Errorf("strategy = %q", parsed.String("strategy"))
	}
	if parsed.KeyVals("pin")["2026-03-02"] != 2 {
		t.Errorf("pin lost: %q", line)
	}
}

// TestRedateArgMap_everyTimeModelRoundTrips renders a redate line for every time
// model and re-parses it. This is the mechanical guard the missing --time-fixed bug
// slipped past: a Fixed-time run used to render a line Parse rejected ("--time-fixed
// is required"), breaking the footer's round-trip guarantee.
func TestRedateArgMap_everyTimeModelRoundTrips(t *testing.T) {
	p := strategy.Params{
		Start:     time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		End:       time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		FixedMin:  10*60 + 30, // 10:30 — exercised by Fixed-time
		JitterMin: 25,         // exercised by Jitter
		Seed:      42,         // every randomized strategy must round-trip its seed
	}
	for _, tm := range strategy.TimeModels() {
		name := tm.Name()
		t.Run(name, func(t *testing.T) {
			line := Render("redate", RedateArgMap("Even Spread", name, p, true, ""))
			parsed, err := Parse(mustLookup(t, "redate"), tokenize(line)[2:])
			if err != nil {
				t.Fatalf("time model %q rendered a line the CLI rejects: %v\n  %s", name, err, line)
			}
			if canonName(parsed.String("time")) != canonName(name) {
				t.Errorf("time model lost on round-trip: got %q from %q", parsed.String("time"), line)
			}
			if parsed.Int("seed") != 42 {
				t.Errorf("seed not preserved for %q: %s", name, line)
			}
		})
	}
}

// TestRewordArgMapMultiPositionalRoundTrips: a multi-commit reword command must
// carry every commit as a positional and round-trip back through Parse.
func TestRewordArgMapMultiPositionalRoundTrips(t *testing.T) {
	line := Render("reword", RewordArgMap("abc123", "def456"))
	toks := tokenize(line)
	if len(toks) < 2 || toks[0] != "regit" || toks[1] != "reword" {
		t.Fatalf("tokens = %v", toks)
	}
	parsed, err := Parse(mustLookup(t, "reword"), toks[2:])
	if err != nil {
		t.Fatalf("re-parse %q: %v", line, err)
	}
	if len(parsed.Positional) != 2 || parsed.Positional[0] != "abc123" || parsed.Positional[1] != "def456" {
		t.Errorf("positionals = %v from %q", parsed.Positional, line)
	}
	if !parsed.Bool("execute") {
		t.Errorf("execute lost: %q", line)
	}
}

func TestMaskSecretArgs_hidesTheSecretInEveryValue(t *testing.T) {
	args := RedactArgMap("sk-live-123", []string{"*.env"}, "replace", `sk-live-123|sk-live-\d+`, "X", true)
	masked := MaskSecretArgs(args, "sk-live-123")
	line := Render("redact", masked)
	if strings.Contains(line, "sk-live-123") {
		t.Fatalf("masked line still contains the secret: %q", line)
	}
	if masked["match"] != SecretPlaceholder {
		t.Errorf("match = %q, want %q", masked["match"], SecretPlaceholder)
	}
	if masked["replace-pattern"] != SecretPlaceholder+`|sk-live-\d+` {
		t.Errorf("replace-pattern = %q, want the secret replaced in place", masked["replace-pattern"])
	}
	if args["match"] != "sk-live-123" {
		t.Error("MaskSecretArgs must not modify its input map")
	}
}

func TestRedateArgMap_spreadRoundTrips(t *testing.T) {
	p := strategy.Params{Picked: map[string]bool{"2026-03-02": true}, Spread: 40, Seed: 9}

	line := Render("redate", RedateArgMap("Picked Days", "Working hours", p, true, ""))
	parsed, err := Parse(mustLookup(t, "redate"), tokenize(line)[2:])

	if err != nil {
		t.Fatalf("re-parse %q: %v", line, err)
	}
	if parsed.Int("spread") != 40 {
		t.Errorf("spread lost: %q", line)
	}
}

func TestRedate_spreadAppliesToTheRandomizableStrategiesOnly(t *testing.T) {
	cases := []struct {
		strategy string
		ok       bool
	}{
		{"Picked-Days", true}, {"Even-Spread", true}, {"Working-Days", true}, {"Weekends-Only", true},
		{"Rate-Fill", false}, {"Manual-Pin", false},
	}
	for _, tc := range cases {
		args := []string{"--strategy=" + tc.strategy, "--spread=40",
			"--start=2026-03-02", "--end=2026-03-15", "--pick=2026-03-02", "--pin=2026-03-02=1"}
		if tc.strategy != "Picked-Days" {
			args = slices.DeleteFunc(args, func(a string) bool { return strings.HasPrefix(a, "--pick") })
		}
		if tc.strategy != "Manual-Pin" {
			args = slices.DeleteFunc(args, func(a string) bool { return strings.HasPrefix(a, "--pin") })
		}
		_, err := Parse(mustLookup(t, "redate"), args)
		if (err == nil) != tc.ok {
			t.Errorf("%s with --spread: err = %v, want ok=%v", tc.strategy, err, tc.ok)
		}
	}
}

// TestRedateArgMap_zeroSpreadIsLogged: with a default of 50, leaving --spread
// out would replay a spread-0 plan as 50, so 0 must be written out.
func TestRedateArgMap_zeroSpreadIsLogged(t *testing.T) {
	p := strategy.Params{Picked: map[string]bool{"2026-03-02": true}, Spread: 0, Seed: 9}
	if line := Render("redate", RedateArgMap("Picked Days", "Working hours", p, true, "")); !strings.Contains(line, "--spread=0") {
		t.Errorf("spread 0 must be logged explicitly: %q", line)
	}
	p.Spread = 50
	if line := Render("redate", RedateArgMap("Picked Days", "Working hours", p, true, "")); strings.Contains(line, "--spread") {
		t.Errorf("the default spread needs no flag: %q", line)
	}
}
