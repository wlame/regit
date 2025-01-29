// internal/llm/agent.go
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
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
// (substituted with the actual inline schema JSON at call time in Generate).
func NewAgentBackend(command string, args []string) *AgentBackend {
	if args == nil {
		args = []string{"-p", "--output-format", "json", "--bare", "--json-schema", SchemaPlaceholder}
	}
	return &AgentBackend{command: command, args: args}
}

// Name returns a human-readable identifier for logs and the TUI status line.
// The command path is included so operators can see which agent binary is in use.
func (b *AgentBackend) Name() string { return "agent:" + b.command }

// Generate runs the given prompt against the agent subprocess and returns the
// raw model text. The full prompt (system + user + JSON instruction) is fed to
// stdin; stdout is unwrapped from the agent envelope before validation.
//
// Any element of b.args equal to SchemaPlaceholder ("{schema}") is replaced
// with the inline schema JSON before the subprocess is launched. The args slice
// is copied so the backend is safe for concurrent use.
//
// Unlike the HTTP backend, the agent path previously had no retry loop. This
// implementation adds one (up to maxAttempts) using validate, strengthening
// the user prompt on each validation failure.
func (b *AgentBackend) Generate(ctx context.Context, system, user string, schema []byte, validate func(string) error) (string, error) {
	// Build the runtime args by substituting any SchemaPlaceholder element with
	// the inline schema JSON. We copy b.args so the backend is reusable across
	// concurrent Generate calls without data races.
	runtimeArgs := make([]string, len(b.args))
	copy(runtimeArgs, b.args)
	for i, arg := range runtimeArgs {
		if arg == SchemaPlaceholder {
			runtimeArgs[i] = string(schema)
		}
	}

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		// Combine system + user text into a single stdin payload. Agent CLIs
		// such as `claude -p` receive a single prompt string rather than a
		// structured message array; appending the return-JSON instruction
		// mirrors the same constraint communicated to HTTP backends via
		// the ResponseFormat field.
		prompt := system + "\n\n" + user + "\n\nReturn ONLY the JSON object."
		cmd := exec.CommandContext(ctx, b.command, runtimeArgs...)
		cmd.Stdin = bytes.NewReader([]byte(prompt))
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			// Transport/subprocess error: retry with the unchanged prompt, matching
			// HTTPBackend.Generate's documented contract (transport error → retry,
			// do NOT append the strengthen text).
			lastErr = fmt.Errorf("agent %s: %w: %s", b.command, err, stderr.String())
			continue
		}
		// unwrapAgentJSON extracts the inner JSON from the envelope before
		// handing it to the caller's validate function.
		raw := unwrapAgentJSON(stdout.String())
		if verr := validate(raw); verr == nil {
			return raw, nil
		} else {
			lastErr = verr
			user += "\n\nYour previous answer was invalid: " + verr.Error() +
				". Return ONLY valid JSON matching the requested schema."
		}
	}
	return "", fmt.Errorf("agent generation failed after %d attempts: %w", maxAttempts, lastErr)
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
