// internal/llm/agent_test.go
package llm

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

// writeArgRecordingStub writes a stub script that records all CLI args (one per
// line) to the file at $ARGFILE, then prints body on stdout. The test passes
// the ARGFILE path via exec.Cmd.Env so each test gets its own file.
func writeArgRecordingStub(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("stub script is POSIX sh")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "fakeagent")
	// printf '%s\n' "$@" writes each positional arg on its own line, which lets
	// us grep for exact strings without worrying about word splitting.
	script := "#!/bin/sh\ncat >/dev/null\nprintf '%s\\n' \"$@\" >\"$ARGFILE\"\nprintf '%s' '" + body + "'\n"
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
	prop, err := ProposeSplit(context.Background(), b, sampleReq(), SchemaJSON())
	if err != nil {
		t.Fatal(err)
	}
	if len(prop.Groups) != 1 {
		t.Fatalf("want 1 group, got %d", len(prop.Groups))
	}
}

func TestAgentBackend_parsesResultField(t *testing.T) {
	// Envelope with "result" string field containing the JSON.
	body := `{"result":"{\"should_split\":true,\"groups\":[{\"order\":1,\"hunk_ids\":[\"H1\",\"H2\"],\"subject\":\"all\"}]}"}`
	stub := writeStub(t, body)
	b := NewAgentBackend(stub, nil)
	prop, err := ProposeSplit(context.Background(), b, sampleReq(), SchemaJSON())
	if err != nil {
		t.Fatal(err)
	}
	if len(prop.Groups) != 1 {
		t.Fatalf("want 1 group, got %d", len(prop.Groups))
	}
}

func TestAgentBackend_parsesBareJSON(t *testing.T) {
	// Raw JSON object without envelope (agent CLI emits bare JSON).
	body := `{"should_split":true,"groups":[{"order":1,"hunk_ids":["H1","H2"],"subject":"all"}]}`
	stub := writeStub(t, body)
	b := NewAgentBackend(stub, nil)
	prop, err := ProposeSplit(context.Background(), b, sampleReq(), SchemaJSON())
	if err != nil {
		t.Fatal(err)
	}
	if len(prop.Groups) != 1 {
		t.Fatalf("want 1 group, got %d", len(prop.Groups))
	}
}

func TestAgentBackend_schemaPlaceholderSubstituted(t *testing.T) {
	// Verify that SchemaPlaceholder in the args slice is replaced with the
	// inline schema JSON before the subprocess is launched. The stub records
	// all CLI args to $ARGFILE; we assert the schema content is present and
	// the raw placeholder string is absent.
	body := `{"structured_output":{"should_split":true,"groups":[{"order":1,"hunk_ids":["H1","H2"],"subject":"all"}]}}`
	stub := writeArgRecordingStub(t, body)

	argFile := filepath.Join(t.TempDir(), "args.txt")

	args := []string{"--json-schema", SchemaPlaceholder}
	b := NewAgentBackend(stub, args)

	// Inject ARGFILE into the subprocess environment so the stub knows where
	// to write the recorded args.
	origCmd := b.command
	_ = origCmd // used below via b.command unchanged
	b2 := &AgentBackend{command: b.command, args: b.args}

	// We need to set ARGFILE before the exec; do it via os.Setenv scoped to
	// this test (safe because tests in this package are not run in parallel).
	t.Setenv("ARGFILE", argFile)

	prop, err := ProposeSplit(context.Background(), b2, sampleReq(), SchemaJSON())
	if err != nil {
		t.Fatal(err)
	}
	if len(prop.Groups) != 1 {
		t.Fatalf("want 1 group, got %d", len(prop.Groups))
	}

	recorded, err := os.ReadFile(argFile)
	if err != nil {
		t.Fatalf("read argfile: %v", err)
	}
	recordedStr := string(recorded)

	// The inline schema JSON must contain the "should_split" field name.
	if !strings.Contains(recordedStr, "should_split") {
		t.Errorf("recorded args do not contain schema content; got:\n%s", recordedStr)
	}
	// The raw placeholder must not appear in the recorded args.
	if strings.Contains(recordedStr, SchemaPlaceholder) {
		t.Errorf("recorded args still contain the literal placeholder %q; got:\n%s", SchemaPlaceholder, recordedStr)
	}
}

