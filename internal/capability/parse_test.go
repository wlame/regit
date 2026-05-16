package capability

import "testing"

// testCmd is a small command exercising every relevance/type rule. Its
// variadic positional slot lets the positional-collection tests keep passing
// under the arity validation.
var testCmd = Command{
	Name:       "demo",
	Positional: &PositionalSpec{Name: "ref", Help: "x", Variadic: true},
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

func TestParseTrailingPositional(t *testing.T) {
	a, err := Parse(testCmd, []string{"--strategy=Even-Spread", "--execute", "HEAD"})
	if err != nil {
		t.Fatalf("Parse err: %v", err)
	}
	if len(a.Positional) != 1 || a.Positional[0] != "HEAD" {
		t.Errorf("trailing positional = %v", a.Positional)
	}
}

func TestParseLeadingAndTrailingPositional(t *testing.T) {
	a, err := Parse(testCmd, []string{"A", "--strategy=Even-Spread", "B"})
	if err != nil {
		t.Fatalf("Parse err: %v", err)
	}
	if len(a.Positional) != 2 || a.Positional[0] != "A" || a.Positional[1] != "B" {
		t.Errorf("positionals = %v", a.Positional)
	}
}

// TestValidate_positionalArity verifies the registry-driven positional rules:
// a command without a slot rejects stray tokens, a required slot must be
// filled, and a single-value slot rejects extras (all were silently ignored
// or dropped before).
func TestValidate_positionalArity(t *testing.T) {
	redate, _ := Lookup("redate")
	if _, err := Parse(redate, []string{"HEAD", "--strategy=Even-Spread"}); err == nil {
		t.Error("redate should reject a positional argument")
	}

	squash, _ := Lookup("squash")
	if _, err := Parse(squash, []string{}); err == nil {
		t.Error("squash should require a <range> argument")
	}

	split, _ := Lookup("split")
	if _, err := Parse(split, []string{"HEAD", "HEAD~1"}); err == nil {
		t.Error("split should reject a second positional (it takes exactly one)")
	}
	if _, err := Parse(split, []string{}); err != nil {
		t.Errorf("split without a positional defaults to HEAD, got: %v", err)
	}

	reword, _ := Lookup("reword")
	if _, err := Parse(reword, []string{"HEAD~2", "HEAD"}); err != nil {
		t.Errorf("reword is variadic, got: %v", err)
	}
}
