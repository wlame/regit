// internal/capability/registry_test.go
package capability

import (
	"testing"

	"github.com/wlame/regit/internal/strategy"
)

func TestRegistryHasBaseVerbs(t *testing.T) {
	for _, name := range []string{"split", "reword"} {
		if _, ok := Lookup(name); !ok {
			t.Errorf("Registry missing %q", name)
		}
	}
	if _, ok := Lookup("nope"); ok {
		t.Error("Lookup(nope) should be false")
	}
}

func TestStrategyNamesMatchEngine(t *testing.T) {
	got := strategyNames()
	want := strategy.Strategies()
	if len(got) != len(want) {
		t.Fatalf("strategyNames len = %d, engine has %d", len(got), len(want))
	}
	// canonName(kebab) must resolve back to the real strategy.
	for _, s := range want {
		if strategyByName(canonName(s.Name())) == nil {
			t.Errorf("strategyByName(canon %q) = nil", s.Name())
		}
	}
}

func TestCanonName(t *testing.T) {
	if canonName("Even Spread") != "Even-Spread" {
		t.Errorf("canon spaced = %q", canonName("Even Spread"))
	}
	if canonName("even-spread") != "Even-Spread" {
		// canonName is case-insensitive on input but yields the registry's exact kebab.
		t.Errorf("canon kebab = %q", canonName("even-spread"))
	}
}
