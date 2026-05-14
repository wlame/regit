package split

import (
	"testing"
	"time"

	"github.com/wlame/regit/internal/model"
)

func TestCache_putThenGetHitAndMiss(t *testing.T) {
	c := NewCache(t.TempDir())
	fp := Fingerprint("difaaa", "openai/gpt-4o", "readme", "hint", 4)
	prop := model.SplitProposal{CommitHash: "abc", ShouldSplit: true}

	if _, ok := c.Get("abc", fp); ok {
		t.Fatal("expected miss before put")
	}
	if err := c.Put(CacheEntry{Version: 1, CommitHash: "abc", Fingerprint: fp, Proposal: prop, CreatedAt: time.Unix(0, 0)}); err != nil {
		t.Fatal(err)
	}
	got, ok := c.Get("abc", fp)
	if !ok {
		t.Fatal("expected hit after put")
	}
	if !got.ShouldSplit {
		t.Fatal("round-trip lost ShouldSplit")
	}
	// Different fingerprint must miss.
	if _, ok := c.Get("abc", Fingerprint("difaaa", "ollama/x", "readme", "hint", 4)); ok {
		t.Fatal("expected miss on fingerprint change")
	}
}

func TestFingerprint_changesWithInputs(t *testing.T) {
	a := Fingerprint("d", "m", "c", "h", 4)
	if a == Fingerprint("d", "m", "c", "h", 3) {
		t.Fatal("fingerprint should change with maxCommits")
	}
	if a == Fingerprint("d", "m", "c", "other", 4) {
		t.Fatal("fingerprint should change with userHint")
	}
	if a == Fingerprint("x", "m", "c", "h", 4) {
		t.Fatal("fingerprint should change with diffHash")
	}
	if a == Fingerprint("d", "m", "z", "h", 4) {
		t.Fatal("fingerprint should change with contextKey")
	}
}

// TestContentHash_distinguishesReadmeContent proves the helper that folds README
// content into the fingerprint yields different digests for different content — the
// mechanism that stops an edited README (or the TUI's empty README) from colliding
// with a stale cached proposal.
func TestContentHash_distinguishesReadmeContent(t *testing.T) {
	if contentHash("readme v1") == contentHash("readme v2") {
		t.Error("different README content must hash differently")
	}
	if contentHash("") == contentHash("some content") {
		t.Error("empty vs non-empty README must hash differently")
	}
	if contentHash("same") != contentHash("same") {
		t.Error("identical content must hash identically")
	}
}

func TestCache_putRejectsEmptyCommitHash(t *testing.T) {
	c := NewCache(t.TempDir())
	fp := Fingerprint("d", "m", "c", "h", 4)
	prop := model.SplitProposal{CommitHash: "abc", ShouldSplit: true}

	err := c.Put(CacheEntry{Version: 1, CommitHash: "", Fingerprint: fp, Proposal: prop, CreatedAt: time.Unix(0, 0)})
	if err == nil {
		t.Fatal("expected error on empty commit hash")
	}
}
