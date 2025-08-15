// Package capability is regit's single source of truth for every non-interactive
// action. Each action is a Command row of data (name, typed params, examples,
// limits) plus a headless Run. The CLI dispatch, the TUI feature menu, the
// transparency footer, and the --help / --llm-help docs are all PROJECTIONS of
// this registry, so they cannot drift apart.
//
// Layering: capability imports the engine packages (model, strategy, gitio,
// safety, split, reword, llm, config) but MUST NOT import internal/ui. The TUI
// imports capability.
package capability

import (
	"io"
	"time"

	"github.com/wlame/regit/internal/config"
	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/llm"
	"github.com/wlame/regit/internal/model"
)

// ParamType enumerates the value kinds a flag can hold. Held as data so the
// flag-set builder, validator, help renderer, and footer renderer each switch on
// one enum instead of duplicating per-flag logic.
type ParamType int

const (
	ParamString     ParamType = iota // plain string
	ParamInt                         // integer
	ParamBool                        // boolean toggle (--execute)
	ParamFloat                       // float (--scale)
	ParamEnum                        // one of Param.Enum (--strategy)
	ParamStringList                  // comma-separated (--weekdays=mon,tue)
	ParamKeyVals                     // DAY=N,DAY=N (--pin)
	ParamDate                        // YYYY-MM-DD
	ParamClock                       // HH:MM (kept as a string; verbs parse to minutes)
)

// Param describes one flag: the atom every agent surface is built from.
type Param struct {
	Name      string    // long flag; rendered as --Name
	Type      ParamType // value kind
	Default   string    // default for help display; the footer omits a flag equal to it
	Required  bool      // a missing required (and applicable) flag is a usage error
	Enum      []string  // allowed values when Type == ParamEnum
	Help      string    // one-line purpose, shown in --help and --llm-help
	AppliesIf string    // relevance predicate "flag=value" (e.g. "strategy=Manual-Pin");
	// drives BOTH validation and help. Empty = always applies.
}

// Example is a correct or incorrect invocation, annotated with why (for --llm-help).
type Example struct {
	Cmd string // the command line
	Why string // why it is correct, or why it is wrong
	Bad bool   // true = an anti-pattern shown as "don't do this"
}

// Command is one registry row. Metadata is pure data; Run is the impure execution.
type Command struct {
	Name        string                              // verb: "redate", "split", "retitle", "undo"
	Summary     string                              // one-line purpose → terse --help
	Mutating    bool                                // false = read-only; true = writes the repo
	Params      []Param                             // typed flags
	Examples    []Example                           // correct AND incorrect, annotated
	Limitations []string                            // constraints / side-effects / ordering
	SampleOut   string                              // representative output snippet
	ExitCodes   map[int]string                      // documented exit codes
	Workspace   WorkspaceID                         // TUI tab for the menu projection; WSNone if headless-only
	Selection   SelectionMode                       // what the TUI needs selected before running
	Run         func(*RunContext, ParsedArgs) error `json:"-"`
}

// RunContext is the injectable environment a Run executes against, so Run stays
// testable (swap Stdout, inject a stub repo/backend, pin Now for determinism).
type RunContext struct {
	Repo    *gitio.Repo
	Cfg     config.Config
	Stdout  io.Writer
	Stderr  io.Writer
	Stdin   io.Reader
	Now     time.Time   // never call time.Now() inside Run; use this
	Backend llm.Backend // nil unless the verb needs an LLM (built by cmd/regit)
}

// ParsedArgs gives Run typed access to flag values and positionals.
type ParsedArgs struct {
	Positional []string
	values     map[string]any
}

// newParsedArgs builds a ParsedArgs; used by Parse and by tests.
func newParsedArgs(values map[string]any, positional []string) ParsedArgs {
	if values == nil {
		values = map[string]any{}
	}
	return ParsedArgs{Positional: positional, values: values}
}

func (a ParsedArgs) String(name string) string {
	if v, ok := a.values[name].(string); ok {
		return v
	}
	return ""
}

func (a ParsedArgs) Int(name string) int {
	if v, ok := a.values[name].(int); ok {
		return v
	}
	return 0
}

func (a ParsedArgs) Bool(name string) bool {
	if v, ok := a.values[name].(bool); ok {
		return v
	}
	return false
}

func (a ParsedArgs) Float(name string) float64 {
	if v, ok := a.values[name].(float64); ok {
		return v
	}
	return 0
}

func (a ParsedArgs) List(name string) []string {
	if v, ok := a.values[name].([]string); ok {
		return v
	}
	return nil
}

func (a ParsedArgs) KeyVals(name string) map[string]int {
	if v, ok := a.values[name].(map[string]int); ok {
		return v
	}
	return nil
}

func (a ParsedArgs) Date(name string) (time.Time, bool) {
	if v, ok := a.values[name].(time.Time); ok {
		return v, true
	}
	return time.Time{}, false
}

// Has reports whether a value was set for name (used by Render to decide whether
// to emit a flag). A zero-but-present value still counts as set.
func (a ParsedArgs) Has(name string) bool {
	_, ok := a.values[name]
	return ok
}

// Ensure the model import is used even before any Run consumes it.
var _ = model.Commit{}
