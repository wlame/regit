package gitio

import (
	"fmt"
	"strconv"
	"strings"
)

// FileStat is the change summary for a single file within a commit, as shown in
// the TUI's detail panel. Add/Del are line counts; for binary files both are 0
// and Binary is true (git reports "-" instead of a count). Status is git's
// single-letter code: A (added), M (modified), or D (deleted). M does double
// duty: it is also the fallback CommitStat assigns to an entry that
// --name-status did not classify (e.g. a mode-only change), so a caller should
// read M as "modified or otherwise-unclassified". Renames are not detected, so a
// moved file appears as a D + an A.
type FileStat struct {
	Status  string
	Path    string
	OldPath string // reserved for rename support; currently always ""
	Add     int
	Del     int
	Binary  bool
}

// Stat is the file-level change summary for one commit. Additions/Deletions are
// the totals across all non-binary files.
type Stat struct {
	Files     []FileStat
	Additions int
	Deletions int
}

// numstatRow is one parsed line of `git show --numstat` output.
type numstatRow struct {
	add    int
	del    int
	path   string
	binary bool
}

// CommitStat returns the file-change summary for a commit, diffed against its
// first parent (or, for a root commit, the empty tree — `git show` does this
// automatically). It runs two cheap git queries without rename detection so the
// --numstat and --name-status outputs line up file-for-file by plain path.
//
// It is meant to be called lazily from a tea.Cmd and cached by the caller: a
// large commit's diff can take a beat, so it must never run on the UI goroutine.
func (r *Repo) CommitStat(hash string) (Stat, error) {
	numOut, err := r.gitRaw("show", "--no-color", "--no-ext-diff", "--format=", "--numstat", hash)
	if err != nil {
		return Stat{}, fmt.Errorf("numstat %s: %w", hash, err)
	}
	nameOut, err := r.gitRaw("show", "--no-color", "--no-ext-diff", "--format=", "--name-status", hash)
	if err != nil {
		return Stat{}, fmt.Errorf("name-status %s: %w", hash, err)
	}

	statusByPath := parseNameStatus(nameOut)

	var st Stat
	for _, ns := range parseNumstat(numOut) {
		status := statusByPath[ns.path]
		if status == "" {
			status = "M" // mode-only or unmatched changes read as a modify
		}
		fs := FileStat{Status: status, Path: ns.path, Add: ns.add, Del: ns.del, Binary: ns.binary}
		if !fs.Binary {
			st.Additions += fs.Add
			st.Deletions += fs.Del
		}
		st.Files = append(st.Files, fs)
	}
	return st, nil
}

// parseNumstat parses `git show --numstat` lines of the form "<add>\t<del>\t<path>".
// A binary file is reported as "-\t-\t<path>" → Binary=true, counts 0.
func parseNumstat(out string) []numstatRow {
	var rows []numstatRow
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) != 3 {
			continue
		}
		row := numstatRow{path: fields[2]}
		if fields[0] == "-" || fields[1] == "-" {
			row.binary = true
		} else {
			row.add, _ = strconv.Atoi(fields[0])
			row.del, _ = strconv.Atoi(fields[1])
		}
		rows = append(rows, row)
	}
	return rows
}

// parseNameStatus parses `git show --name-status` lines ("M\t<path>") into a
// path → status map. Without -M every line is "<A|M|D>\t<path>", so a simple
// two-field split is enough.
func parseNameStatus(out string) map[string]string {
	byPath := make(map[string]string)
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 2)
		if len(fields) != 2 {
			continue
		}
		status := strings.TrimSpace(fields[0])
		if status != "" {
			byPath[fields[1]] = status[:1] // first letter: A, M, or D
		}
	}
	return byPath
}
