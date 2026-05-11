package gitio

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// configureSSHSigning generates a throwaway SSH key and points git at it for
// commit signing — no gpg-agent/daemon needed, unlike GPG.
func configureSSHSigning(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not available")
	}
	key := filepath.Join(t.TempDir(), "id")
	if out, err := exec.Command("ssh-keygen", "-t", "ed25519", "-N", "", "-f", key).CombinedOutput(); err != nil {
		t.Skipf("ssh-keygen failed: %v\n%s", err, out)
	}
	gitT(t, dir, "config", "gpg.format", "ssh")
	gitT(t, dir, "config", "user.signingkey", key+".pub")
	// Allowed-signers so `git verify-commit` can validate.
	signers := filepath.Join(t.TempDir(), "allowed_signers")
	pub, _ := os.ReadFile(key + ".pub")
	_ = os.WriteFile(signers, []byte("test@regit "+string(pub)), 0o644)
	gitT(t, dir, "config", "gpg.ssh.allowedSignersFile", signers)
}

// TestReplay_signsWhenReSign proves that Replay(plan, ReSign) actually invokes
// `commit-tree -S`: with an SSH signing key configured, the rewritten commit's
// raw object must carry a `gpgsig` header. This is the guard-rail for the
// --sign flag added in this task — without it, --sign would silently produce
// unsigned commits.
func TestReplay_signsWhenReSign(t *testing.T) {
	dir, r := initRepo(t)
	configureSSHSigning(t, dir)
	snapTree(t, dir, "one")

	commits, err := r.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	plan := buildRedatePlan(t, commits)
	newHead, _, err := r.Replay(plan, ReSign)
	if err != nil {
		t.Fatalf("Replay(ReSign): %v", err)
	}
	raw := gitT(t, dir, "cat-file", "commit", newHead)
	if !strings.Contains(raw, "gpgsig") {
		t.Errorf("ReSign commit has no signature header:\n%s", raw)
	}
}

// TestHasSigningKey proves HasSigningKey tracks user.signingkey: false before
// any signing config exists, true once SSH signing is configured.
func TestHasSigningKey(t *testing.T) {
	dir, r := initRepo(t)
	if r.HasSigningKey() {
		t.Error("no signing key configured yet")
	}
	configureSSHSigning(t, dir)
	if !r.HasSigningKey() {
		t.Error("HasSigningKey should be true after configuring ssh signing")
	}
}
