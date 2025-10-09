package main

import "testing"

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
