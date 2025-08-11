package capability

import (
	"testing"
	"time"
)

func TestParsedArgsAccessors(t *testing.T) {
	day := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)
	a := newParsedArgs(map[string]any{
		"strategy": "Even-Spread",
		"seed":     int(42),
		"execute":  true,
		"scale":    float64(1.5),
		"weekdays": []string{"mon", "tue"},
		"pin":      map[string]int{"2026-03-02": 2},
		"start":    day,
	}, []string{"HEAD"})

	if a.String("strategy") != "Even-Spread" {
		t.Errorf("String = %q", a.String("strategy"))
	}
	if a.Int("seed") != 42 {
		t.Errorf("Int = %d", a.Int("seed"))
	}
	if !a.Bool("execute") {
		t.Error("Bool execute = false")
	}
	if a.Float("scale") != 1.5 {
		t.Errorf("Float = %v", a.Float("scale"))
	}
	if got := a.List("weekdays"); len(got) != 2 || got[0] != "mon" {
		t.Errorf("List = %v", got)
	}
	if got := a.KeyVals("pin"); got["2026-03-02"] != 2 {
		t.Errorf("KeyVals = %v", got)
	}
	if got, ok := a.Date("start"); !ok || !got.Equal(day) {
		t.Errorf("Date = %v ok=%v", got, ok)
	}
	if a.String("missing") != "" || a.Int("missing") != 0 || a.Bool("missing") {
		t.Error("missing keys should return zero values")
	}
	if got := a.Positional; len(got) != 1 || got[0] != "HEAD" {
		t.Errorf("Positional = %v", got)
	}
}
