// Package termsafe sanitizes untrusted text before it is written to a terminal.
//
// Commit subjects, author names, file paths, and LLM output can contain arbitrary
// bytes — including raw ANSI/OSC escape sequences. Rendered verbatim, those could
// rewrite the terminal's window title, drive an OSC-52 clipboard write, move the
// cursor, or spoof regit's own prompts. Every repo- or model-derived string that
// reaches the screen (or the command log) is passed through Line or Block first so
// what renders is inert text with the escapes and stray control bytes removed.
package termsafe

import (
	"strings"
	"unicode/utf8"
)

// Line sanitizes text for a single-line context (a list row, a header field, a
// truncated subject). Escape sequences and control bytes are removed, and any
// newline or tab is folded to a single space so the value can never break the
// layout by spanning rows.
func Line(s string) string { return sanitize(s, false) }

// Block sanitizes text for a multi-line context (a detail-panel body). It is like
// Line but preserves newlines and tabs, since those are legitimate structure in a
// commit body; only escape sequences and other control bytes are removed.
func Block(s string) string { return sanitize(s, true) }

// sanitize walks s byte-by-byte, dropping ESC-introduced sequences (CSI, OSC, and
// lone ESC), C0/C1/DEL control bytes, and raw invalid UTF-8 bytes (which include a
// naked 8-bit CSI/OSC). keepStructural governs whether \n and \t survive (Block) or
// fold to a space (Line). Valid multi-byte UTF-8 is passed through unchanged.
func sanitize(s string, keepStructural bool) string {
	if !hasControlByte(s) { // fast path: nothing to strip
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == 0x1b: // ESC — skip the whole sequence it introduces
			i = skipEscapeSeq(s, i)
		case c == '\n' || c == '\t':
			if keepStructural {
				b.WriteByte(c)
			} else {
				b.WriteByte(' ')
			}
			i++
		case c < 0x20 || c == 0x7f: // other C0 controls + DEL
			i++
		case c < 0x80: // printable ASCII
			b.WriteByte(c)
			i++
		default: // non-ASCII: decode one rune
			r, size := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError && size == 1 {
				i++ // raw invalid byte (e.g. an 8-bit 0x9b CSI) — drop
				continue
			}
			if r >= 0x80 && r <= 0x9f { // valid C1 control — drop
				i += size
				continue
			}
			b.WriteString(s[i : i+size])
			i += size
		}
	}
	return b.String()
}

// hasControlByte reports whether s contains any byte that sanitize would strip or
// fold, so the common escape-free string skips allocation.
func hasControlByte(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c == 0x1b || c < 0x20 || c == 0x7f || (c >= 0x80 && c <= 0x9f) {
			return true
		}
	}
	return false
}

// skipEscapeSeq returns the index to resume from after the escape sequence that
// begins at s[i] (which must be ESC). CSI/OSC sequences are consumed whole; any
// other ESC drops only the ESC byte, leaving the following byte to render as a
// harmless literal (with the ESC gone, it can no longer trigger anything).
func skipEscapeSeq(s string, i int) int {
	if i+1 >= len(s) {
		return i + 1 // lone trailing ESC
	}
	switch s[i+1] {
	case '[': // CSI: params/intermediates until a final byte in 0x40–0x7e
		j := i + 2
		for j < len(s) && !(s[j] >= 0x40 && s[j] <= 0x7e) {
			j++
		}
		if j < len(s) {
			j++ // include the final byte
		}
		return j
	case ']': // OSC: string terminated by BEL (0x07) or ST (ESC \)
		j := i + 2
		for j < len(s) {
			if s[j] == 0x07 {
				return j + 1
			}
			if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
				return j + 2
			}
			j++
		}
		return j
	default:
		return i + 1 // drop only the ESC, keep the following byte
	}
}
