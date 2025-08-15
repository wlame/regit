package capability

import (
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
	line := Render("redate", RedateArgMap("Manual Pin", "Working hours", p, true))
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
