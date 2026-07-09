package docs

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/wlame/regit/internal/capability"
)

// verbBlock is one fenced bash block in the README that documents a verb.
type verbBlock struct {
	verb     string
	line     int
	flags    map[string]bool // every --flag mentioned (outside quoted strings)
	flagList bool            // true when the block is a per-verb flag LIST, not just examples
}

var quotedSegment = regexp.MustCompile(`"[^"]*"|'[^']*'`)

// readmeVerbBlocks extracts every fenced bash block whose first line invokes a
// registered verb, recording the flags it mentions. A block whose first line
// carries no flags and that then documents 3+ lines of leading "--flag" rows is
// classified as a flag LIST (the documentation style the split/reword sections
// use), which must cover the verb's full registry surface.
func readmeVerbBlocks(t *testing.T, readme string) []verbBlock {
	t.Helper()
	var blocks []verbBlock
	lines := strings.Split(readme, "\n")
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "```bash" {
			continue
		}
		start := i + 1
		end := start
		for end < len(lines) && strings.TrimSpace(lines[end]) != "```" {
			end++
		}
		if start >= len(lines) {
			break
		}
		first := strings.Fields(lines[start])
		if len(first) < 2 || first[0] != "regit" {
			i = end
			continue
		}
		if _, ok := capability.Lookup(first[1]); !ok {
			i = end
			continue
		}
		b := verbBlock{verb: first[1], line: start + 1, flags: map[string]bool{}}
		leadingFlagRows := 0
		// A block can mix invocations ("regit redate …" examples followed by
		// "regit --help"): each unindented "regit …" line switches the command
		// context, and only flags belonging to the block's verb are collected.
		curVerb := b.verb
		for _, ln := range lines[start:end] {
			if f := strings.Fields(ln); len(f) > 0 && !strings.HasPrefix(ln, " ") && f[0] == "regit" {
				curVerb = ""
				if len(f) > 1 && !strings.HasPrefix(f[1], "-") {
					curVerb = f[1]
				}
			}
			if curVerb != b.verb {
				continue
			}
			// Quoted values may legitimately contain "--words" (e.g. the
			// --agent-args example); they are not flags of the verb.
			clean := quotedSegment.ReplaceAllString(ln, `""`)
			if c := strings.Index(clean, "#"); c >= 0 {
				clean = clean[:c]
			}
			if strings.HasPrefix(strings.TrimLeft(ln, " \t"), "--") && strings.HasPrefix(ln, " ") {
				leadingFlagRows++
			}
			for _, tok := range strings.Fields(clean) {
				if !strings.HasPrefix(tok, "--") {
					continue
				}
				name, _, _ := strings.Cut(strings.TrimPrefix(tok, "--"), "=")
				if name != "" {
					b.flags[name] = true
				}
			}
		}
		firstHasFlags := false
		for _, tok := range first {
			if strings.HasPrefix(tok, "--") {
				firstHasFlags = true
			}
		}
		b.flagList = !firstHasFlags && leadingFlagRows >= 3
		blocks = append(blocks, b)
		i = end
	}
	return blocks
}

// TestREADME_verbFlagBlocksMatchRegistry drift-protects the hand-written
// per-verb documentation: every --flag a bash block mentions must exist in the
// verb's registry row (no stale flags survive a rename), and a block written
// as a flag LIST must document the verb's complete flag surface (a new
// registry Param cannot be forgotten).
func TestREADME_verbFlagBlocksMatchRegistry(t *testing.T) {
	root, err := FindRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}

	blocks := readmeVerbBlocks(t, string(data))
	if len(blocks) == 0 {
		t.Fatal("no verb bash blocks found in README.md — the extractor is broken")
	}
	flagLists := 0
	for _, b := range blocks {
		cmd, _ := capability.Lookup(b.verb)
		// help/llm-help/version are dispatch-level flags (cmd/regit), valid on
		// any verb without being registry Params.
		valid := map[string]bool{"help": true, "llm-help": true, "version": true}
		for _, p := range cmd.Params {
			valid[p.Name] = true
		}
		for f := range b.flags {
			if !valid[f] {
				t.Errorf("README.md:%d documents --%s for %q, which is not in the registry", b.line, f, b.verb)
			}
		}
		if !b.flagList {
			continue
		}
		flagLists++
		for _, p := range cmd.Params {
			if !b.flags[p.Name] {
				t.Errorf("README.md:%d: %q flag list is missing --%s (%s)", b.line, b.verb, p.Name, p.Help)
			}
		}
	}
	if flagLists == 0 {
		t.Error("no flag-list blocks detected — the classifier is broken (split/reword document one each)")
	}
}
