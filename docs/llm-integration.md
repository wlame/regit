# LLM Integration Conventions

A reusable spec for adding **opt-in, gracefully-degrading LLM features** to a CLI
tool (with an optional TUI/GUI on top). It captures the configuration surface,
the provider abstraction, structured output, the trust model, and caching used
by `regit` so the *same interface* can be reproduced in other products.

Placeholders: `<tool>` = your binary name (e.g. `regit`), `<TOOL>` = its
upper-cased env prefix (e.g. `REGIT`), `<feature>` = an LLM-backed command
(e.g. `split`, `reword`).

---

## 0. Principles

1. **Opt-in, never required.** The tool works fully without an LLM. Enabling one
   is an enhancement. If no LLM is configured, an LLM-backed command fails fast
   with a clear message; non-LLM commands are unaffected.
2. **One small flag surface, two backends.** A single `--llm=<provider>/<model>`
   flag (plus an `--agent` escape hatch) covers local and hosted models, because
   they all speak the OpenAI-compatible `/v1/chat/completions` API.
3. **Secrets only from the environment.** API keys never appear in flags, config
   files, logs, or process listings.
4. **The model proposes; the tool disposes.** The LLM returns *data* (validated
   against a schema); your deterministic code performs the actual side effects.
   Never let an external model/agent mutate state directly.
5. **Headless-first.** Every LLM action is invocable non-interactively; the
   TUI/GUI calls the same engine, never a parallel path.

---

## 1. CLI flag surface

Standard flags for every LLM-backed command (long-form flags use `=`):

```
--llm=<provider>/<model>     # e.g. ollama/qwen2.5-coder, openai/gpt-4o,
                             #      openrouter/anthropic/claude-sonnet-4-6
--agent=<command>            # drive a local agent CLI instead of HTTP (e.g. claude)
--agent-args="...{schema}..."# replace the default agent argv; {schema} is the placeholder
--llm-base-url=<url>         # override the inferred endpoint (self-hosted/vLLM)
--prompt="<hint>"            # optional free-text user guidance to the model
--dry-run                    # compute + cache the proposal, perform no side effects
--yes                        # non-interactive confirm (CI/one-shot)
--no-cache | --clear-cache   # cache controls (see §6)
```

Per-feature flags layer on top (e.g. `--variants=N`, `--max-commits=N`,
`--context=readme,filestates`, `--test-cmd=...`). Keep the model/transport flags
identical across features.

### Provider → base-URL defaults
So the common case needs no `--llm-base-url`:

| provider     | default base URL                  |
|--------------|-----------------------------------|
| `ollama`     | `http://localhost:11434/v1`       |
| `lmstudio`   | `http://localhost:1234/v1`        |
| `openai`     | `https://api.openai.com/v1`       |
| `openrouter` | `https://openrouter.ai/api/v1`    |

`--llm-base-url` overrides the default (when set, `--llm` may carry just the
model; e.g. for vLLM use `--llm=openai/<model> --llm-base-url=http://host:8000/v1`).

### `provider/model` parsing
Split on the **first** `/`. Three-segment values keep the rest as the model:
`openrouter/anthropic/claude-x` → provider `openrouter`, model `anthropic/claude-x`.

### API key resolution (env only, in order)
1. `<TOOL>_LLM_API_KEY` — tool-wide override
2. provider-standard var: `OPENAI_API_KEY`, `OPENROUTER_API_KEY`, …
3. none — local providers (`ollama`, `lmstudio`) need no key

---

## 2. Configuration (YAML + env)

Precedence, lowest → highest: **built-in defaults → YAML file → environment**.
YAML lives at `$XDG_CONFIG_HOME/<tool>/config.yaml`. Defaults shown:

```yaml
llm:
  provider: ""            # off unless set here or via --llm
  model: ""
  base_url: ""            # override for self-hosted
  temperature: 0          # deterministic by default
  timeout_sec: 60

<feature>:                # one block per LLM-backed feature
  # …feature defaults (variants, context, cache, etc.)
  cache: true
```

