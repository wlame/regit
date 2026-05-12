// internal/llm/agent.go
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// secretEnvSubstrings name environment variables the agent subprocess must never
// inherit: anything holding an API key, token, secret, or password, plus common
// cloud-credential prefixes. Matched case-insensitively against the variable name.
var secretEnvSubstrings = []string{"API_KEY", "TOKEN", "SECRET", "PASSWORD", "AWS_", "GH_", "GITHUB_", "OPENAI", "OPENROUTER", "ANTHROPIC"}

// scrubbedAgentEnv returns the process environment with every variable whose name
// matches secretEnvSubstrings removed. The agent CLI uses its own host session for
// auth, so dropping these cannot break it — it only prevents a prompt-injected scan
// from reading a credential out of the inherited environment.
func scrubbedAgentEnv() []string {
	env := os.Environ()
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			name = kv[:i]
		}
		upper := strings.ToUpper(name)
		secret := false
		for _, s := range secretEnvSubstrings {
			if strings.Contains(upper, s) {
				secret = true
				break
			}
		}
		if !secret {
			out = append(out, kv)
		}
	}
	return out
}

// SchemaPlaceholder is the token replaced at runtime with the inline JSON
// schema string when constructing the agent subprocess arguments. Callers who
// build custom args slices may include this token where --json-schema's value
// should appear; Propose substitutes it with the actual schema JSON.
const SchemaPlaceholder = "{schema}"

// AgentBackend drives a local agent CLI (default: `claude -p --output-format
// json --bare --json-schema <schema>`) as a subprocess. The full prompt is fed
// on stdin; the agent's JSON envelope is parsed and its structured_output (or
// message text) validated.
//
// When dir is non-empty, the subprocess is run with that working directory.
// This is required for agent-scan mode so that the agent's Read/Grep/Glob
// tool calls resolve against the correct repository, especially when --repo
// points elsewhere.
type AgentBackend struct {
	command string
	args    []string
	dir     string        // working directory for the subprocess; "" inherits the process cwd
	timeout time.Duration // per-call ceiling; 0 falls back to defaultAgentTimeout
}

// defaultAgentTimeout bounds a single agent subprocess call. Unlike the HTTP
// backend (which has an http.Client.Timeout), a hung agent CLI would otherwise
// block regit forever; this deadline guarantees the call returns.
const defaultAgentTimeout = 5 * time.Minute

// ScanDefaultArgs are the read-only headless args used when --agent-scan is
// active and the user has not supplied --agent-args. The agent gets ONLY the
// file-browsing tools (Read/Grep/Glob) — no Bash: even "read-only" git verbs
// accept --output=<path> (arbitrary file write) and find allows -exec, so any
// exec surface is an injection risk. The commit diff is supplied in the prompt
// by the caller; --permission-mode plan stays as defense-in-depth. The --bare
// flag skips CLAUDE.md / hooks / plugins / MCP for deterministic output.
// {schema} is substituted with the actual JSON schema at call time.
//
// A research spike against Claude Code v2.1.x found that --allowedTools
// path-scoping is NOT a security boundary for the file tools: a prompt
// injected via the scanned diff can still steer Read/Grep/Glob at paths
// outside the intended scope. --disallowedTools deny rules, in contrast, ARE
// a real flags-level hard block, and they DO resolve symlinks and `..`
// (verified empirically). ScanDefaultArgs therefore also denies reads of a
// short list of well-known secret stores (SSH/AWS/GPG keys, /etc, .env,
// id_rsa*, etc.) as defense-in-depth against a prompt-injected diff steering
// the agent to exfiltrate them via its own Read/Grep/Glob calls.
//
// Be honest about what this is NOT: it is not a perimeter or a sandbox. Any
// path not on the deny list — including secrets stored under a different
// name or location than the ones listed here — remains readable if it is
// reachable via Read/Grep/Glob from the scan's working directory. Agent-scan
// mode is a documented TRUST BOUNDARY, not a hard confinement; true
// confinement (blocking arbitrary reads outside the repo) requires an OS-level
// sandbox (e.g. a container, chroot, or Landlock/seccomp profile), which
// regit does not currently provide.
//
// ScanDefaultArgs should be treated as read-only; Generate copies the args
// before substituting {schema}, so the original slice is never modified.
var ScanDefaultArgs = buildScanArgs()

// scanFileTools are the file-browsing tools agent-scan permits. Every deny path is
// expanded to a rule for EACH of them: a Read(p) deny alone still lets Grep(p) read
// the same file's content and Glob(p) enumerate it, so all three must be denied.
var scanFileTools = []string{"Read", "Grep", "Glob"}

