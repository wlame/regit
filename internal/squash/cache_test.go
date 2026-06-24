package squash

import (
	"testing"
	"time"
)

func TestCache_putGetRoundTrip(t *testing.T) {
	dir := t.TempDir()
	c := NewCache(dir)
	fp := Fingerprint("chainKeyABC", "http:llama", "false\x1f")
	want := Proposal{Subject: "Add user model", Body: "Combined body."}
	if err := c.Put(CacheEntry{Key: "last123", Fingerprint: fp, Payload: want, CreatedAt: time.Unix(0, 0)}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, ok := c.Get("last123", fp)
	if !ok {
		t.Fatal("expected cache hit")
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestCache_missOnFingerprintChange(t *testing.T) {
	dir := t.TempDir()
	c := NewCache(dir)
	c.Put(CacheEntry{Key: "last123", Fingerprint: Fingerprint("k", "m", "a"), Payload: Proposal{Subject: "x"}, CreatedAt: time.Unix(0, 0)})
	if _, ok := c.Get("last123", Fingerprint("k", "m", "b")); ok {
		t.Error("a different fingerprint must miss")
	}
}

func TestCache_clearRemovesEntries(t *testing.T) {
	dir := t.TempDir()
	c := NewCache(dir)
	c.Put(CacheEntry{Key: "h", Fingerprint: "fp", Payload: Proposal{Subject: "x"}, CreatedAt: time.Unix(0, 0)})
	if err := c.Clear(); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Get("h", "fp"); ok {
		t.Error("cache should be empty after Clear")
	}
}

// TestCache_putEmptyHashRefuses verifies that Put rejects entries with an
// empty commit hash rather than silently writing an invalid cache file.
func TestCache_putEmptyHashRefuses(t *testing.T) {
	c := NewCache(t.TempDir())
	err := c.Put(CacheEntry{
		Key:         "",
		Fingerprint: "abc",
		Payload:     Proposal{Subject: "x"},
	})
	if err == nil {
		t.Error("Put with empty CommitHash should return an error")
	}
}
