package model

import "testing"

func TestPurgeSpecValidate_acceptsCleanPaths(t *testing.T) {
	s := PurgeSpec{Paths: []string{"dir/sub", "file.txt"}}
	if err := s.Validate(); err != nil {
		t.Fatalf("valid spec rejected: %v", err)
	}
}

func TestPurgeSpecValidate_rejectsBadInput(t *testing.T) {
	cases := []struct {
		name  string
		paths []string
	}{
		{"empty spec", nil},
		{"empty path", []string{""}},
		{"absolute path", []string{"/etc/passwd"}},
		{"parent escape", []string{"../outside"}},
		{"glob star", []string{"*.log"}},
		{"glob question", []string{"a?.txt"}},
		{"glob bracket", []string{"a[0].txt"}},
		{"duplicate", []string{"a.txt", "a.txt"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := (PurgeSpec{Paths: tc.paths}).Validate(); err == nil {
				t.Errorf("Validate(%q) should fail", tc.paths)
			}
		})
	}
}

func TestUnderAnyPath_matchesExactAndPrefix(t *testing.T) {
	prefixes := []string{"dir/sub", "file.txt"}
	cases := []struct {
		path string
		want bool
	}{
		{"file.txt", true},
		{"dir/sub", true},
		{"dir/sub/deep/x.go", true},
		{"dir/subzero/x.go", false}, // prefix must stop at a path boundary
		{"other.txt", false},
	}
	for _, tc := range cases {
		if got := UnderAnyPath(tc.path, prefixes); got != tc.want {
			t.Errorf("UnderAnyPath(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}
