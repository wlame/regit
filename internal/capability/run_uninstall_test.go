package capability

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUninstall_RemovesOwnLink(t *testing.T) {
	bin := t.TempDir()
	regit := filepath.Join(bin, "regit")
	writeFakeBinary(t, regit)
	env := fakeInstallEnv(regit, bin)
	ctx0, _, _ := installCtx()
	if err := runInstallEnv(env, ctx0, newParsedArgs(nil, nil)); err != nil {
		t.Fatal(err)
	}

	ctx, out, _ := installCtx()
	if err := runUninstallEnv(env, ctx, newParsedArgs(nil, nil)); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(bin, "git-re")); !os.IsNotExist(err) {
		t.Fatalf("git-re still present: %v", err)
	}
	if !strings.Contains(out.String(), "Removed") {
		t.Fatalf("output missing Removed banner: %q", out.String())
	}
}

func TestUninstall_ForeignLinkRefused(t *testing.T) {
	bin := t.TempDir()
	regit := filepath.Join(bin, "regit")
	writeFakeBinary(t, regit)
	foreign := filepath.Join(bin, "git-re")
	writeFakeBinary(t, foreign)

	ctx, _, _ := installCtx()
	err := runUninstallEnv(fakeInstallEnv(regit, bin), ctx, newParsedArgs(nil, nil))
	if err == nil || ExitCode(err) != 3 {
		t.Fatalf("foreign git-re should refuse with exit 3, got %v", err)
	}
	if _, statErr := os.Lstat(foreign); statErr != nil {
		t.Fatal("foreign git-re was deleted")
	}
}

func TestUninstall_NotInstalledIsNoop(t *testing.T) {
	bin := t.TempDir()
	regit := filepath.Join(bin, "regit")
	writeFakeBinary(t, regit)

	ctx, out, _ := installCtx()
	if err := runUninstallEnv(fakeInstallEnv(regit, bin), ctx, newParsedArgs(nil, nil)); err != nil {
		t.Fatalf("uninstall on clean system: %v", err)
	}
	if !strings.Contains(out.String(), "Not installed") {
		t.Fatalf("expected Not installed message, got %q", out.String())
	}
}
