package capability

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Parse turns a verb's args into typed ParsedArgs. Leading and trailing non-flag
// tokens become Positional (so `reword HEAD --execute foo` collects HEAD and foo).
// Each --name=value is parsed per its Param.Type; enum values are canonicalized.
// Parse then runs Validate. Long-form flags MUST use =name=value form.
func Parse(c Command, args []string) (ParsedArgs, error) {
	byName := map[string]Param{}
	for _, p := range c.Params {
		byName[p.Name] = p
	}

	values := map[string]any{}
	var positional []string

	for _, tok := range args {
		if !strings.HasPrefix(tok, "-") {
			positional = append(positional, tok)
			continue
		}
		name, raw, hasEq := strings.Cut(strings.TrimLeft(tok, "-"), "=")
		p, known := byName[name]
		if !known {
			return ParsedArgs{}, fmt.Errorf("unknown flag --%s for %q", name, c.Name)
		}
		// Bools may appear bare (--execute) or as --execute=true.
		if p.Type == ParamBool && !hasEq {
			raw = "true"
		} else if !hasEq {
			return ParsedArgs{}, fmt.Errorf("flag --%s needs a value: use --%s=VALUE", name, name)
		}
		v, err := parseValue(p, raw)
		if err != nil {
			return ParsedArgs{}, fmt.Errorf("--%s: %w", name, err)
		}
		values[name] = v
	}

	a := newParsedArgs(values, positional)
	if err := Validate(c, a); err != nil {
		return ParsedArgs{}, err
	}
	return a, nil
}

// parseValue converts a raw flag string to the typed value for p.Type.
func parseValue(p Param, raw string) (any, error) {
	switch p.Type {
	case ParamString, ParamClock:
		return raw, nil
	case ParamEnum:
		return canonName(raw), nil
	case ParamInt:
		n, err := strconv.Atoi(raw)
		if err != nil {
			return nil, fmt.Errorf("expected an integer, got %q", raw)
		}
		return n, nil
	case ParamFloat:
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, fmt.Errorf("expected a number, got %q", raw)
		}
		return f, nil
	case ParamBool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("expected true/false, got %q", raw)
		}
		return b, nil
	case ParamStringList:
		return splitList(raw), nil
	case ParamKeyVals:
		return parseKeyVals(raw)
	case ParamDate:
		t, err := time.Parse("2006-01-02", raw)
		if err != nil {
			return nil, fmt.Errorf("expected a YYYY-MM-DD date, got %q", raw)
		}
		return t, nil
	}
	return nil, fmt.Errorf("unsupported param type")
}

// Validate enforces Required, Enum membership, and AppliesIf relevance from the
// registry data — no per-flag branches. A required-and-applicable flag that is
// absent, or an applicable flag whose AppliesIf predicate is false, is an error.
func Validate(c Command, a ParsedArgs) error {
	for _, p := range c.Params {
		applicable := relevanceHolds(p.AppliesIf, a)
		if a.Has(p.Name) && !applicable {
			return fmt.Errorf("--%s only applies when %s", p.Name, humanPredicate(p.AppliesIf))
		}
		if p.Required && applicable && !a.Has(p.Name) {
			if p.AppliesIf == "" {
				return fmt.Errorf("--%s is required for %q", p.Name, c.Name)
			}
			return fmt.Errorf("--%s is required when %s", p.Name, humanPredicate(p.AppliesIf))
		}
		if p.Type == ParamEnum && a.Has(p.Name) {
			if !contains(p.Enum, a.String(p.Name)) {
				return fmt.Errorf("--%s must be one of: %s", p.Name, strings.Join(p.Enum, " "))
			}
		}
	}
	return nil
}

// relevanceHolds evaluates an AppliesIf predicate "flag=value" against parsed
// args. Empty predicate = always applicable.
func relevanceHolds(pred string, a ParsedArgs) bool {
	if pred == "" {
		return true
	}
	key, want, _ := strings.Cut(pred, "=")
	return canonName(a.String(key)) == canonName(want)
}

func humanPredicate(pred string) string {
	key, want, _ := strings.Cut(pred, "=")
	return fmt.Sprintf("--%s=%s", key, want)
}

func splitList(raw string) []string {
	var out []string
	for _, p := range strings.Split(raw, ",") {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// parseKeyVals parses "DAY=N,DAY=N" into a map (used by --pin).
func parseKeyVals(raw string) (map[string]int, error) {
	out := map[string]int{}
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			return nil, fmt.Errorf("expected DAY=N pairs, got %q", pair)
		}
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return nil, fmt.Errorf("count in %q is not an integer", pair)
		}
		out[strings.TrimSpace(k)] = n
	}
	return out, nil
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
