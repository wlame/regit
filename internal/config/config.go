// Package config loads regit's user-tunable defaults. Resolution order, lowest
// to highest precedence: built-in defaults -> YAML file -> environment
// variables. The YAML file lives at $XDG_CONFIG_HOME/regit/config.yaml (falling
// back to ~/.config/regit/config.yaml).
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

// LLMConfig configures the optional LLM used for commit splitting. Off unless a
// provider/model is set or --llm is passed. API keys come only from env.
type LLMConfig struct {
	Provider    string  `yaml:"provider"`
	Model       string  `yaml:"model"`
	BaseURL     string  `yaml:"base_url"`
	Temperature float64 `yaml:"temperature"`
	TimeoutSec  int     `yaml:"timeout_sec"`
}

// SplitConfig holds defaults for the commit-splitting feature.
type SplitConfig struct {
	MaxCommits     int      `yaml:"max_commits"`
	LinesPerCommit int      `yaml:"lines_per_commit"`
	MinSplitLines  int      `yaml:"min_split_lines"`
	RecursionDepth int      `yaml:"recursion_depth"`
	Context        []string `yaml:"context"`
	RepairAttempts int      `yaml:"repair_attempts"`
	WIPPrefix      string   `yaml:"wip_prefix"`
	Cache          bool     `yaml:"cache"`
}

// RewordConfig holds defaults for the LLM-backed commit-rewording feature.
type RewordConfig struct {
	Variants     int      `yaml:"variants"`
	Context      []string `yaml:"context"`
	ContextLines int      `yaml:"context_lines"`
	Prefetch     int      `yaml:"prefetch"`
	Cache        bool     `yaml:"cache"`
}

// Config holds every default the TUI seeds its forms with. Times-of-day are
// stored as "HH:MM" strings for human-friendly YAML and parsed to minutes via
// the accessor methods.
type Config struct {
	// Timezone is an IANA name (e.g. "Europe/Berlin") or "local".
	Timezone string `yaml:"timezone"`

	// WorkdayStart / WorkdayEnd bound the default working-hours window.
	WorkdayStart string `yaml:"workday_start"`
	WorkdayEnd   string `yaml:"workday_end"`

	// LunchStart / LunchEnd describe an optional gap to avoid; empty disables it.
	LunchStart string `yaml:"lunch_start"`
	LunchEnd   string `yaml:"lunch_end"`

	// DefaultStrategy / DefaultTimeModel select the palette entries pre-selected
	// on launch (matched against Strategy.Name() / TimeModel.Name()).
	DefaultStrategy  string `yaml:"default_strategy"`
	DefaultTimeModel string `yaml:"default_time_model"`

	// JitterMinutes is the default +/- spread for the Jitter time model.
	JitterMinutes int `yaml:"jitter_minutes"`

	// LinkDates keeps author and committer dates equal by default.
	LinkDates bool `yaml:"link_dates"`

	// Weekdays lists the enabled weekday short names for weekday-masked
	// strategies, e.g. ["mon","tue","wed","thu","fri"].
	Weekdays []string `yaml:"weekdays"`

	// Seed makes randomized strategies reproducible across a session. Zero means
	// "derive from the current time" (resolved by the caller, not here).
	Seed int64 `yaml:"seed"`

	// LLM configures the optional language model used for commit splitting.
	LLM LLMConfig `yaml:"llm"`

	// Split configures default parameters for the commit-splitting feature.
	Split SplitConfig `yaml:"split"`

	// Reword configures default parameters for the commit-rewording feature.
	Reword RewordConfig `yaml:"reword"`
}

