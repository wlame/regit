package capability

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeInstallEnv builds an installEnv whose PATH search sees exactly one
// directory (binDir) and whose "running binary" is self. lookPath mirrors
// exec.LookPath's contract: it errors when the name is absent from binDir.
func fakeInstallEnv(self, binDir string) installEnv {
	return installEnv{
		executable: func() (string, error) { return self, nil },
		lookPath: func(name string) (string, error) {
			p := filepath.Join(binDir, name)
			if _, err := os.Lstat(p); err != nil {
				return "", err
			}
			return p, nil
		},
	}
}

// writeFakeBinary creates an executable file to stand in for the regit binary.
func writeFakeBinary(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// installCtx returns a RunContext with buffer-backed stdio for assertions.
func installCtx() (*RunContext, *bytes.Buffer, *bytes.Buffer) {
	var out, errBuf bytes.Buffer
	return &RunContext{Stdout: &out, Stderr: &errBuf}, &out, &errBuf
}

func TestInstall_FreshCreatesSymlink(t *testing.T) {
	bin := t.TempDir()
	regit := filepath.Join(bin, "regit")
	writeFakeBinary(t, regit)
	ctx, out, _ := installCtx()

	err := runInstallEnv(fakeInstallEnv(regit, bin), ctx, newParsedArgs(nil, nil))
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	link := filepath.Join(bin, "git-re")
	resolved, err := filepath.EvalSymlinks(link)
	if err != nil {
		t.Fatalf("git-re not created: %v", err)
	}
	wantTarget, _ := filepath.EvalSymlinks(regit)
	if resolved != wantTarget {
		t.Fatalf("git-re resolves to %s, want %s", resolved, wantTarget)
	}
	if !strings.Contains(out.String(), "Installed:") {
		t.Fatalf("output missing Installed banner: %q", out.String())
	}
	if !strings.Contains(out.String(), "git re -h") {
		t.Fatalf("output missing teaching hint: %q", out.String())
	}
}

func TestInstall_SecondRunIsIdempotent(t *testing.T) {
	bin := t.TempDir()
	regit := filepath.Join(bin, "regit")
	writeFakeBinary(t, regit)
	env := fakeInstallEnv(regit, bin)

	ctx1, _, _ := installCtx()
	if err := runInstallEnv(env, ctx1, newParsedArgs(nil, nil)); err != nil {
		t.Fatalf("first install: %v", err)
	}
	ctx2, out, _ := installCtx()
	if err := runInstallEnv(env, ctx2, newParsedArgs(nil, nil)); err != nil {
		t.Fatalf("second install: %v", err)
	}
	if !strings.Contains(out.String(), "Already installed") {
		t.Fatalf("second run should report already installed, got %q", out.String())
	}
}

func TestInstall_ForeignFileRefusedWithoutForce(t *testing.T) {
	bin := t.TempDir()
	regit := filepath.Join(bin, "regit")
	writeFakeBinary(t, regit)
	foreign := filepath.Join(bin, "git-re")
	writeFakeBinary(t, foreign) // a real file, not our symlink

	ctx, _, _ := installCtx()
	err := runInstallEnv(fakeInstallEnv(regit, bin), ctx, newParsedArgs(nil, nil))
	if err == nil {
		t.Fatal("expected refusal for foreign git-re")
	}
	if got := ExitCode(err); got != 3 {
		t.Fatalf("exit code = %d, want 3", got)
	}
	// The foreign file must be untouched.
	if fi, statErr := os.Lstat(foreign); statErr != nil || fi.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("foreign git-re was modified: %v %v", fi, statErr)
	}
}

func TestInstall_ForceReplacesForeignFile(t *testing.T) {
	bin := t.TempDir()
	regit := filepath.Join(bin, "regit")
	writeFakeBinary(t, regit)
	writeFakeBinary(t, filepath.Join(bin, "git-re"))

	ctx, _, _ := installCtx()
	args := newParsedArgs(map[string]any{"force": true}, nil)
	if err := runInstallEnv(fakeInstallEnv(regit, bin), ctx, args); err != nil {
		t.Fatalf("install --force: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(bin, "git-re"))
	wantTarget, _ := filepath.EvalSymlinks(regit)
	if err != nil || resolved != wantTarget {
		t.Fatalf("git-re not replaced by link: %s %v", resolved, err)
	}
}

func TestInstall_DanglingSymlinkIsForeign(t *testing.T) {
	bin := t.TempDir()
	regit := filepath.Join(bin, "regit")
	writeFakeBinary(t, regit)
	if err := os.Symlink(filepath.Join(bin, "gone"), filepath.Join(bin, "git-re")); err != nil {
		t.Fatal(err)
	}

	ctx, _, _ := installCtx()
	err := runInstallEnv(fakeInstallEnv(regit, bin), ctx, newParsedArgs(nil, nil))
	if err == nil || ExitCode(err) != 3 {
		t.Fatalf("dangling git-re should refuse with exit 3, got %v", err)
	}
	args := newParsedArgs(map[string]any{"force": true}, nil)
	ctx2, _, _ := installCtx()
	if err := runInstallEnv(fakeInstallEnv(regit, bin), ctx2, args); err != nil {
		t.Fatalf("install --force over dangling link: %v", err)
	}
}

func TestInstall_RegitMissingFromPath(t *testing.T) {
	bin := t.TempDir() // no regit inside
	self := filepath.Join(t.TempDir(), "regit")
	writeFakeBinary(t, self)

	ctx, _, _ := installCtx()
	err := runInstallEnv(fakeInstallEnv(self, bin), ctx, newParsedArgs(nil, nil))
	if err == nil || ExitCode(err) != 3 {
		t.Fatalf("missing PATH regit should be exit 3, got %v", err)
	}
	if !strings.Contains(err.Error(), "PATH") {
		t.Fatalf("error should mention PATH: %v", err)
	}
}

func TestInstall_PathCopyDiffersWarnsButProceeds(t *testing.T) {
	bin := t.TempDir()
	pathCopy := filepath.Join(bin, "regit")
	writeFakeBinary(t, pathCopy)
	other := filepath.Join(t.TempDir(), "regit")
	writeFakeBinary(t, other) // the "running" binary differs from the PATH copy

	ctx, _, errBuf := installCtx()
	if err := runInstallEnv(fakeInstallEnv(other, bin), ctx, newParsedArgs(nil, nil)); err != nil {
		t.Fatalf("install: %v", err)
	}
	if !strings.Contains(errBuf.String(), "not the running binary") {
		t.Fatalf("expected differing-binary warning, got %q", errBuf.String())
	}
	// The link must point at the PATH copy, not the running binary.
	resolved, _ := filepath.EvalSymlinks(filepath.Join(bin, "git-re"))
	wantTarget, _ := filepath.EvalSymlinks(pathCopy)
	if resolved != wantTarget {
		t.Fatalf("git-re resolves to %s, want PATH copy %s", resolved, wantTarget)
	}
}

func TestInstall_UnwritableDirErrors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory write bits are not enforced on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory write permissions")
	}
	bin := t.TempDir()
	regit := filepath.Join(bin, "regit")
	writeFakeBinary(t, regit)
	if err := os.Chmod(bin, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(bin, 0o755) })

	ctx, _, _ := installCtx()
	err := runInstallEnv(fakeInstallEnv(regit, bin), ctx, newParsedArgs(nil, nil))
	if err == nil || ExitCode(err) != 3 {
		t.Fatalf("unwritable dir should be exit 3, got %v", err)
	}
	if !strings.Contains(err.Error(), "writable") {
		t.Fatalf("error should hint at writability: %v", err)
	}
}
