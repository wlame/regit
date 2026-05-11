package model

import (
	"strings"
	"testing"
)

// TestPushHintLines proves the remote in the force-with-lease command is
// derived from the branch's actual upstream ref, not hardcoded to "origin" --
// the bug this test guards against would print "git push --force-with-lease
// origin <branch>" even when the real upstream lives on a differently named
// remote (e.g. "up/main"), which is both wrong and self-contradicting next to
// the divergence line that correctly names "up/main".
func TestPushHintLines(t *testing.T) {
	cases := []struct {
		name  string
		state RepoState
		want  []string
	}{
		{
			name: "origin remote",
			state: RepoState{
				Branch: "main", HasUpstream: true, UpstreamRef: "origin/main",
			},
			want: []string{
				"origin/main has diverged from your rewrite. To publish:",
				"git push --force-with-lease origin main",
			},
		},
		{
			name: "non-origin remote",
			state: RepoState{
				Branch: "main", HasUpstream: true, UpstreamRef: "up/main",
			},
			want: []string{
				"up/main has diverged from your rewrite. To publish:",
				"git push --force-with-lease up main",
			},
		},
		{
			name:  "no upstream",
			state: RepoState{Branch: "main", HasUpstream: false},
			want:  nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := PushHintLines(tc.state)
			if len(got) != len(tc.want) {
				t.Fatalf("PushHintLines = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("line %d = %q, want %q", i, got[i], tc.want[i])
				}
			}
			if tc.want != nil && !strings.Contains(got[1], "git push --force-with-lease") {
				t.Errorf("command line %q missing force-with-lease", got[1])
			}
		})
	}
}