// Default returns the built-in configuration used when nothing overrides it.
func Default() Config {
	return Config{
		Timezone:         "local",
		WorkdayStart:     "09:00",
		WorkdayEnd:       "18:00",
		LunchStart:       "",
		LunchEnd:         "",
		DefaultStrategy:  "Even Spread",
		DefaultTimeModel: "Working hours",
		JitterMinutes:    20,
		LinkDates:        true,
		Weekdays:         []string{"mon", "tue", "wed", "thu", "fri"},
		Seed:             0,
		LLM:              LLMConfig{Temperature: 0, TimeoutSec: 60},
		Split: SplitConfig{
			MaxCommits:     4,
			LinesPerCommit: 40,
			MinSplitLines:  20,
			RecursionDepth: 2,
			Context:        []string{"readme"},
			RepairAttempts: 2,
			WIPPrefix:      "WIP: ",
			Cache:          true,
		},
		Reword: RewordConfig{Variants: 3, Context: []string{"readme"}, ContextLines: 30, Prefetch: 1, Cache: true},
	}
}

// Load resolves the configuration: defaults, then the YAML file if present, then
// environment overrides. A missing config file is not an error. Unknown YAML
// keys and unusable values (bad timezone, inverted workday window) ARE errors —
// a typo'd key or clock silently falling back to a default is how a config
// change goes unnoticed.
func Load() (Config, error) {
	cfg := Default()

	path := configPath()
	if data, err := os.ReadFile(path); err == nil {
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		// Decode returns io.EOF for an empty (or comment-only) file; that is a
		// valid "all defaults" config, not a parse error.
		if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
			return cfg, fmt.Errorf("parse %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return cfg, fmt.Errorf("read %s: %w", path, err)
	}

	applyEnvOverrides(&cfg)
	if err := cfg.Validate(); err != nil {
		return cfg, fmt.Errorf("config: %w", err)
	}
	return cfg, nil
}

// Validate reports configuration values that cannot work: an unresolvable
// timezone, an unparsable workday clock, or an inverted workday window. Load
// calls it after the file and environment are applied, so a bad value fails
// fast at startup instead of silently falling back mid-run (the old behavior
// dated everything at the workday start when end <= start).
func (c Config) Validate() error {
	if c.Timezone != "" && c.Timezone != "local" {
		if _, err := time.LoadLocation(c.Timezone); err != nil {
			return fmt.Errorf("timezone %q is not a recognized IANA zone (or \"local\")", c.Timezone)
		}
	}
	start, err := parseClock(c.WorkdayStart)
	if err != nil {
		return fmt.Errorf("workday_start: %w", err)
	}
	end, err := parseClock(c.WorkdayEnd)
	if err != nil {
		return fmt.Errorf("workday_end: %w", err)
	}
	if end <= start {
		return fmt.Errorf("workday_end %q must be after workday_start %q", c.WorkdayEnd, c.WorkdayStart)
	}
	return nil
}

// parseClock parses a required "HH:MM" value to minutes from midnight. Unlike
// parseMinutes it has no silent fallback: Validate wants bad values loud.
func parseClock(hhmm string) (int, error) {
	if hhmm == "" {
		return 0, fmt.Errorf("missing HH:MM value")
	}
	t, err := time.Parse("15:04", hhmm)
	if err != nil {
		return 0, fmt.Errorf("%q is not a valid HH:MM clock", hhmm)
	}
	return t.Hour()*60 + t.Minute(), nil
}

// configPath returns the location of the YAML config file.
func configPath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "regit", "config.yaml")
}

// envOverride binds one REGIT_* environment variable to the setter that applies
// its value. This table is the single source of truth for which overrides
// exist: applyEnvOverrides ranges it, and EnvOverrideNames projects it for the
// README env-var list and its drift test (internal/docs). Extending env
// coverage (planned for M5) means adding rows here, nothing else.
type envOverride struct {
	Name  string                // the environment variable, e.g. "REGIT_TIMEZONE"
	Apply func(*Config, string) // parses v and sets the field; ignores bad values
}

