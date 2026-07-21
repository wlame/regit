package docs

import (
	"fmt"
	"strings"
)

// Marker grammar: a begin/end pair each alone on its own line delimits one
// generated block. Everything between the markers is replaced on splice, so
// hand edits inside a block never survive `just docs-sync` — by design.
const (
	beginPrefix  = "<!-- regit:begin "
	endPrefix    = "<!-- regit:end "
	markerSuffix = " -->"
)

// BeginMarker renders the opening marker line for a block name.
func BeginMarker(name string) string { return beginPrefix + name + markerSuffix }

// EndMarker renders the closing marker line for a block name.
func EndMarker(name string) string { return endPrefix + name + markerSuffix }

// parseMarker extracts the block name when line is exactly one marker of the
// given kind (ignoring surrounding whitespace handled by the caller).
//
// The length check is load-bearing, not defensive noise: HasPrefix and HasSuffix
// can BOTH be true while the two overlap, which happens for a marker with an
// empty name ("<!-- regit:end -->" is one byte shorter than prefix+suffix). The
// slice below would then run backwards and panic. A nameless marker is not a
// marker, so reporting ok=false is also the semantically right answer — the
// caller emits its "unknown block" error instead of crashing.
func parseMarker(line, prefix string) (string, bool) {
	if len(line) < len(prefix)+len(markerSuffix) {
		return "", false
	}
	if !strings.HasPrefix(line, prefix) || !strings.HasSuffix(line, markerSuffix) {
		return "", false
	}
	return line[len(prefix) : len(line)-len(markerSuffix)], true
}

// SpliceBlocks replaces the content between each begin/end marker pair in src
// with blocks[name] and returns the new document. It errors on: a begin whose
// name is not in blocks, a begin without its end, an end without a begin, a
// nested begin, a mismatched end, or the same block spliced twice. The
// operation is idempotent: splicing its own output changes nothing.
func SpliceBlocks(src string, blocks map[string]string) (string, error) {
	lines := strings.Split(src, "\n")
	var out []string
	seen := map[string]bool{}
	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if name, ok := parseMarker(trimmed, endPrefix); ok {
			return "", fmt.Errorf("end marker %q without a begin", name)
		}
		name, ok := parseMarker(trimmed, beginPrefix)
		if !ok {
			out = append(out, lines[i])
			continue
		}
		content, known := blocks[name]
		if !known {
			return "", fmt.Errorf("unknown block %q (not declared for this file in Files())", name)
		}
		if seen[name] {
			return "", fmt.Errorf("duplicate begin marker %q", name)
		}
		seen[name] = true
		end := -1
		for j := i + 1; j < len(lines); j++ {
			t := strings.TrimSpace(lines[j])
			if n, isBegin := parseMarker(t, beginPrefix); isBegin {
				return "", fmt.Errorf("nested begin marker %q inside %q", n, name)
			}
			if n, isEnd := parseMarker(t, endPrefix); isEnd {
				if n != name {
					return "", fmt.Errorf("end marker %q closes begin %q", n, name)
				}
				end = j
				break
			}
		}
		if end == -1 {
			return "", fmt.Errorf("begin marker %q without an end", name)
		}
		out = append(out, lines[i])
		out = append(out, strings.Split(content, "\n")...)
		out = append(out, lines[end])
		i = end
	}
	return strings.Join(out, "\n"), nil
}
