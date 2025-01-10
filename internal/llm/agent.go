// internal/llm/agent.go
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"

	"github.com/wlame/regit/internal/model"
)

// SchemaPlaceholder is the token replaced at runtime with the inline JSON
// schema string when constructing the agent subprocess arguments. Callers who
// build custom args slices may include this token where --json-schema's value
// should appear; Propose substitutes it with the actual schema JSON.
const SchemaPlaceholder = "{schema}"

// AgentBackend drives a local agent CLI (default: `claude -p --output-format
// json --bare --json-schema <schema>`) as a subprocess. The full prompt is fed
// on stdin; the agent's JSON envelope is parsed and its structured_output (or
// message text) validated.
type AgentBackend struct {
	command string
	args    []string
}

// NewAgentBackend builds an agent backend. When args is nil it defaults to the
// Claude Code headless invocation flags: -p (print mode), --output-format json
// (machine-readable envelope), --bare (skip CLAUDE.md/hooks/plugins/MCP for
// deterministic output), and --json-schema with the SchemaPlaceholder token
// (substituted with the actual inline schema JSON at call time in Propose).
func NewAgentBackend(command string, args []string) *AgentBackend {
	if args == nil {
		args = []string{"-p", "--output-format", "json", "--bare", "--json-schema", SchemaPlaceholder}
	}
	return &AgentBackend{command: command, args: args}
}

// Name returns a human-readable identifier for logs and the TUI status line.
// The command path is included so operators can see which agent binary is in use.
func (b *AgentBackend) Name() string { return "agent:" + b.command }

// Propose sends the split request to the agent subprocess and returns an
// ordered grouping of hunks. The full prompt (system + user context) is
// written to the subprocess's stdin; stdout must be either a JSON envelope
// produced by `claude --output-format json` or a bare JSON object.
//
// The subprocess is run under the caller's context, so a deadline or
// cancellation propagates as a process kill via exec.CommandContext.
//
// Any element of b.args equal to SchemaPlaceholder ("{schema}") is replaced
// with the inline schema JSON (the value of schema) before the subprocess is
// launched, forwarding it as the argument to --json-schema per claude's API.
// If no element matches the placeholder, the schema is not forwarded (back-
// compat path for custom agents that handle schema in another way).
func (b *AgentBackend) Propose(ctx context.Context, req SplitRequest, schema []byte) (model.SplitProposal, error) {
	system, user := BuildMessages(req)
	// Combine system + user text into a single stdin payload. Agent CLIs such
	// as `claude -p` receive a single prompt string rather than a structured
	// message array; appending the return-JSON instruction mirrors the same
	// constraint communicated to HTTP backends via the ResponseFormat field.
	prompt := system + "\n\n" + user + "\n\nReturn ONLY the JSON object."

	// Build the runtime args by substituting any SchemaPlaceholder element with
	// the inline schema JSON. We copy b.args so the backend is reusable across
	// concurrent Propose calls without data races.
	runtimeArgs := make([]string, len(b.args))
	copy(runtimeArgs, b.args)
	for i, arg := range runtimeArgs {
		if arg == SchemaPlaceholder {
			runtimeArgs[i] = string(schema)
		}
	}

	cmd := exec.CommandContext(ctx, b.command, runtimeArgs...)
	cmd.Stdin = bytes.NewReader([]byte(prompt))
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	if err := cmd.Run(); err != nil {
		return model.SplitProposal{}, fmt.Errorf("agent %s: %w: %s", b.command, err, stderr.String())
	}

	// unwrapAgentJSON extracts the inner plan JSON from the envelope before
	// handing it to ParseAndValidate, which expects a bare JSON object.
	return ParseAndValidate(unwrapAgentJSON(stdout.String()), req.Hunks)
}

// unwrapAgentJSON pulls the plan JSON out of an agent envelope. It prefers a
// "structured_output" field (Claude Code --output-format json with a JSON schema),
// then a plain "result" string field, and finally falls back to the raw output
// for agent CLIs that emit the JSON object directly without an envelope.
func unwrapAgentJSON(out string) string {
	var env struct {
		StructuredOutput json.RawMessage `json:"structured_output"`
		Result           string          `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &env); err == nil {
		if len(env.StructuredOutput) > 0 && string(env.StructuredOutput) != "null" {
			return string(env.StructuredOutput)
		}
		if env.Result != "" {
			return env.Result
		}
	}
	return out
}
