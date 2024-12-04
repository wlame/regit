// internal/llm/agent_test.go
package llm

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func writeStub(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("stub script is POSIX sh")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "fakeagent")
	script := "#!/bin/sh\ncat >/dev/null\nprintf '%s' '" + body + "'\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAgentBackend_parsesStructuredOutput(t *testing.T) {
	// Claude Code --output-format json wraps the result; we read structured_output.
	body := `{"structured_output":{"should_split":true,"groups":[{"order":1,"hunk_ids":["H1","H2"],"subject":"all"}]}}`
	stub := writeStub(t, body)
	b := NewAgentBackend(stub, nil)
	prop, err := b.Propose(context.Background(), sampleReq(), SchemaJSON())
	if err != nil {
		t.Fatal(err)
	}
	if len(prop.Groups) != 1 {
		t.Fatalf("want 1 group, got %d", len(prop.Groups))
	}
}