// envOverrides holds every supported override in documentation order. Setters
// silently ignore unparsable numbers/bools, matching the historical behavior:
// a bad env value falls back to the file/default rather than erroring.
var envOverrides = []envOverride{
	{"REGIT_TIMEZONE", func(c *Config, v string) { c.Timezone = v }},
	{"REGIT_WORKDAY_START", func(c *Config, v string) { c.WorkdayStart = v }},
	{"REGIT_WORKDAY_END", func(c *Config, v string) { c.WorkdayEnd = v }},
	{"REGIT_DEFAULT_STRATEGY", func(c *Config, v string) { c.DefaultStrategy = v }},
	{"REGIT_DEFAULT_TIME_MODEL", func(c *Config, v string) { c.DefaultTimeModel = v }},
	{"REGIT_SEED", func(c *Config, v string) {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			c.Seed = n
		}
	}},
	{"REGIT_LINK_DATES", func(c *Config, v string) {
		if b, err := strconv.ParseBool(v); err == nil {
			c.LinkDates = b
		}
	}},
	{"REGIT_LLM_PROVIDER", func(c *Config, v string) { c.LLM.Provider = v }},
	{"REGIT_LLM_MODEL", func(c *Config, v string) { c.LLM.Model = v }},
	{"REGIT_SPLIT_MAX_COMMITS", func(c *Config, v string) {
		if n, err := strconv.Atoi(v); err == nil {
			c.Split.MaxCommits = n
		}
	}},
	{"REGIT_REWORD_VARIANTS", func(c *Config, v string) {
		if n, err := strconv.Atoi(v); err == nil {
			c.Reword.Variants = n
		}
	}},
}

// applyEnvOverrides lets REGIT_* variables win over the file, which is handy
// for one-off runs and CI without editing the YAML.
func applyEnvOverrides(cfg *Config) {
	for _, o := range envOverrides {
		if v := os.Getenv(o.Name); v != "" {
			o.Apply(cfg, v)
		}
	}
}

// EnvOverrideNames returns every supported REGIT_* override in table order.
// The README's env-var list is generated from this (see internal/docs), so the
// docs always name exactly the variables the code honors.
func EnvOverrideNames() []string {
	names := make([]string, len(envOverrides))
	for i, o := range envOverrides {
		names[i] = o.Name
	}
	return names
}

// Location resolves the configured timezone to a *time.Location, defaulting to
// the machine local zone for "local" or an unrecognized name.
func (c Config) Location() *time.Location {
	if c.Timezone == "" || c.Timezone == "local" {
		return time.Local
	}
	if loc, err := time.LoadLocation(c.Timezone); err == nil {
		return loc
	}
	return time.Local
}

// WorkdayStartMin / WorkdayEndMin return the working-window bounds as minutes
// from midnight, falling back to 09:00 / 18:00 when unparsable.
func (c Config) WorkdayStartMin() int { return parseMinutes(c.WorkdayStart, 9*60) }
func (c Config) WorkdayEndMin() int   { return parseMinutes(c.WorkdayEnd, 18*60) }

// LunchStartMin / LunchEndMin return the lunch gap in minutes; 0/0 disables it.
func (c Config) LunchStartMin() int { return parseMinutes(c.LunchStart, 0) }
func (c Config) LunchEndMin() int   { return parseMinutes(c.LunchEnd, 0) }

// WeekdayMask converts the configured weekday short names into a 7-element mask
// indexed by time.Weekday (Sunday == 0). Unrecognized names are ignored. The
// strategy package consumes this as its WeekdayMask type.
func (c Config) WeekdayMask() [7]bool {
	short := map[string]time.Weekday{
		"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday,
		"wed": time.Wednesday, "thu": time.Thursday, "fri": time.Friday,
		"sat": time.Saturday,
	}
	var mask [7]bool
	for _, name := range c.Weekdays {
		if wd, ok := short[name]; ok {
			mask[int(wd)] = true
		}
	}
	return mask
}

// parseMinutes converts "HH:MM" to minutes from midnight; returns fallback on
// any parse failure (including the empty string).
func parseMinutes(hhmm string, fallback int) int {
	if hhmm == "" {
		return fallback
	}
	t, err := time.Parse("15:04", hhmm)
	if err != nil {
		return fallback
	}
	return t.Hour()*60 + t.Minute()
}
