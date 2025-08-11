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