- Env overrides use the `<TOOL>_` prefix, one var per setting:
  `<TOOL>_LLM_PROVIDER`, `<TOOL>_LLM_MODEL`, `<TOOL>_<FEATURE>_<SETTING>`.
- Setting `llm.provider`/`llm.model` (in YAML or env) lets the command run with
  no `--llm` flag. Without any of those, the command errors:
  `no LLM configured: pass --llm=provider/model or --agent=<cmd>`.

---

## 3. The backend abstraction

One interface, feature-agnostic. Each *feature* owns its prompt, JSON schema, and
parsing; the backend only knows "given a prompt + schema, return validated text."

```go
// Backend is the single interface every provider implements.
type Backend interface {
    // Generate runs one prompt and returns the raw model text (expected to
    // contain JSON matching schema). It retries a small N when validate(raw)
    // returns an error, strengthening the instruction each retry.
    Generate(ctx context.Context, system, user string, schema []byte,
        validate func(raw string) error) (string, error)
    Name() string // e.g. "http:gpt-4o" / "agent:claude" — for logs/cache keys
}
```

A feature adapter builds the prompt, calls `Generate`, then parses the validated
raw into its domain type:

```go
func Propose<Feature>(ctx, b Backend, req <Feature>Request) (<Feature>Result, error) {
    system, user := buildMessages(req)
    raw, err := b.Generate(ctx, system, user, schemaJSON(), func(raw string) error {
        _, perr := parse(raw)   // domain parse + validation
        return perr
    })
    if err != nil { return zero, err }
    return parse(raw)
}
```

Two implementations ship: an HTTP backend (§4) and an agent-CLI backend (§5).

---

## 4. HTTP backend (OpenAI-compatible)

Covers Ollama, LM Studio, vLLM, OpenAI, OpenRouter via `POST {base}/chat/completions`.

- Body: `{model, messages:[{role:"system"},{role:"user"}], temperature:0,
  response_format:{type:"json_schema", json_schema:{name:"response", schema:<raw>}}}`.
  Hosted providers honor `response_format`; local ones ignore it harmlessly — so
  always send it, and still validate the result yourself.
- Auth header `Authorization: Bearer <key>` only when a key is present (omit for
  local). Never log the key.
- Timeout from config; `context.Context` cancellation propagates to the request.
- **Retry contract:** loop up to `maxAttempts` (2 is plenty). Transport error →
  retry with the *unchanged* prompt. Validation failure → append
  `"Your previous answer was invalid: <err>. Return ONLY valid JSON matching the
  requested schema."` and retry. After the cap, return the wrapped last error.

---

## 5. Agent-CLI backend (local Claude Code / others)

Drives a local agent binary as a subprocess — uses the host's *existing* auth, so
no API key is needed.

- Default invocation (Claude Code), prompt fed on **stdin**:
  ```
  claude -p --output-format json --bare --json-schema '<inline schema JSON>'
  ```
  - `--json-schema` takes the schema as an **inline string** (not a file path);
    the result lands in the envelope's `structured_output` field.
  - `--bare` skips project `CLAUDE.md`/hooks/plugins/MCP for deterministic output.
- **`{schema}` placeholder:** the default argv contains a `{schema}` token; at call
  time substitute it with the inline schema JSON on a *copy* of the argv (never
  mutate the shared default slice). Custom agents are wired via
  `--agent-args="<their flags> {schema}"`; if a custom agent has no schema flag,
  omit `{schema}` and rely on the prompt instruction.
- **Envelope parsing:** prefer `structured_output`, then `result`, then treat the
  whole stdout as raw JSON; then run the same `parse`/validate as the HTTP path.
  Extract JSON robustly (first `{` … last `}`) to tolerate reasoning prose.
- Same retry contract as §4 (re-run the subprocess on validation failure).

### Read-only "scan" mode (optional, agent only)
Let the agent inspect a target itself (read-only) instead of you sending the
context. Verified Claude Code read-only, non-interactive flags:
```
claude -p --output-format json --bare \
  --permission-mode plan \
  --allowedTools "Read,Bash(git show *),Bash(git diff *),Bash(git log *),Bash(grep *),Bash(find *)" \
  --json-schema '<inline>'
```
- `--permission-mode plan` = read-only (no edits); the pre-approved `--allowedTools`
  list suppresses prompts so it stays non-interactive.
