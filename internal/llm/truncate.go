package llm

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// MaxPromptDiffBytes bounds the total diff text embedded in any single LLM
// prompt (split hunks, reword diff, squash member diffs). Without a cap, one
// giant commit turns into an oversized request — provider 4xx errors at best,
// runaway token cost at worst. 192 KiB is roughly 48k tokens of diff, far more
// context than any of the prompts need to do their job.
const MaxPromptDiffBytes = 192 * 1024

// minDiffShareBytes floors the per-item budget when MaxPromptDiffBytes is
// divided across many hunks or member diffs, so each item always shows at
// least a meaningful excerpt.
const minDiffShareBytes = 1024

// TruncateForPrompt returns s unchanged when it fits in max bytes; otherwise
// it cuts at the last line boundary inside the budget and appends a marker
// naming how much was dropped, so the model knows it is seeing an excerpt.
// The cut never lands inside a UTF-8 sequence.
func TruncateForPrompt(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	cut := max
	if i := strings.LastIndexByte(s[:max], '\n'); i > 0 {
		cut = i
	}
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + fmt.Sprintf("\n… [diff truncated: %d of %d bytes shown]", cut, len(s))
}

// DiffShareBytes divides the prompt-wide diff budget across n items (hunks,
// member diffs), flooring at minDiffShareBytes so every item keeps a useful
// excerpt even in a very large set.
func DiffShareBytes(n int) int {
	if n <= 0 {
		return MaxPromptDiffBytes
	}
	share := MaxPromptDiffBytes / n
	if share < minDiffShareBytes {
		return minDiffShareBytes
	}
	return share
}
