// internal/llm/prompt.go
package llm

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/wlame/regit/internal/model"
)

// SchemaJSON returns the minimal, portable JSON schema for the SplitPlan.
// This schema is passed to backends that support structured output (e.g.
// OpenAI's response_format / json_schema), ensuring the model returns
// well-formed JSON every time rather than wrapping it in prose.
func SchemaJSON() []byte {
	return []byte(`{
  "type":"object","required":["should_split","groups"],
  "properties":{
    "should_split":{"type":"boolean"},
    "reason":{"type":"string"},
    "groups":{"type":"array","items":{"type":"object",
      "required":["order","hunk_ids","subject"],
      "properties":{
        "order":{"type":"integer"},
        "hunk_ids":{"type":"array","items":{"type":"string"}},
        "subject":{"type":"string"},"body":{"type":"string"},
        "intent":{"type":"string"},"confidence":{"type":"number"}}}}}}`)
}

// planWire mirrors the JSON schema for unmarshalling the model's response.
// It is kept private; callers only see the canonical model.SplitProposal.
type planWire struct {
	ShouldSplit bool   `json:"should_split"`
	Reason      string `json:"reason"`
	Groups      []struct {
		Order      int      `json:"order"`
		HunkIDs    []string `json:"hunk_ids"`
		Subject    string   `json:"subject"`
		Body       string   `json:"body"`
		Intent     string   `json:"intent"`
		Confidence float64  `json:"confidence"`
	} `json:"groups"`
}

// BuildMessages renders the system and user prompt for the OpenAI chat API.
// The system prompt establishes the task; the user prompt supplies all
// context (commit message, hunks, README, hints) specific to this request.
func BuildMessages(req SplitRequest) (system, user string) {
	system = "You split one git commit into smaller, meaningful commits. " +
		"You are given the commit message and its diff as numbered hunks (H1, H2, …). " +
		"Group the hunk IDs into 2 or more ordered commits so each commit is a single " +
		"coherent change that builds on the previous ones. Return ONLY JSON matching the schema. " +
		"Never output code; only hunk IDs and commit subjects. Reason step by step before the JSON."
	var b strings.Builder
	fmt.Fprintf(&b, "Original commit message:\n%s\n\n", req.CommitMessage)
	if req.UserHint != "" {
		fmt.Fprintf(&b, "Additional user guidance: %s\n\n", req.UserHint)
	}
	fmt.Fprintf(&b, "Split into at most %d commits.\n\n", req.MaxCommits)
	if req.Readme != "" {
		fmt.Fprintf(&b, "Repository overview:\n%s\n\n", req.Readme)
	}
	b.WriteString("Hunks:\n")
	for _, h := range req.Hunks {
		fmt.Fprintf(&b, "%s %s (+%d/-%d)\n%s\n", h.ID, h.File, h.Additions, h.Deletions, h.Body)
	}
	return system, b.String()
}

// ParseAndValidate extracts the JSON object from a model response text,
// unmarshals it into a SplitProposal, and enforces the coverage invariant
// (every hunk assigned exactly once) when should_split is true.
//
// Models often prefix JSON with reasoning prose; we find the first '{' and
// last '}' to tolerate that without a separate "extract-then-parse" step.
func ParseAndValidate(raw string, hunks []model.Hunk) (model.SplitProposal, error) {
	js := extractJSON(raw)
	if js == "" {
		return model.SplitProposal{}, fmt.Errorf("no JSON object found in response")
	}
	var w planWire
	if err := json.Unmarshal([]byte(js), &w); err != nil {
		return model.SplitProposal{}, fmt.Errorf("invalid JSON: %w", err)
	}
	groups := make([]model.HunkGroup, len(w.Groups))
	for i, g := range w.Groups {
		groups[i] = model.HunkGroup{
			Order: g.Order, HunkIDs: g.HunkIDs, Subject: g.Subject,
			Body: g.Body, Intent: g.Intent, Confidence: g.Confidence,
		}
	}
	prop := model.SplitProposal{Hunks: hunks, Groups: groups, ShouldSplit: w.ShouldSplit, Reason: w.Reason}
	if w.ShouldSplit {
		// Coverage validation is the pure first line of defense: every hunk must
		// appear in exactly one group before any git object is created.
		if err := model.ValidateCoverage(hunks, groups); err != nil {
			return model.SplitProposal{}, err
		}
	}
	return prop, nil
}

// extractJSON returns the substring from the first '{' to the last '}'.
// Returns "" when no balanced object is found.
func extractJSON(s string) string {
	i := strings.Index(s, "{")
	j := strings.LastIndex(s, "}")
	if i < 0 || j < 0 || j < i {
		return ""
	}
	return s[i : j+1]
}
