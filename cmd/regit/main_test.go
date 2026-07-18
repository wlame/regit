package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWantsVersion(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want bool
	}{
		{"double dash", []string{"--version"}, true},
		{"single dash", []string{"-version"}, true},
		{"version after repo flag", []string{"--repo", ".", "--version"}, true},
		{"no args", nil, false},
		{"repo only", []string{"--repo", "/tmp/x"}, false},
		{"split subcommand", []string{"split", "HEAD"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := wantsVersion(tc.args); got != tc.want {
				t.Errorf("wantsVersion(%v) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}

func TestWantsHelp(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want bool
	}{
		{"double dash help", []string{"--help"}, true},
		{"-h short", []string{"-h"}, true},
		{"help among flags", []string{"--dry-run", "--help", "--yes"}, true},
		{"no args", nil, false},
		{"other flags only", []string{"--dry-run", "--yes"}, false},
		{"version flag", []string{"--version"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := wantsHelp(tc.args); got != tc.want {
				t.Errorf("wantsHelp(%v) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}

func TestExtractRepoFlag(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantRepo string
		wantRest []string
	}{
		{
			name:     "equals form before verb",
			args:     []string{"--repo=/x", "split", "HEAD"},
			wantRepo: "/x",
			wantRest: []string{"split", "HEAD"},
		},
		{
			name:     "space form before verb",
			args:     []string{"--repo", "/x", "split", "HEAD"},
			wantRepo: "/x",
			wantRest: []string{"split", "HEAD"},
		},
		{
			name:     "repo only, no verb",
			args:     []string{"--repo=/some/path"},
			wantRepo: "/some/path",
			wantRest: []string{},
		},
		{
			name:     "absent, returns dot",
			args:     []string{"split", "HEAD"},
			wantRepo: ".",
			wantRest: []string{"split", "HEAD"},
		},
		{
			name:     "no args",
			args:     nil,
			wantRepo: ".",
			wantRest: nil,
		},
		{
			name:     "post-verb repo not extracted",
			args:     []string{"split", "--repo=/x", "HEAD"},
			wantRepo: ".",
			wantRest: []string{"split", "--repo=/x", "HEAD"},
		},
		{
			name:     "other leading flag before repo",
			args:     []string{"--dry-run", "--repo=/x", "split"},
			wantRepo: "/x",
			wantRest: []string{"--dry-run", "split"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotRepo, gotRest := extractRepoFlag(tc.args)
			if gotRepo != tc.wantRepo {
				t.Errorf("extractRepoFlag(%v) repo = %q, want %q", tc.args, gotRepo, tc.wantRepo)
			}
			// Compare slices element-by-element so nil and empty are both treated
			// as "nothing returned".
			if len(gotRest) != len(tc.wantRest) {
				t.Errorf("extractRepoFlag(%v) rest = %v, want %v", tc.args, gotRest, tc.wantRest)
				return
			}
			for i := range gotRest {
				if gotRest[i] != tc.wantRest[i] {
					t.Errorf("extractRepoFlag(%v) rest[%d] = %q, want %q", tc.args, i, gotRest[i], tc.wantRest[i])
				}
			}
		})
	}
}

func TestRejectUnknownFlag(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{"unknown double-dash flag", []string{"--unknown"}, true},
		{"unknown single-dash flag", []string{"-x"}, true},
		{"empty args", nil, false},
		{"verb first", []string{"split", "--repo=."}, false},
		{"no args at all", []string{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := rejectUnknownFlag(tc.args)
			if (err != nil) != tc.wantErr {
				t.Errorf("rejectUnknownFlag(%v) error = %v, wantErr %v", tc.args, err, tc.wantErr)
			}
		})
	}
}

// TestUnknownCommand verifies a verb-shaped but unregistered leading token is
// reported (so it errors instead of silently launching the TUI — H3), while a
// known verb, a leading flag, or no args are not (TUI / flag handling proceeds).
func TestUnknownCommand(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		wantVerb   string
		wantReject bool
	}{
		{"typo verb", []string{"redat"}, "redat", true},
		{"typo verb with flags", []string{"redat", "--execute"}, "redat", true},
		{"known verb", []string{"split", "HEAD"}, "", false},
		{"another known verb", []string{"reauthor"}, "", false},
		{"leading flag", []string{"--help"}, "", false},
		{"no args", nil, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			verb, reject := unknownCommand(tc.args)
			if reject != tc.wantReject || verb != tc.wantVerb {
				t.Errorf("unknownCommand(%v) = (%q, %v), want (%q, %v)", tc.args, verb, reject, tc.wantVerb, tc.wantReject)
			}
		})
	}
}

// TestDispatch_InstallOutsideRepo proves the NoRepo path end to end in-process:
// dispatch must not open a git repository for install, and the run must create
// the git-re symlink next to the PATH-resolved regit. PATH is pinned to a temp
// dir holding a fake regit binary, so nothing outside the test tree is touched.
func TestDispatch_InstallOutsideRepo(t *testing.T) {
	bin := t.TempDir()
	fake := filepath.Join(bin, "regit")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)

	// repoDir points at a plain temp dir that is NOT a git repo; with NoRepo
	// wired this must succeed anyway. (The differing-binary warning goes to
	// os.Stderr via defaultInstallEnv — harmless test noise.)
	if err := dispatch("install", t.TempDir(), nil); err != nil {
		t.Fatalf("dispatch install: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(bin, "git-re")); err != nil {
		t.Fatalf("git-re not created: %v", err)
	}

	if err := dispatch("uninstall", t.TempDir(), nil); err == nil {
		// The fake regit is not the running test binary, so uninstall must
		// refuse to delete it (foreign link) — that refusal is the pass.
		t.Fatal("uninstall should refuse a link owned by a different binary")
	}
}

// TestInvokedAsGitRe covers the argv[0] shapes git and shells produce.
func TestInvokedAsGitRe(t *testing.T) {
	cases := []struct {
		argv0 string
		want  bool
	}{
		{"/usr/local/bin/git-re", true},
		{"git-re", true},
		{"git-re.exe", true},
		{"/usr/local/bin/regit", false},
		{"regit", false},
	}
	for _, c := range cases {
		if got := invokedAsGitRe(c.argv0); got != c.want {
			t.Errorf("invokedAsGitRe(%q) = %v, want %v", c.argv0, got, c.want)
		}
	}
}
