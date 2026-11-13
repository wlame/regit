package capability

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/wlame/regit/internal/config"
)

func TestRetitleTransform_edgeCases(t *testing.T) {
	period := RetitleTransform(nil, "", false, true)
	for _, tc := range []struct{ in, want string }{
		{"Fix login", "Fix login."},
		{"Fix login.", "Fix login."},
		{"Fix it!", "Fix it!"},
		{"What?", "What?"},
		{"", ""},
	} {
		if got := period(tc.in); got != tc.want {
			t.Errorf("ensure-period(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	capital := RetitleTransform(nil, "", true, false)
	for _, tc := range []struct{ in, want string }{
		{"fix login", "Fix login"},
		{"éclair fix", "Éclair fix"},
		{"123 fix", "123 fix"},
		{"", ""},
	} {
		if got := capital(tc.in); got != tc.want {
			t.Errorf("capitalize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	strip := RetitleTransform(regexp.MustCompile(`^feat: `), "", true, true)
	if got := strip("feat: add parser"); got != "Add parser." {
		t.Errorf("regex then toggles = %q, want %q", got, "Add parser.")
	}
}

func runRetitleArgs(t *testing.T, args ...string) (string, error) {
	t.Helper()
	_, repo := newTestRepo(t, 3) // subjects: "commit A/B/C"
	var out bytes.Buffer
	ctx := &RunContext{Repo: repo, Cfg: config.Default(), Stdout: &out, Stderr: &out, Now: time.Unix(1000, 0)}
	a, err := Parse(mustLookup(t, "retitle"), args)
	if err != nil {
		return "", err
	}
	err = runRetitle(ctx, a)
	return out.String(), err
}

func TestRetitle_togglesWithoutMatchArePreviewed(t *testing.T) {
	out, err := runRetitleArgs(t, "--ensure-period")
	if err != nil {
		t.Fatalf("runRetitle: %v", err)
	}
	if !strings.Contains(out, `"commit A" -> "commit A."`) {
		t.Errorf("ensure-period should add a full stop:\n%s", out)
	}
}

func TestRetitle_withNothingToDoIsUsageError(t *testing.T) {
	_, err := runRetitleArgs(t)
	if err == nil || !strings.Contains(err.Error(), "--match, --capitalize, or --ensure-period") {
		t.Fatalf("want a usage error naming the three options, got %v", err)
	}
	if ExitCode(err) != 1 {
		t.Errorf("exit code = %d, want 1", ExitCode(err))
	}
}

func TestRetitle_emptyResultIsSkippedAndReported(t *testing.T) {
	out, err := runRetitleArgs(t, "--match=^commit A$", "--replace=")
	if err != nil {
		t.Fatalf("runRetitle: %v", err)
	}
	if !strings.Contains(out, "skipped 1 title(s) that would become empty") {
		t.Errorf("an empty result should be reported as skipped:\n%s", out)
	}
}
