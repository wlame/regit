package capability

import "testing"

// testCmd is a small command exercising every relevance/type rule.
var testCmd = Command{
	Name: "demo",
	Params: []Param{
		{Name: "strategy", Type: ParamEnum, Enum: strategyNames(), Required: true, Help: "x"},
		{Name: "pin", Type: ParamKeyVals, AppliesIf: "strategy=Manual-Pin", Help: "x"},
		{Name: "seed", Type: ParamInt, Default: "0", Help: "x"},
		{Name: "execute", Type: ParamBool, Default: "false", Help: "x"},
		{Name: "weekdays", Type: ParamStringList, Help: "x"},
	},
}

func TestParseTypedValuesAndPositionals(t *testing.T) {
	a, err := Parse(testCmd, []string{"HEAD", "--strategy=even-spread", "--seed=7", "--execute", "--weekdays=mon,tue"})
	if err != nil {
		t.Fatalf("Parse err: %v", err)
	}
	if a.String("strategy") != "Even-Spread" { // canonicalized
		t.Errorf("strategy = %q", a.String("strategy"))
	}
	if a.Int("seed") != 7 || !a.Bool("execute") {
		t.Errorf("seed/execute wrong")
	}
	if got := a.List("weekdays"); len(got) != 2 {
		t.Errorf("weekdays = %v", got)
	}
	if len(a.Positional) != 1 || a.Positional[0] != "HEAD" {
		t.Errorf("positional = %v", a.Positional)
	}
}

func TestParseMissingRequiredErrors(t *testing.T) {
	if _, err := Parse(testCmd, []string{"--seed=1"}); err == nil {
		t.Error("expected missing-required error for --strategy")
	}
}

func TestParseBadEnumErrors(t *testing.T) {
	if _, err := Parse(testCmd, []string{"--strategy=Nope"}); err == nil {
		t.Error("expected bad-enum error")
	}
}

func TestParseAppliesIfViolation(t *testing.T) {
	// --pin only applies with strategy=Manual-Pin; supplying it under Even-Spread is an error.
	if _, err := Parse(testCmd, []string{"--strategy=Even-Spread", "--pin=2026-03-02=2"}); err == nil {
		t.Error("expected AppliesIf violation for --pin under Even-Spread")
	}
}
