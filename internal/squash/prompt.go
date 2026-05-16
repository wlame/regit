package squash

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/wlame/regit/internal/llm"
	"github.com/wlame/regit/internal/model"
)

// systemPrompt instructs the model to merge several commit messages into one,
// following regit's commit-message convention.
const systemPrompt = "You merge several consecutive git commits into a SINGLE commit. " +
	"Write one combined message that summarizes all of them. The subject must be a " +
	"single imperative sentence, start with a capital letter, end with a full stop, " +
	"and use NO conventional-commit prefix (no 'feat:'/'fix:') and NO trailer. The " +
	"body (optional) may add a short paragraph of context. Reply with JSON only: " +
	`{"subject": "...", "body": "..."}.`

// BuildMessages renders the system and user prompts for summarizing one chain.
// diffs[i] (when non-empty) is the patch for ch.Members[i]; a nil or all-empty
// diffs slice yields a messages-only prompt.
func BuildMessages(ch model.SquashChain, diffs []string) (system, user string) {
	var b strings.Builder
	b.WriteString("Combine these commits (oldest first) into one:\n\n")
	// Member diffs share a bounded byte budget so one huge chain member cannot
	// blow up the request size (see llm.MaxPromptDiffBytes).
	share := llm.DiffShareBytes(len(diffs))
	for i, m := range ch.Members {
		fmt.Fprintf(&b, "Commit %d:\n  subject: %s\n", i+1, m.Subject)
		if m.Body != "" {
			fmt.Fprintf(&b, "  body: %s\n", strings.ReplaceAll(m.Body, "\n", "\n    "))
		}
		if i < len(diffs) && strings.TrimSpace(diffs[i]) != "" {
			fmt.Fprintf(&b, "  diff:\n%s\n", llm.TruncateForPrompt(diffs[i], share))
		}
		b.WriteString("\n")
	}
	return systemPrompt, b.String()
}

// SchemaJSON is the JSON schema hosted providers honor for structured output.
func SchemaJSON() []byte {
	return []byte(`{
  "type": "object",
  "properties": {
    "subject": {"type": "string"},
    "body": {"type": "string"}
  },
  "required": ["subject"]
}`)
}

// proposalJSON is the wire shape the model returns.
type proposalJSON struct {
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

// extractJSON returns the substring from the first '{' to the last '}', so
// reasoning prose around the JSON is tolerated.
func extractJSON(raw string) string {
	start := strings.IndexByte(raw, '{')
	end := strings.LastIndexByte(raw, '}')
	if start < 0 || end < 0 || end < start {
		return ""
	}
	return raw[start : end+1]
}

// validateProposalJSON is the validate callback passed to Backend.Generate: it
// fails (triggering a retry) when the response is not parseable or lacks a subject.
func validateProposalJSON(raw string) error {
	js := extractJSON(raw)
	if js == "" {
		return fmt.Errorf("no JSON object found in response")
	}
	var p proposalJSON
	if err := json.Unmarshal([]byte(js), &p); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	if strings.TrimSpace(p.Subject) == "" {
		return fmt.Errorf("missing required field: subject")
	}
	return nil
}

// ParseProposal parses a validated model response into a Proposal.
func ParseProposal(raw string) (Proposal, error) {
	js := extractJSON(raw)
	if js == "" {
		return Proposal{}, fmt.Errorf("no JSON object found in response")
	}
	var p proposalJSON
	if err := json.Unmarshal([]byte(js), &p); err != nil {
		return Proposal{}, err
	}
	return Proposal{Subject: strings.TrimSpace(p.Subject), Body: strings.TrimSpace(p.Body)}, nil
}
