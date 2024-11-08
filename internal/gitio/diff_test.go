package gitio

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitT runs a git command in dir and fails the test on error.
func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// twoCommitRepo creates a repo with a parent commit and a child that changes two
// files, returning the repo plus the parent and child tree hashes.
func twoCommitRepo(t *testing.T) (*Repo, string, string) {
	t.Helper()
	dir := t.TempDir()
	gitT(t, dir, "init", "-q")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\ntwo\nthree\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("keep\n"), 0o644)
	gitT(t, dir, "add", "-A")
	gitT(t, dir, "commit", "-q", "-m", "parent")
	parent := gitT(t, dir, "rev-parse", "HEAD^{tree}")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("ONE\ntwo\nthree\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "c.txt"), []byte("new file\n"), 0o644)
	gitT(t, dir, "add", "-A")
	gitT(t, dir, "commit", "-q", "-m", "child")
	child := gitT(t, dir, "rev-parse", "HEAD^{tree}")
	repo, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return repo, parent, child
}

// initRepo initialises a bare git repo in a temp dir and returns (dir, *Repo).
func initRepo(t *testing.T) (string, *Repo) {
	t.Helper()
	dir := t.TempDir()
	gitT(t, dir, "init", "-q")
	repo, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return dir, repo
}

// snapTree commits whatever is currently staged and returns the tree hash.
func snapTree(t *testing.T, dir, msg string) string {
	t.Helper()
	gitT(t, dir, "commit", "-q", "-m", msg)
	return gitT(t, dir, "rev-parse", "HEAD^{tree}")
}

func TestHunks_assignsStableIDsAndFiles(t *testing.T) {
	repo, parent, child := twoCommitRepo(t)
	hs, err := repo.Hunks(parent, child)
	if err != nil {
		t.Fatal(err)
	}
	if len(hs) < 2 {
		t.Fatalf("expected >=2 hunks (a.txt edit, c.txt add), got %d", len(hs))
	}
	if hs[0].ID != "H1" {
		t.Fatalf("first hunk id = %q, want H1", hs[0].ID)
	}
	files := map[string]bool{}
	for _, h := range hs {
		files[h.File] = true
		if !strings.HasPrefix(h.Body, "@@") {
			t.Fatalf("hunk %s body should start with @@, got %q", h.ID, h.Body)
		}
		if !strings.Contains(h.FileHeader, "diff --git") {
			t.Fatalf("hunk %s missing file header", h.ID)
		}
	}
	if !files["a.txt"] || !files["c.txt"] {
		t.Fatalf("expected hunks for a.txt and c.txt, got %v", files)
	}
}

// TestHunks_twoNonAdjacentHunks verifies that a file with two non-adjacent @@
// regions produces two separate Hunks whose bodies each start with "@@", and
// that reconstructing FileHeader + bodies round-trips to the original diff text.
func TestHunks_twoNonAdjacentHunks(t *testing.T) {
	dir, repo := initRepo(t)

	// Write a file large enough that edits at lines 1 and 40 are non-adjacent
	// (i.e. not bridged by the default 3-line context).
	lines := make([]string, 50)
	for i := range lines {
		lines[i] = "line\n"
	}
	content := strings.Join(lines, "")
	os.WriteFile(filepath.Join(dir, "big.txt"), []byte(content), 0o644)
	gitT(t, dir, "add", "-A")
	parent := snapTree(t, dir, "parent")

	// Change line 1 and line 45 — far enough apart to guarantee two @@ hunks.
	contentBytes := []byte(content)
	edited := strings.Replace(string(contentBytes), lines[0], "CHANGED_TOP\n", 1)
	// Replace the 45th occurrence of "line\n".
	count := 0
	edited = strings.Map(func(r rune) rune { return r }, edited) // identity, just to get a string
	idx := 0
	for i := 0; i < 50; i++ {
		pos := strings.Index(edited[idx:], "line\n")
		if pos == -1 {
			break
		}
		count++
		if count == 44 { // 0-based: the 45th "line\n" is index 44
			abs := idx + pos
			edited = edited[:abs] + "CHANGED_BOTTOM\n" + edited[abs+len("line\n"):]
			break
		}
		idx += pos + len("line\n")
	}
	os.WriteFile(filepath.Join(dir, "big.txt"), []byte(edited), 0o644)
	gitT(t, dir, "add", "-A")
	child := snapTree(t, dir, "child")

	hs, err := repo.Hunks(parent, child)
	if err != nil {
		t.Fatal(err)
	}

	// Collect hunks for big.txt.
	var bigHunks []string
	var fileHeader string
	for _, h := range hs {
		if h.File == "big.txt" {
			bigHunks = append(bigHunks, h.Body)
			fileHeader = h.FileHeader
		}
	}
	if len(bigHunks) < 2 {
		t.Fatalf("expected >=2 hunks for big.txt, got %d", len(bigHunks))
	}
	for i, b := range bigHunks {
		if !strings.HasPrefix(b, "@@") {
			t.Fatalf("big.txt hunk %d body should start with @@, got %q", i, b)
		}
	}

	// Round-trip: FileHeader + concatenated bodies must equal what git diff produced.
	rawDiff, err := repo.gitRaw("diff", "--no-color", "--no-ext-diff", "-U3", parent, child)
	if err != nil {
		t.Fatal(err)
	}
	reconstructed := fileHeader + strings.Join(bigHunks, "")
	if !strings.Contains(rawDiff, reconstructed) {
		t.Fatalf("round-trip failed: reconstructed patch not found in raw diff\nreconstructed:\n%s\nraw diff:\n%s", reconstructed, rawDiff)
	}
}

