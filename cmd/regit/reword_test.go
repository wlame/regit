package main

import (
	"context"
	"strings"
	"testing"

	"github.com/wlame/regit/internal/config"
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

// TestParseRewordFlags_commitAfterFlag verifies that a positional commit-ish
// appearing after a flag (e.g. --yes HEAD~2) is not silently dropped.
func TestParseRewordFlags_commitAfterFlag(t *testing.T) {
	f, err := parseRewordFlags([]string{"--yes", "HEAD~2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Commits) != 1 || f.Commits[0] != "HEAD~2" {
		t.Fatalf("commits = %v, want [HEAD~2]", f.Commits)
	}
	if !f.Yes {
		t.Fatal("expected Yes=true")
	}
}

// TestParseRewordFlags_agentScan verifies that --agent-scan is parsed correctly.
func TestParseRewordFlags_agentScan(t *testing.T) {
	f, err := parseRewordFlags([]string{"--agent=claude", "--agent-scan"})
	if err != nil {
		t.Fatal(err)
	}
	if !f.AgentScan {
		t.Fatal("expected AgentScan=true")
	}
	if f.Agent != "claude" {
		t.Fatalf("expected Agent=claude, got %q", f.Agent)
	}
}

// TestValidateRewordFlags_agentScanRequiresAgent verifies that --agent-scan
// without --agent is rejected with a clear error message.
func TestValidateRewordFlags_agentScanRequiresAgent(t *testing.T) {
	tests := []struct {
		name    string
		f       rewordFlags
		wantErr bool
		errText string
	}{
		{
			name:    "--agent-scan without --agent",
			f:       rewordFlags{AgentScan: true, Agent: ""},
			wantErr: true,
			errText: "--agent-scan requires --agent",
		},
		{
			name:    "--agent-scan with --agent",
			f:       rewordFlags{AgentScan: true, Agent: "claude"},
			wantErr: false,
		},
		{
			name:    "--agent-scan=false (no requirement)",
			f:       rewordFlags{AgentScan: false},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRewordFlags(tt.f)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateRewordFlags() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && !strings.Contains(err.Error(), tt.errText) {
				t.Fatalf("error %q does not contain %q", err.Error(), tt.errText)
			}
		})
	}
}

// TestBuildRewordBackend_agentScanBuildsInDirBackend verifies that
// buildRewordBackend with --agent-scan + --agent returns a non-nil backend
// without error (the backend type isn't exported, so we just check no error).
func TestBuildRewordBackend_agentScanBuildsBackend(t *testing.T) {
	f := rewordFlags{Agent: "claude", AgentScan: true}
	b, err := buildRewordBackend(config.Config{}, f, "/tmp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b == nil {
		t.Fatal("expected non-nil backend")
	}
	// The backend name includes the command.
	if !strings.Contains(b.Name(), "claude") {
		t.Fatalf("backend name %q does not contain 'claude'", b.Name())
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
