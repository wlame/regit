package promptcache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type payload struct {
	Text string
	N    int
}

func newCache(t *testing.T) *Cache[payload] {
	t.Helper()
	return New[payload](t.TempDir(), "test")
}

// TestCache_putThenGet covers the hit/miss contract: a stored entry hits on
// the same key+fingerprint, misses on a different fingerprint or key.
func TestCache_putThenGet(t *testing.T) {
	c := newCache(t)
	fp := Fingerprint("diff", "model", "ctx")
	if _, ok := c.Get("abc", fp); ok {
		t.Fatal("expected miss before put")
	}
	want := payload{Text: "hello", N: 7}
	if err := c.Put(Entry[payload]{Key: "abc", Fingerprint: fp, Payload: want, CreatedAt: time.Unix(0, 0)}); err != nil {
		t.Fatal(err)
	}
	got, ok := c.Get("abc", fp)
	if !ok || got != want {
		t.Fatalf("Get = %+v, %v; want %+v, true", got, ok, want)
	}
	if _, ok := c.Get("abc", Fingerprint("other")); ok {
		t.Error("different fingerprint should miss")
	}
	if _, ok := c.Get("zzz", fp); ok {
		t.Error("different key should miss")
	}
}

// TestCache_versionMismatchIsMiss verifies an entry written under another
// schema version — e.g. a leftover v1 file from the per-feature caches —
// never hits, even with a matching fingerprint.
func TestCache_versionMismatchIsMiss(t *testing.T) {
	c := newCache(t)
	fp := Fingerprint("x")
	if err := c.Put(Entry[payload]{Key: "k", Fingerprint: fp, Payload: payload{Text: "v"}}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(c.Path("k"))
	if err != nil {
		t.Fatal(err)
	}
	var e Entry[payload]
	if err := json.Unmarshal(data, &e); err != nil {
		t.Fatal(err)
	}
	e.Version = Version - 1
	stale, _ := json.Marshal(e)
	if err := os.WriteFile(c.Path("k"), stale, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Get("k", fp); ok {
		t.Error("stale-version entry must be a miss")
	}
}

// TestCache_corruptFileIsMiss verifies unreadable JSON degrades to a miss.
func TestCache_corruptFileIsMiss(t *testing.T) {
	c := newCache(t)
	if err := c.Put(Entry[payload]{Key: "k", Fingerprint: "f", Payload: payload{}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.Path("k"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Get("k", "f"); ok {
		t.Error("corrupt file must be a miss")
	}
}

// TestCache_putRejectsEmptyKey guards the empty-key refusal.
func TestCache_putRejectsEmptyKey(t *testing.T) {
	if err := newCache(t).Put(Entry[payload]{Key: ""}); err == nil {
		t.Fatal("empty key must be rejected")
	}
}

// TestCache_clearRemovesEverything covers Clear.
func TestCache_clearRemovesEverything(t *testing.T) {
	c := newCache(t)
	if err := c.Put(Entry[payload]{Key: "k", Fingerprint: "f"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Clear(); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Get("k", "f"); ok {
		t.Error("entry survived Clear")
	}
}

// TestFingerprint_fieldBoundaries verifies adjacent fields cannot collide and
// output is stable.
func TestFingerprint_fieldBoundaries(t *testing.T) {
	if Fingerprint("ab", "c") == Fingerprint("a", "bc") {
		t.Error("field boundary collision")
	}
	if Fingerprint("a", "b") != Fingerprint("a", "b") {
		t.Error("fingerprint not deterministic")
	}
	if got := len(Fingerprint("x")); got != 32 {
		t.Errorf("fingerprint length = %d, want 32", got)
	}
}

// TestContentHash_distinguishesContent covers the short content digest.
func TestContentHash_distinguishesContent(t *testing.T) {
	if ContentHash("readme v1") == ContentHash("readme v2") {
		t.Error("different content must hash differently")
	}
	if ContentHash("same") != ContentHash("same") {
		t.Error("content hash not deterministic")
	}
}

// TestCache_putIsAtomic hammers one key with concurrent Puts and verifies a
// reader always sees a complete entry — under the old direct WriteFile, two
// interleaved writers could leave a torn JSON file — and that no temp files
// leak afterwards.
func TestCache_putIsAtomic(t *testing.T) {
	c := newCache(t)
	big := strings.Repeat("x", 64*1024)
	fp := Fingerprint("f")

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				_ = c.Put(Entry[payload]{Key: "k", Fingerprint: fp, Payload: payload{Text: big, N: n}})
				if got, ok := c.Get("k", fp); ok && got.Text != big {
					t.Errorf("torn read: payload text length %d", len(got.Text))
					return
				}
			}
		}(w)
	}
	wg.Wait()

	got, ok := c.Get("k", fp)
	if !ok || got.Text != big {
		t.Fatalf("final entry incomplete (ok=%v, len=%d)", ok, len(got.Text))
	}
	leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(c.Path("k")), "*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) > 0 {
		t.Errorf("temp files leaked: %v", leftovers)
	}
}
