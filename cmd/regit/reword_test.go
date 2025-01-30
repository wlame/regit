package main

import (
	"context"
	"testing"

	"github.com/wlame/regit/internal/reword"
)

func TestParseRewordFlags_defaultsAndList(t *testing.T) {
	f, err := parseRewordFlags([]string{"HEAD~2", "HEAD", "--variants=4", "--llm=ollama/x", "--yes"})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Commits) != 2 || f.Commits[0] != "HEAD~2" || f.Commits[1] != "HEAD" {
		t.Fatalf("commits = %v", f.Commits)
	}
	if f.Variants != 4 || !f.Yes || f.LLM != "ollama/x" {
		t.Fatalf("flags = %+v", f)
	}
}

func TestParseRewordFlags_defaultsHEAD(t *testing.T) {
	f, _ := parseRewordFlags([]string{"--llm=ollama/x"})
	if len(f.Commits) != 1 || f.Commits[0] != "HEAD" {
		t.Fatalf("commits = %v", f.Commits)
	}
}

// TestGenerateAhead_prefetchesNext verifies that the pipeline worker prefetches
// item N+1 into the buffer while the consumer has item N in hand. This is done
// purely with channels — no real clock or sleep.
func TestGenerateAhead_prefetchesNext(t *testing.T) {
	// Three items; we will consume them in order via the returned channel.
	items := []int{1, 2, 3}

	// genStarted[i] is closed when gen starts producing item i.
	// genRelease[i] is closed by the test to let gen finish item i.
	genStarted := make([]chan struct{}, len(items))
	genRelease := make([]chan struct{}, len(items))
	for i := range items {
		genStarted[i] = make(chan struct{})
		genRelease[i] = make(chan struct{})
	}

	gen := func(ctx context.Context, item int) (reword.Result, error) {
		// Signal that we've started generating this item.
		close(genStarted[item-1])

		// Block until the test releases us.
		select {
		case <-genRelease[item-1]:
		case <-ctx.Done():
			return reword.Result{}, ctx.Err()
		}
		return reword.Result{CommitHash: string(rune('0' + item))}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// buffer=1: the worker can produce one item ahead of consumption.
	ch := generateAhead(ctx, items, gen, 1)

	// Wait for item 1 to start generating.
	<-genStarted[0]

	// Release item 1 — worker sends result to channel (buffer=1, so it fits).
	close(genRelease[0])

	// Wait for item 2 to start generating (it should start as soon as item 1
	// is sent, since the buffer has space).
	<-genStarted[1]

	// At this point item 2 is being generated. Now consume item 1 from the channel.
	r1 := <-ch
	if r1.err != nil {
		t.Fatalf("item 1: unexpected error: %v", r1.err)
	}

	// Release item 2; item 3 should start.
	close(genRelease[1])
	<-genStarted[2]

	// Consume item 2.
	r2 := <-ch
	if r2.err != nil {
		t.Fatalf("item 2: unexpected error: %v", r2.err)
	}

	// Release item 3 and consume it.
	close(genRelease[2])
	r3 := <-ch
	if r3.err != nil {
		t.Fatalf("item 3: unexpected error: %v", r3.err)
	}

	// Verify results came in order.
	if r1.result.CommitHash != "1" || r2.result.CommitHash != "2" || r3.result.CommitHash != "3" {
		t.Fatalf("results out of order: %v %v %v", r1.result.CommitHash, r2.result.CommitHash, r3.result.CommitHash)
	}

	// Channel must be closed after all items.
	if _, open := <-ch; open {
		t.Fatal("channel not closed after all items consumed")
	}
}
