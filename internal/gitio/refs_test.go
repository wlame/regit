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

// TestAppliedHead_roundTrips: RecordAppliedHead/AppliedHeadFor round-trip the
// produced-head paired with a backup ref, and report ("", false, nil) — not an
// error — for a backup with no paired applied ref (the legacy-degrade case).
func TestAppliedHead_roundTrips(t *testing.T) {
	dir, r := initRepo(t)
	head := snapTree(t, dir, "one")
	backup := BackupRefName("main", 1717689600, 0)
	// The backup ref itself must exist for the applied ref to be meaningful.
	if err := r.UpdateRef(backup, head, ""); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordAppliedHead(backup, head); err != nil {
		t.Fatalf("RecordAppliedHead: %v", err)
	}
	got, ok, err := r.AppliedHeadFor(backup)
	if err != nil || !ok {
		t.Fatalf("AppliedHeadFor: ok=%v err=%v", ok, err)
	}
	if got != head {
		t.Errorf("applied head = %s, want %s", got, head)
	}
	// A backup with no applied ref reports not-found, not an error.
	if _, ok, err := r.AppliedHeadFor(BackupRefName("main", 1717689600, 9)); err != nil || ok {
		t.Errorf("missing applied ref should be (\"\", false, nil); got ok=%v err=%v", ok, err)
	}
}

// TestDeleteAppliedRef_removesPairedRef: DeleteAppliedRef removes the paired
// applied ref (not the backup ref itself), after which AppliedHeadFor reports
// not-found for that backup — the same degrade path a legacy backup takes.
func TestDeleteAppliedRef_removesPairedRef(t *testing.T) {
	dir, r := initRepo(t)
	head := snapTree(t, dir, "one")
	backup := BackupRefName("main", 1717689600, 1)
	if err := r.UpdateRef(backup, head, ""); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordAppliedHead(backup, head); err != nil {
		t.Fatalf("RecordAppliedHead: %v", err)
	}
	if err := r.DeleteAppliedRef(backup); err != nil {
		t.Fatalf("DeleteAppliedRef: %v", err)
	}
	if _, ok, err := r.AppliedHeadFor(backup); err != nil || ok {
		t.Errorf("applied ref should be gone after DeleteAppliedRef; got ok=%v err=%v", ok, err)
	}
	// The backup ref itself must be untouched.
	if got, err := r.ResolveRef(backup); err != nil || got != head {
		t.Errorf("DeleteAppliedRef must not touch the backup ref itself; got %s err=%v", got, err)
	}
}