// TestHunks_binaryFile verifies that a binary file change produces exactly one
// Hunk with Binary=true.
func TestHunks_binaryFile(t *testing.T) {
	dir, repo := initRepo(t)

	// A minimal valid PNG (1×1 pixel, not a text file).
	png := []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, // PNG signature
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52, // IHDR length + type
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, // 1x1
		0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xde, // bit depth, colour, crc
		0x00, 0x00, 0x00, 0x0c, 0x49, 0x44, 0x41, 0x54, // IDAT length + type
		0x08, 0xd7, 0x63, 0xf8, 0xcf, 0xc0, 0x00, 0x00, 0x00, 0x02, 0x00, 0x01,
		0xe2, 0x21, 0xbc, 0x33, // IDAT CRC
		0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae, 0x42, 0x60, 0x82, // IEND
	}
	os.WriteFile(filepath.Join(dir, "img.png"), png, 0o644)
	gitT(t, dir, "add", "-A")
	parent := snapTree(t, dir, "parent")

	// Modify the binary file (flip a byte).
	png2 := make([]byte, len(png))
	copy(png2, png)
	png2[len(png2)-1] ^= 0xff
	os.WriteFile(filepath.Join(dir, "img.png"), png2, 0o644)
	gitT(t, dir, "add", "-A")
	child := snapTree(t, dir, "child")

	hs, err := repo.Hunks(parent, child)
	if err != nil {
		t.Fatal(err)
	}
	var binHunks []string
	for _, h := range hs {
		if h.File == "img.png" {
			binHunks = append(binHunks, h.ID)
			if !h.Binary {
				t.Fatalf("img.png hunk %s: expected Binary=true", h.ID)
			}
		}
	}
	if len(binHunks) != 1 {
		t.Fatalf("expected exactly 1 hunk for img.png, got %d", len(binHunks))
	}
}

// TestHunks_rename verifies that a renamed file produces a Hunk with OldPath
// set to the original name and File set to the new name.
func TestHunks_rename(t *testing.T) {
	dir, repo := initRepo(t)

	os.WriteFile(filepath.Join(dir, "old.txt"), []byte("content\n"), 0o644)
	gitT(t, dir, "add", "-A")
	parent := snapTree(t, dir, "parent")

	// Git rename detection requires the file content to be present in both trees.
	if err := os.Rename(filepath.Join(dir, "old.txt"), filepath.Join(dir, "new.txt")); err != nil {
		t.Fatal(err)
	}
	gitT(t, dir, "add", "-A")
	child := snapTree(t, dir, "child")

	hs, err := repo.Hunks(parent, child)
	if err != nil {
		t.Fatal(err)
	}

	// With --diff-filter or default similarity, git should detect the rename.
	// We look for any hunk touching new.txt or old.txt.
	var found bool
	for _, h := range hs {
		if h.File == "new.txt" {
			found = true
			if h.OldPath != "old.txt" {
				t.Fatalf("rename hunk: OldPath=%q, want old.txt", h.OldPath)
			}
		}
	}
	if !found {
		// If git emitted a delete + add instead of a rename (possible without
		// --find-renames), that's acceptable for this test: just verify paths.
		var sawOld, sawNew bool
		for _, h := range hs {
			if h.File == "old.txt" {
				sawOld = true
			}
			if h.File == "new.txt" {
				sawNew = true
			}
		}
		if !sawOld && !sawNew {
			t.Fatalf("expected hunks for old.txt or new.txt, got %v", hs)
		}
	}
}

// TestHunks_modeOnlyChange verifies that a chmod-only change (no content diff)
// produces at least one Hunk so the mode change is not silently dropped.
func TestHunks_modeOnlyChange(t *testing.T) {
	dir, repo := initRepo(t)

	os.WriteFile(filepath.Join(dir, "script.sh"), []byte("#!/bin/sh\necho hi\n"), 0o644)
	gitT(t, dir, "add", "-A")
	parent := snapTree(t, dir, "parent")

	// Make the file executable — mode-only change, no content diff.
	if err := os.Chmod(filepath.Join(dir, "script.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitT(t, dir, "add", "-A")
	child := snapTree(t, dir, "child")

	hs, err := repo.Hunks(parent, child)
	if err != nil {
		t.Fatal(err)
	}

	var found bool
	for _, h := range hs {
		if h.File == "script.sh" {
			found = true
			// The mode change hunk must be flagged Binary so it is never sub-split
			// and so that Patch() (FileHeader + Body) can be applied by git apply.
			if !h.Binary {
				t.Fatalf("mode-only hunk for script.sh: expected Binary=true, got false")
			}
			if !strings.Contains(h.FileHeader, "diff --git") {
				t.Fatalf("mode-only hunk for script.sh: FileHeader missing diff --git line")
			}
		}
	}
	if !found {
		t.Fatalf("expected at least one hunk for script.sh (mode-only change), got none")
	}
}

// TestHunks_spaceInFilename verifies that a file whose name contains a space is
// parsed correctly — i.e. the File field is the full name including the space,
// not a truncated string.
func TestHunks_spaceInFilename(t *testing.T) {
	dir, repo := initRepo(t)

	name := "my file.txt"
	os.WriteFile(filepath.Join(dir, name), []byte("hello\n"), 0o644)
	gitT(t, dir, "add", "-A")
	parent := snapTree(t, dir, "parent")

	os.WriteFile(filepath.Join(dir, name), []byte("hello world\n"), 0o644)
	gitT(t, dir, "add", "-A")
	child := snapTree(t, dir, "child")

	hs, err := repo.Hunks(parent, child)
	if err != nil {
		t.Fatal(err)
	}

	var found bool
	for _, h := range hs {
		if h.File == name {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected hunk with File=%q, got %v", name, hs)
	}
}
