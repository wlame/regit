package docs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DocFile binds one documentation file to the generated blocks it carries.
// This table drives Sync, Check, and the drift tests — adding a block means a
// renderer in blocks.go, a name here, and a marker pair in the file.
type DocFile struct {
	Path   string   // repo-root-relative
	Blocks []string // marker names the file must contain
}

// Files returns the documentation files under drift control.
func Files() []DocFile {
	return []DocFile{
		{Path: "README.md", Blocks: []string{"keys", "commands", "strategies", "time-models", "env-vars"}},
		{Path: "AGENTS.md", Blocks: []string{"verbs-overview", "verbs-detail", "runfiles-map"}},
	}
}

// FindRoot walks up from dir to the nearest directory containing go.mod — the
// repo root, where README.md and AGENTS.md live.
func FindRoot(dir string) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.mod found above %s", dir)
		}
		dir = parent
	}
}

// fileBlocks selects the subset of all a file declares, so a marker belonging
// to another file is reported as unknown rather than silently spliced.
func fileBlocks(f DocFile, all map[string]string) map[string]string {
	sub := make(map[string]string, len(f.Blocks))
	for _, n := range f.Blocks {
		if content, ok := all[n]; ok {
			sub[n] = content
		}
	}
	return sub
}

// regenerate returns f's current and freshly spliced contents. It errors when
// the file lacks a marker pair for any declared block — a missing marker would
// otherwise silently drop that block from drift protection.
func regenerate(root string, f DocFile, all map[string]string) (raw, next string, err error) {
	data, err := os.ReadFile(filepath.Join(root, f.Path))
	if err != nil {
		return "", "", err
	}
	raw = string(data)
	// Recognize a begin marker the same way SpliceBlocks does — a whole
	// trimmed line, not merely a substring — so a malformed line like
	// "<!-- regit:begin keys --> trailing" is reported as missing here
	// rather than silently passing this check and then being skipped by
	// the splicer.
	present := map[string]bool{}
	for _, line := range strings.Split(raw, "\n") {
		if name, ok := parseMarker(strings.TrimSpace(line), beginPrefix); ok {
			present[name] = true
		}
	}
	for _, n := range f.Blocks {
		if !present[n] {
			return "", "", fmt.Errorf("%s: missing markers for block %q", f.Path, n)
		}
	}
	next, err = SpliceBlocks(raw, fileBlocks(f, all))
	if err != nil {
		return "", "", fmt.Errorf("%s: %w", f.Path, err)
	}
	return raw, next, nil
}

// Sync rewrites every drifted doc file in place and returns the paths changed.
func Sync(root string) ([]string, error) {
	all := Blocks()
	var changed []string
	for _, f := range Files() {
		raw, next, err := regenerate(root, f, all)
		if err != nil {
			return changed, err
		}
		if next != raw {
			if err := os.WriteFile(filepath.Join(root, f.Path), []byte(next), 0o644); err != nil {
				return changed, err
			}
			changed = append(changed, f.Path)
		}
	}
	return changed, nil
}

// Check reports which doc files would change under Sync, writing nothing.
func Check(root string) ([]string, error) {
	all := Blocks()
	var drifted []string
	for _, f := range Files() {
		raw, next, err := regenerate(root, f, all)
		if err != nil {
			return drifted, err
		}
		if next != raw {
			drifted = append(drifted, f.Path)
		}
	}
	return drifted, nil
}
