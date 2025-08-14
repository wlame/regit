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

func TestWantsTopHelp(t *testing.T) {
	// wantsTopHelp delegates to wantsHelp; verify the delegation is correct.
	cases := []struct {
		name string
		args []string
		want bool
	}{
		{"bare --help", []string{"--help"}, true},
		{"bare -h", []string{"-h"}, true},
		{"no args", nil, false},
		{"repo flag only", []string{"--repo=."}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := wantsTopHelp(tc.args); got != tc.want {
				t.Errorf("wantsTopHelp(%v) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}
