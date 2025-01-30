package reword

import (
	"encoding/json"
	"os"
	"path/filepath"
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

func TestRewordCache_versionMismatchMiss(t *testing.T) {
	gitDir := t.TempDir()
	c := NewCache(gitDir)
	fp := Fingerprint("d", "m", "readme", "hint", 3)
	// Write a valid entry via Put.
	if err := c.Put(CacheEntry{
		CommitHash:  "h1",
		Fingerprint: fp,
		Result:      Result{CommitHash: "h1", Variants: []Variant{{Title: "T"}}},
		CreatedAt:   time.Unix(0, 0),
	}); err != nil {
		t.Fatal(err)
	}
	// Manually read the file and overwrite with a mismatched Version.
	cacheFile := filepath.Join(gitDir, "regit", "cache", "reword", "h1.json")
	data, err := os.ReadFile(cacheFile)
	if err != nil {
		t.Fatal(err)
	}
	var e CacheEntry
	if err := json.Unmarshal(data, &e); err != nil {
		t.Fatal(err)
	}
	e.Version = 999 // Incompatible version.
	corruptedData, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cacheFile, corruptedData, 0o644); err != nil {
		t.Fatal(err)
	}
	// Get should return a miss due to version mismatch.
	if _, ok := c.Get("h1", fp); ok {
		t.Fatal("expected miss on version mismatch")
	}
}

func TestRewordCache_corruptJSONMiss(t *testing.T) {
	gitDir := t.TempDir()
	c := NewCache(gitDir)
	// Manually create the cache directory and a corrupt JSON file.
	cacheDir := filepath.Join(gitDir, "regit", "cache", "reword")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	corruptFile := filepath.Join(cacheDir, "h2.json")
	if err := os.WriteFile(corruptFile, []byte("{invalid json}"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Get should return a miss without panicking.
	if _, ok := c.Get("h2", Fingerprint("d", "m", "readme", "hint", 3)); ok {
		t.Fatal("expected miss on corrupt JSON")
	}
}
