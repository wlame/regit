package gitio

import (
	"strings"
	"testing"
)

func TestBackupRefDocPattern_rendersLikeBackupRefName(t *testing.T) {
	rep := strings.NewReplacer("<branch>", "main", "<ts>", "1717689600", "<seq>", "0")
	got := rep.Replace(BackupRefDocPattern)
	want := BackupRefName("main", 1717689600, 0)
	if got != want {
		t.Fatalf("BackupRefDocPattern instantiates to %q; BackupRefName renders %q — keep them in lockstep", got, want)
	}
	if !strings.HasPrefix(BackupRefDocPattern, BackupRefPrefix) {
		t.Errorf("BackupRefDocPattern %q must start with BackupRefPrefix %q", BackupRefDocPattern, BackupRefPrefix)
	}
}
