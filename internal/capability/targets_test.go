package capability

import (
	"fmt"
	"testing"
)

func TestResolveTargets(t *testing.T) {
	_, repo := newTestRepo(t, 4) // commits A, B, C, D (D is HEAD)
	commits, _ := repo.ReadCommits()

	tests := []struct {
		name      string
		spec      string
		wantCount int
		wantErr   bool
		wantPanic bool // for out-of-order cases that used to panic
	}{
		{
			name:      "empty spec returns all commits",
			spec:      "",
			wantCount: 4,
			wantErr:   false,
		},
		{
			name:      "valid range A..B",
			spec:      "HEAD~3..HEAD~1",
			wantCount: 2, // commits after HEAD~3 (exclusive) through HEAD~1 (inclusive)
			wantErr:   false,
		},
		{
			name:      "range with empty lo (..B)",
			spec:      "..HEAD~1",
			wantCount: 3, // from root through HEAD~1
			wantErr:   false,
		},
		{
			name:      "single ref (HEAD)",
			spec:      "HEAD",
			wantCount: 1,
			wantErr:   false,
		},
		{
			name:      "single ref (HEAD~2)",
			spec:      "HEAD~2",
			wantCount: 1,
			wantErr:   false,
		},
		{
			name:      "equal commits (A..A)",
			spec:      "HEAD..HEAD",
			wantCount: 0, // empty slice
			wantErr:   false,
		},
		{
			name:      "out-of-order range (newer..older) should error not panic",
			spec:      "HEAD~1..HEAD~3",
			wantCount: 0,
			wantErr:   true,
			wantPanic: true, // this would panic before the fix
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := resolveTargets(repo, commits, tt.spec)
			if (err != nil) != tt.wantErr {
				t.Fatalf("got error %v, want error %v", err, tt.wantErr)
			}
			if err != nil {
				// Verify out-of-order case produces the expected error message
				if tt.wantPanic && tt.spec == "HEAD~1..HEAD~3" {
					if fmt.Sprint(err) != fmt.Sprintf("range \"HEAD~1..HEAD~3\": A must be an ancestor of B") {
						t.Errorf("got error %q, want error about ancestor relationship", err)
					}
				}
				return
			}
			if len(result) != tt.wantCount {
				t.Errorf("got %d commits, want %d", len(result), tt.wantCount)
			}

			// For valid ranges, verify the first element is correct
			if tt.spec == "HEAD~3..HEAD~1" {
				// Should include commits right after HEAD~3 through HEAD~1
				// With root-first ordering: HEAD~3 is at index 0, HEAD~1 is at index 2
				// Result should be [index1, index2] = [commit B, commit C]
				if len(result) > 0 && result[0].Subject != "commit B" {
					t.Errorf("expected first commit to be B, got %s", result[0].Subject)
				}
			}

			if tt.spec == "..HEAD~1" {
				// Should include all commits from root through HEAD~1
				// [commit A, commit B, commit C]
				if len(result) > 0 && result[0].Subject != "commit A" {
					t.Errorf("expected first commit to be A, got %s", result[0].Subject)
				}
			}

			if tt.spec == "HEAD" {
				// Single commit HEAD (commit D)
				if len(result) == 1 && result[0].Subject != "commit D" {
					t.Errorf("expected commit D, got %s", result[0].Subject)
				}
			}
		})
	}
}