// TestAgentBackend_validationRetryLoop verifies that AgentBackend.Generate retries
// when the validate function rejects a response. A stateful POSIX sh stub uses a
// sentinel file in a temp dir: on the first run (no sentinel) it prints garbage
// JSON and creates the file; on the second run (sentinel present) it prints a
// valid envelope. The test asserts Generate returns the valid raw after exactly 2
// subprocess invocations.
func TestAgentBackend_validationRetryLoop(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub script is POSIX sh")
	}

	// sentinelDir is exported to the stub via SENTINEL_DIR env var.
	sentinelDir := t.TempDir()
	stubDir := t.TempDir()
	stubPath := filepath.Join(stubDir, "statefulagent")

	// Valid structured-output envelope the stub returns on its second call.
	validEnvelope := `{"structured_output":{"should_split":true,"groups":[{"order":1,"hunk_ids":["H1","H2"],"subject":"all"}]}}`

	// The stub: if $SENTINEL_DIR/seen exists → second call, print valid JSON;
	// else create the sentinel and print garbage.
	script := "#!/bin/sh\ncat >/dev/null\n" +
		"if [ -f \"$SENTINEL_DIR/seen\" ]; then\n" +
		"  printf '%s' '" + validEnvelope + "'\n" +
		"else\n" +
		"  touch \"$SENTINEL_DIR/seen\"\n" +
		"  printf 'not-json-garbage'\n" +
		"fi\n"
	if err := os.WriteFile(stubPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("SENTINEL_DIR", sentinelDir)

	b := NewAgentBackend(stubPath, nil)

	// validate rejects anything that is not valid JSON / not parseable as a plan.
	calls := 0
	validate := func(raw string) error {
		calls++
		_, err := ParseAndValidate(raw, sampleReq().Hunks)
		return err
	}

	schema := SchemaJSON()
	req := sampleReq()
	system, user := BuildMessages(req)

	raw, err := b.Generate(context.Background(), system, user, schema, validate)
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	// The returned raw must be the unwrapped structured_output from the valid envelope.
	if !strings.Contains(raw, "should_split") {
		t.Errorf("expected valid JSON in raw, got: %s", raw)
	}

	// validate must have been called exactly twice: once for garbage, once for valid.
	if calls != 2 {
		t.Errorf("expected validate called 2 times, got %d", calls)
	}
}

func TestAgentBackend_defaultArgsContainBareAndSchema(t *testing.T) {
	// Verify that NewAgentBackend(cmd, nil) produces the expected default flags
	// by running a stub that records its args and asserting --bare and
	// --json-schema are present in the recorded output.
	body := `{"structured_output":{"should_split":true,"groups":[{"order":1,"hunk_ids":["H1","H2"],"subject":"all"}]}}`
	stub := writeArgRecordingStub(t, body)

	argFile := filepath.Join(t.TempDir(), "args.txt")
	t.Setenv("ARGFILE", argFile)

	b := NewAgentBackend(stub, nil) // nil → default args
	_, err := ProposeSplit(context.Background(), b, sampleReq(), SchemaJSON())
	if err != nil {
		t.Fatal(err)
	}

	recorded, err := os.ReadFile(argFile)
	if err != nil {
		t.Fatalf("read argfile: %v", err)
	}
	recordedStr := string(recorded)

	for _, want := range []string{"--bare", "--json-schema"} {
		if !strings.Contains(recordedStr, want) {
			t.Errorf("default args missing %q; recorded:\n%s", want, recordedStr)
		}
	}
	// The placeholder must have been replaced — schema JSON must be present.
	if !strings.Contains(recordedStr, "should_split") {
		t.Errorf("schema placeholder was not substituted; recorded:\n%s", recordedStr)
	}
}
