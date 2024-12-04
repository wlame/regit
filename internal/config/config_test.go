package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefault_sensibleValues(t *testing.T) {
	c := Default()
	if c.WorkdayStartMin() != 9*60 || c.WorkdayEndMin() != 18*60 {
		t.Errorf("default working window wrong: %d-%d", c.WorkdayStartMin(), c.WorkdayEndMin())
	}
	if !c.LinkDates {
		t.Error("dates should be linked by default")
	}
}

func TestParseMinutes(t *testing.T) {
	if parseMinutes("14:30", 0) != 14*60+30 {
		t.Error("14:30 should be 870 minutes")
	}
	if parseMinutes("", 99) != 99 {
		t.Error("empty should return fallback")
	}
	if parseMinutes("garbage", 7) != 7 {
		t.Error("invalid should return fallback")
	}
}

func TestWeekdayMask_mapsShortNames(t *testing.T) {
	c := Config{Weekdays: []string{"mon", "wed", "fri", "bogus"}}
	m := c.WeekdayMask()
	if !m[int(time.Monday)] || !m[int(time.Wednesday)] || !m[int(time.Friday)] {
		t.Error("expected mon/wed/fri enabled")
	}
	if m[int(time.Tuesday)] || m[int(time.Sunday)] {
		t.Error("unlisted days should be disabled")
	}
}

func TestLocation_localAndNamed(t *testing.T) {
	if (Config{Timezone: "local"}).Location() != time.Local {
		t.Error("'local' should map to time.Local")
	}
	if (Config{Timezone: "definitely/not-a-zone"}).Location() != time.Local {
		t.Error("invalid zone should fall back to local")
	}
	if loc := (Config{Timezone: "UTC"}).Location(); loc.String() != "UTC" {
		t.Errorf("UTC should load, got %s", loc)
	}
}

func TestLoad_fileAndEnvOverrides(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "regit")
	os.MkdirAll(cfgDir, 0o755)
	yaml := "timezone: UTC\nworkday_start: \"08:00\"\ndefault_strategy: Compress\n"
	os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte(yaml), 0o644)

	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("REGIT_DEFAULT_STRATEGY", "Even Spread") // env beats file

	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Timezone != "UTC" {
		t.Errorf("file value not loaded: timezone=%s", c.Timezone)
	}
	if c.WorkdayStartMin() != 8*60 {
		t.Errorf("file workday_start not loaded: %d", c.WorkdayStartMin())
	}
	if c.DefaultStrategy != "Even Spread" {
		t.Errorf("env override should win, got %s", c.DefaultStrategy)
	}
}

func TestLoad_allEnvOverrides(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // no file
	t.Setenv("REGIT_TIMEZONE", "UTC")
	t.Setenv("REGIT_WORKDAY_START", "07:30")
	t.Setenv("REGIT_WORKDAY_END", "19:00")
	t.Setenv("REGIT_DEFAULT_TIME_MODEL", "Evening")
	t.Setenv("REGIT_SEED", "42")
	t.Setenv("REGIT_LINK_DATES", "false")

	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Timezone != "UTC" || c.WorkdayStartMin() != 7*60+30 || c.WorkdayEndMin() != 19*60 {
		t.Errorf("env window/timezone not applied: %+v", c)
	}
	if c.DefaultTimeModel != "Evening" || c.Seed != 42 || c.LinkDates {
		t.Errorf("env scalar overrides not applied: %+v", c)
	}
}

func TestLunchAccessors(t *testing.T) {
	c := Config{LunchStart: "12:00", LunchEnd: "13:00"}
	if c.LunchStartMin() != 12*60 || c.LunchEndMin() != 13*60 {
		t.Errorf("lunch accessors wrong: %d-%d", c.LunchStartMin(), c.LunchEndMin())
	}
	empty := Config{}
	if empty.LunchStartMin() != 0 || empty.LunchEndMin() != 0 {
		t.Error("empty lunch should be 0/0 (disabled)")
	}
}

func TestLoad_malformedYAMLErrors(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "regit")
	os.MkdirAll(cfgDir, 0o755)
	os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte("timezone: [unclosed"), 0o644)
	t.Setenv("XDG_CONFIG_HOME", dir)
	if _, err := Load(); err == nil {
		t.Error("malformed YAML should error")
	}
}

func TestLoad_missingFileUsesDefaults(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // empty dir, no config file
	c, err := Load()
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if c.DefaultStrategy != "Even Spread" {
		t.Errorf("expected default strategy, got %s", c.DefaultStrategy)
	}
}

func TestLoad_splitDefaults(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // no file → defaults
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Split.MaxCommits != 4 || cfg.Split.LinesPerCommit != 40 {
		t.Fatalf("unexpected split defaults: %+v", cfg.Split)
	}
	if cfg.Split.WIPPrefix != "WIP: " {
		t.Fatalf("wip prefix = %q", cfg.Split.WIPPrefix)
	}
}

func TestLoad_regitEnvPrefixOverrides(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("REGIT_TIMEZONE", "UTC")
	t.Setenv("REGIT_SPLIT_MAX_COMMITS", "3")
	t.Setenv("REGIT_LLM_PROVIDER", "ollama")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Timezone != "UTC" {
		t.Fatalf("timezone = %q", cfg.Timezone)
	}
	if cfg.Split.MaxCommits != 3 {
		t.Fatalf("split max = %d", cfg.Split.MaxCommits)
	}
	if cfg.LLM.Provider != "ollama" {
		t.Fatalf("llm provider = %q", cfg.LLM.Provider)
	}
}
