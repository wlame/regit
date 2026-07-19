package gitio

import (
	"os"
	"path/filepath"
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

// TestPushedDetection_failsClosedOnBrokenRemoteRef verifies a rev-list
// --remotes failure (here: a corrupt remote-tracking ref) propagates out of
// ReadCommits instead of silently reporting every commit as unpushed — the
// pushed-history confirmation gate must degrade closed, not open.
func TestPushedDetection_failsClosedOnBrokenRemoteRef(t *testing.T) {
	repo, dir := testRepo(t)

	// Sanity: a healthy repo with no remotes reads fine (empty pushed set).
	if _, err := repo.ReadCommits(); err != nil {
		t.Fatalf("healthy ReadCommits: %v", err)
	}

	// Corrupt ref: a refs/remotes file whose content is not a hash.
	brokenDir := filepath.Join(dir, ".git", "refs", "remotes", "origin")
	if err := os.MkdirAll(brokenDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(brokenDir, "main"), []byte("notahash\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Old gits (e.g. 2.25) silently skip a broken loose ref instead of failing
	// rev-list, so the fail-closed property is unobservable there: probe the
	// exact command pushedHashes runs and skip when this git tolerates it.
	if _, err := repo.gitRaw("rev-list", "--remotes"); err == nil {
		t.Skip("this git silently ignores broken remote-tracking refs")
	}
	if _, err := repo.ReadCommits(); err == nil {
		t.Fatal("ReadCommits must fail when pushed detection fails, not report everything unpushed")
	}
}

// TestAppliedHeadFor_absentVsPresent verifies the guard-input contract: a
// missing applied ref is the benign legacy case (ok=false, no error), while a
// recorded one round-trips.
func TestAppliedHeadFor_absentVsPresent(t *testing.T) {
	repo, _ := testRepo(t)
	head, err := repo.ResolveRef("HEAD")
	if err != nil {
		t.Fatal(err)
	}
	backup, err := repo.CreateBackupRef("main", 1000, head)
	if err != nil {
		t.Fatal(err)
	}

	if _, ok, err := repo.AppliedHeadFor(backup); ok || err != nil {
		t.Fatalf("no applied ref recorded: want ok=false, err=nil; got ok=%v err=%v", ok, err)
	}
	if err := repo.RecordAppliedHead(backup, head); err != nil {
		t.Fatal(err)
	}
	got, ok, err := repo.AppliedHeadFor(backup)
	if !ok || err != nil || got != head {
		t.Fatalf("recorded applied head: got %q ok=%v err=%v, want %q", got, ok, err, head)
	}
}