// scanDenyPaths are the filesystem locations agent-scan denies across every file
// tool. Deny rules resolve symlinks and `..`, so these ARE a real hard block
// (unlike --allowedTools path-scoping). Defense-in-depth only — a path NOT listed
// here stays readable, so scan mode remains a documented trust boundary, not a
// sandbox. /proc is included so the process environment (which could carry API
// keys) cannot be read via /proc/self/environ; the agent subprocess additionally
// runs with a scrubbed environment (see scrubbedAgentEnv).
var scanDenyPaths = []string{
	"~/.ssh/**", "~/.aws/**", "~/.gnupg/**", "~/.config/**", "~/.kube/**",
	"~/.docker/**", "//etc/**", "//proc/**",
	"~/.netrc", "~/.git-credentials", "~/.npmrc", "~/.pypirc",
	"**/.env", "**/.env.*", "**/*.pem",
	"**/id_rsa*", "**/id_ed25519*", "**/id_ecdsa*", "**/id_dsa*",
}

// scanDenyRules expands scanDenyPaths into a comma-joined "<Tool>(<path>)" deny
// list covering every scanFileTool for every path.
func scanDenyRules() string {
	rules := make([]string, 0, len(scanDenyPaths)*len(scanFileTools))
	for _, p := range scanDenyPaths {
		for _, tool := range scanFileTools {
			rules = append(rules, tool+"("+p+")")
		}
	}
	return strings.Join(rules, ",")
}

// ScanProtectionArgs are the security flags that MUST be present for every
// agent-scan invocation: plan mode, the restricted allow-list, and the full
// deny-list. They are kept separate from the invocation flags so a user's extra
// --agent-args can be appended WITHOUT being able to drop them (see BuildBackend).
func ScanProtectionArgs() []string {
	return []string{
		"--permission-mode", "plan",
		"--allowedTools", strings.Join(scanFileTools, ","),
		"--disallowedTools", scanDenyRules(),
	}
}

// buildScanArgs assembles the full default agent-scan argv: the invocation flags,
// the mandatory protections, and the schema placeholder.
func buildScanArgs() []string {
	args := []string{"-p", "--output-format", "json", "--bare"}
	args = append(args, ScanProtectionArgs()...)
	return append(args, "--json-schema", SchemaPlaceholder)
}

// NewAgentBackend builds an agent backend with the process's inherited working
// directory. When args is nil it defaults to the Claude Code headless invocation
// flags: -p (print mode), --output-format json (machine-readable envelope),
// --bare (skip CLAUDE.md/hooks/plugins/MCP for deterministic output), and
// --json-schema with the SchemaPlaceholder token (substituted with the actual
// inline schema JSON at call time in Generate).
func NewAgentBackend(command string, args []string) *AgentBackend {
	if args == nil {
		args = []string{"-p", "--output-format", "json", "--bare", "--json-schema", SchemaPlaceholder}
	}
	return &AgentBackend{command: command, args: args}
}

// NewAgentBackendInDir builds an agent backend that runs its subprocess with
// cwd set to dir. This is required for agent-scan mode so that the agent's
// Read/Grep/Glob tool calls target the correct repository when --repo is set.
// When dir is empty it behaves identically to NewAgentBackend.
func NewAgentBackendInDir(command string, args []string, dir string) *AgentBackend {
	b := NewAgentBackend(command, args)
	b.dir = dir
	return b
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

	// Apply a default deadline when the caller's context has none, so a hung agent
	// CLI cannot block regit forever. Callers that pass their own deadline keep it.
	timeout := b.timeout
	if timeout <= 0 {
		timeout = defaultAgentTimeout
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
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
		if b.dir != "" {
			// Set the subprocess working directory so the agent's Read/Grep/Glob
			// tool calls target the correct repository (used in agent-scan mode).
			cmd.Dir = b.dir
		}
		// The agent authenticates with its own host session (not our env), and
		// regit supplies the diff in the prompt — so the child never needs regit's
		// secrets. Strip them so a prompt-injected scan cannot exfiltrate an API key
		// or cloud credential from the inherited environment.
		cmd.Env = scrubbedAgentEnv()
		cmd.Stdin = bytes.NewReader([]byte(prompt))
		// After the context is cancelled (deadline hit), give the child a short
		// grace period and then force-kill it, so a wedged agent that ignores its
		// stdin closing is still reaped instead of leaking.
		cmd.WaitDelay = 10 * time.Second
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
