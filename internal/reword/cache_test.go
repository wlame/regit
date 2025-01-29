package reword

import (
	"testing"
	"time"
)

func TestRewordCache_putGetMiss(t *testing.T) {
	c := NewCache(t.TempDir())
	fp := Fingerprint("d", "m", "readme", "hint", 3)
	if _, ok := c.Get("h", fp); ok {
		t.Fatal("expected miss")
	}
	if err := c.Put(CacheEntry{CommitHash: "h", Fingerprint: fp,
		Result: Result{CommitHash: "h", Variants: []Variant{{Title: "T"}}}, CreatedAt: time.Unix(0, 0)}); err != nil {
		t.Fatal(err)
	}
	got, ok := c.Get("h", fp)
	if !ok || len(got.Variants) != 1 || got.Variants[0].Title != "T" {
		t.Fatalf("round-trip failed: %+v ok=%v", got, ok)
	}
	if _, ok := c.Get("h", Fingerprint("d", "m2", "readme", "hint", 3)); ok {
		t.Fatal("expected miss on fingerprint change")
	}
}

func TestRewordCache_emptyHashRejected(t *testing.T) {
	if err := NewCache(t.TempDir()).Put(CacheEntry{CommitHash: ""}); err == nil {
		t.Fatal("expected error for empty commit hash")
	}
}
