package strategy

import (
	"math/rand"
	"testing"
	"time"
)

func TestStrategies_allHaveNameAndDescription(t *testing.T) {
	all := Strategies()
	if len(all) != 10 {
		t.Fatalf("expected 10 strategies, got %d", len(all))
	}
	seen := map[string]bool{}
	for _, s := range all {
		if s.Name() == "" || s.Description() == "" {
			t.Errorf("strategy %T missing name or description", s)
		}
		if seen[s.Name()] {
			t.Errorf("duplicate strategy name: %s", s.Name())
		}
		seen[s.Name()] = true
	}
}

func TestTimeModels_allHaveNameAndDescription(t *testing.T) {
	all := TimeModels()
	if len(all) != 6 {
		t.Fatalf("expected 6 time models, got %d", len(all))
	}
	for _, m := range all {
		if m.Name() == "" || m.Description() == "" {
			t.Errorf("time model %T missing name or description", m)
		}
	}
}

func TestManualPin_sharesQuotaSemantics(t *testing.T) {
	commits := makeCommits(2)
	p := paramsRange("2026-03-01", "2026-03-05")
	p.Quotas = map[string]int{"2026-03-02": 2}
	days, err := ManualPin{}.AssignDays(commits, p)
	if err != nil {
		t.Fatal(err)
	}
	if !days[0].Equal(day("2026-03-02")) || !days[1].Equal(day("2026-03-02")) {
		t.Errorf("manual pin should place both on 03-02, got %v", days)
	}
	if (ManualPin{}).Name() == (PerDayQuota{}).Name() {
		t.Error("ManualPin should present a distinct name from PerDayQuota")
	}
}

func TestEveningModel_withinEveningWindow(t *testing.T) {
	commits := makeCommits(10)
	days := make([]time.Time, 10)
	for i := range days {
		days[i] = day("2026-03-02")
	}
	times, _ := EveningModel{}.AssignTimes(commits, days, Params{Location: time.UTC}, rand.New(rand.NewSource(3)))
	for _, tm := range times {
		mins := tm.Hour()*60 + tm.Minute()
		if mins < 18*60 || mins > 23*60 {
			t.Errorf("evening time outside window: %s", tm)
		}
	}
}

func TestJitterModel_centeredWithinSpread(t *testing.T) {
	commits := makeCommits(10)
	days := make([]time.Time, 10)
	for i := range days {
		days[i] = day("2026-03-02")
	}
	p := Params{Location: time.UTC, FixedMin: 12 * 60, JitterMin: 20}
	times, _ := JitterModel{}.AssignTimes(commits, days, p, rand.New(rand.NewSource(5)))
	for _, tm := range times {
		mins := tm.Hour()*60 + tm.Minute()
		if mins < 12*60-20 || mins > 12*60+20 {
			t.Errorf("jitter time outside center +/- spread: %s", tm)
		}
	}
}

func TestPreserveRelative_assignDaysReturnsDayGranularity(t *testing.T) {
	commits := makeCommits(2)
	commits[0].OrigAuthorDate = ts("2026-01-01T05:00:00Z")
	commits[1].OrigAuthorDate = ts("2026-01-03T05:00:00Z")
	p := Params{Location: time.UTC, Anchor: ts("2026-06-01T09:00:00Z"), Scale: 1.0}
	days, err := PreserveRelative{}.AssignDays(commits, p)
	if err != nil {
		t.Fatal(err)
	}
	if !days[0].Equal(day("2026-06-01")) {
		t.Errorf("expected day-truncated anchor, got %s", days[0])
	}
}

func TestLoc_defaultsToLocalWhenNil(t *testing.T) {
	p := Params{}
	if p.Loc() != time.Local {
		t.Errorf("Loc() should default to time.Local")
	}
}

func TestClampMinute_bounds(t *testing.T) {
	if clampMinute(-5) != 0 {
		t.Error("negative minute should clamp to 0")
	}
	if clampMinute(5000) != 1439 {
		t.Error("over-max minute should clamp to 1439")
	}
	if clampMinute(600) != 600 {
		t.Error("in-range minute should pass through")
	}
}

func TestRandMinuteInWindow_skipsLunchGap(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	// Window 12:00-13:00 entirely inside a lunch gap 12:00-13:00 should reflect
	// into the afternoon.
	for i := 0; i < 50; i++ {
		m := randMinuteInWindow(rng, 12*60, 13*60, 12*60, 13*60)
		if m >= 12*60 && m < 13*60 {
			t.Fatalf("minute %d landed inside lunch gap", m)
		}
	}
}
