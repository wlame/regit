package model

import "testing"

func TestAnyPushed(t *testing.T) {
	cases := []struct {
		name    string
		commits []Commit
		want    bool
	}{
		{"empty", nil, false},
		{"none pushed", []Commit{{IsPushed: false}, {IsPushed: false}}, false},
		{"one pushed", []Commit{{IsPushed: false}, {IsPushed: true}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := AnyPushed(tc.commits); got != tc.want {
				t.Errorf("AnyPushed = %v, want %v", got, tc.want)
			}
		})
	}
}
