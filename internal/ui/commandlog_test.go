package ui

import (
	"os"
	"path/filepath"
	"testing"
)

// TestAppendCommandLog_tightensPreexistingPermissions covers an install that
// created .git/regit/ and command.log before they were restricted to the
// owner. MkdirAll and OpenFile apply their mode only on creation, so without
// an explicit chmod the old world-readable modes would survive and keep
// receiving lines that can carry a redact secret.
func TestAppendCommandLog_tightensPreexistingPermissions(t *testing.T) {
	m := newTestModelN(t, 1)
	gitDir, err := m.repo.GitDir()
	if err != nil {
		t.Fatalf("GitDir: %v", err)
	}
	dir := filepath.Join(gitDir, "regit")
	logPath := filepath.Join(dir, "command.log")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(logPath, 0o644); err != nil {
		t.Fatal(err)
	}

	appendCommandLog(m.repo, "regit redact --match=secret --execute")

	assertOwnerOnly(t, dir)
	assertOwnerOnly(t, logPath)
}

func assertOwnerOnly(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("%s has mode %o; want no group/other bits", path, perm)
	}
}