- Run the subprocess with `cmd.Dir = <repo/work dir>` so its reads target the
  right place. Gate this behind an explicit `--<feature>-scan`/`--agent-scan` flag;
  require `--agent`. (No current equivalent docs for the `codex` CLI — let users
  pass its own read-only sandbox flags via `--agent-args`.)
- **The agent still never writes:** your tool turns the returned data into an
  action and performs it through its own guarded path.

---

## 6. Structured output & validation

- Keep the JSON schema **minimal and portable** — advanced JSON-Schema features
  are unevenly supported across engines. A small object with a typed array is the
  sweet spot.
- Treat structured-output support as best-effort: always validate the returned
  JSON yourself (schema-shape parse **plus** domain invariants) and rely on the
  retry loop, rather than trusting the provider to constrain perfectly.
- Pattern: `ExtractJSON(raw)` (first `{`/last `}`) → `json.Unmarshal` into a wire
  struct → map to the domain type → check invariants → return a typed error on
  failure (the error text feeds the retry's strengthened prompt).

---

## 7. Trust & safety model

- **Default (any backend): the tool gathers all context** and sends it in the
  prompt; the model has **no** repo/filesystem access. Hardest to misuse,
  provider-agnostic.
- **Opt-in scan mode (agent only):** read-only sandbox as in §5.
- **Invariant in every mode:** the model/agent never performs the side effect.
  The tool converts the model's validated output into a deterministic operation
  and applies it through its own guarded code (with backup/undo where relevant).

---

## 8. Caching (optional but recommended)

For expensive proposals, cache by a stable key so `--dry-run` then a real run
(or a re-run) reuses the result without a second LLM call.

- Location: `<work-dir>/<tool>/cache/<feature>/<item-key>.json`
  (e.g. under `.git/` for a git tool — repo-scoped, not in the working tree).
- Key: the item id + a **fingerprint** of every input that affects the output
  (input hash, model name, the feature's params, the user hint). Any change →
  cache miss. Store a schema/format `version` and miss on mismatch.
- `--dry-run` writes the cache; the real run reads it; `--no-cache`/`--clear-cache`
  control it. Keep the cache package clock-free (pass timestamps in) for testable,
  deterministic behavior.

---

## 9. Headless + UI parity

- Build the headless command first; the TUI/GUI calls the same engine.
- Headless typically asks the model for **one** result and confirms `Y/n`
  (auto-accept with `--yes`); the UI can ask for several ranked variants.
- For multi-item runs, hide latency with a **bounded, sequential prefetch**: a
  worker generates one item ahead of the user's review (channel/goroutine in the
  CLI; async `tea.Cmd`/equivalent in the TUI). Sequential (not all-parallel) keeps
  token cost proportional to how far the user actually goes; make it cancellable.

---

## 10. Implementation checklist

- [ ] `--llm`, `--agent`, `--agent-args`, `--llm-base-url`, `--prompt`, `--dry-run`,
      `--yes`, `--no-cache`/`--clear-cache` flags on each LLM command.
- [ ] provider→base-URL map; `provider/model` first-slash split.
- [ ] API keys resolved from env only (`<TOOL>_LLM_API_KEY` → provider-standard → none).
- [ ] `llm:` config block + `<TOOL>_LLM_*` env overrides; clear "no LLM configured" error.
- [ ] `Backend` interface with `Generate` + `Name`; HTTP and agent-CLI impls; retry contract.
- [ ] minimal JSON schema + `ExtractJSON` + parse/validate + retry on invalid.
- [ ] default mode gathers context (no model repo access); optional read-only scan mode; model never mutates state.
- [ ] fingerprinted, versioned, clock-free cache (optional).
- [ ] one engine behind both headless and UI; bounded sequential prefetch for multi-item runs.
- [ ] LLM features are opt-in and degrade gracefully; tests are deterministic (httptest + stub scripts, no real network/LLM/clock).
