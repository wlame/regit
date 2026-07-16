package model

import (
	"fmt"
	"regexp"
)

type RedactMode int

const (
	RedactRemove  RedactMode = iota // delete each matched line entirely
	RedactReplace                   // apply Rule within each matched line
)

// RedactRule is a compiled replace rule: Pattern → Replacement ($1 groups).
type RedactRule struct {
	Pattern     string // user-supplied Go regex source (kept for logging/cache keys)
	Replacement string // may be "" (strip the secret, keep the line)
}

// RedactVersion is one state of a tracked line at some commit.
type RedactVersion struct {
	Commit string // full hash where this content first appears
	Path   string // repo-relative path at that commit (renames tracked)
	Line   string // full line content, EOL excluded
}

// RedactOccurrence is one introduction of the secret plus the line's later
// states (the cascade). Context is the ±2 diff lines around the introduction.
type RedactOccurrence struct {
	Intro       RedactVersion
	Context     []string
	Descendants []RedactVersion // ordered oldest→newest; empty when the line was never edited later
	ChainNote   string          // "" or why the chain ended early (ambiguous multi-line rewrite)
}

// RedactSpec is the validated selection driving an apply: per-path line
// contents to act on, the mode, and (replace mode) the rule.
type RedactSpec struct {
	Match   string              // the literal that was searched (logging + absence check)
	Mode    RedactMode
	Rule    RedactRule          // used iff Mode == RedactReplace
	Targets map[string][]string // path → full line contents to act on in that path
}

// Validate rejects a spec that could silently do nothing or do the wrong
// thing: an empty match (nothing to verify absence of), empty targets
// (nothing to act on), or a mode/rule mismatch (a rule on remove mode is a
// sign the caller mixed flags up — fail fast rather than ignore it).
func (s RedactSpec) Validate() error {
	if s.Match == "" {
		return fmt.Errorf("redact: match must not be empty")
	}
	if len(s.Targets) == 0 {
		return fmt.Errorf("redact: no target lines selected")
	}
	for p, lines := range s.Targets {
		if p == "" {
			return fmt.Errorf("redact: empty target path")
		}
		if len(lines) == 0 {
			return fmt.Errorf("redact: path %q has no target lines", p)
		}
		for _, l := range lines {
			if l == "" {
				return fmt.Errorf("redact: path %q has an empty target line", p)
			}
		}
	}
	switch s.Mode {
	case RedactRemove:
		if s.Rule.Pattern != "" || s.Rule.Replacement != "" {
			return fmt.Errorf("redact: replace rule given but mode is remove")
		}
	case RedactReplace:
		if _, err := s.Rule.Compile(); err != nil {
			return fmt.Errorf("redact: replace pattern: %w", err)
		}
	default:
		return fmt.Errorf("redact: unknown mode %d", s.Mode)
	}
	return nil
}

// Compile builds the rule's regexp. An empty pattern is an error here (the
// replace path must never silently match everything).
func (r RedactRule) Compile() (*regexp.Regexp, error) {
	if r.Pattern == "" {
		return nil, fmt.Errorf("empty replace pattern")
	}
	return regexp.Compile(r.Pattern)
}
