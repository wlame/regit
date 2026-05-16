package squash

import (
	"strings"
	"testing"

	"github.com/wlame/regit/internal/llm"
	"github.com/wlame/regit/internal/model"
)

func twoMemberChain() model.SquashChain {
	return model.SquashChain{Members: []model.Commit{
		{Hash: "a", Subject: "Add user model", Body: "introduce User"},
		{Hash: "b", Subject: "Add validation", Body: "validate fields"},
	}}
}

func TestBuildMessages_messagesOnlyOmitsDiffs(t *testing.T) {
	system, user := BuildMessages(twoMemberChain(), nil)
	if !strings.Contains(user, "Add user model") || !strings.Contains(user, "Add validation") {
		t.Error("user prompt should list both commit subjects")
	}
	if strings.Contains(user, "diff --git") || strings.Contains(user, "@@") {
		t.Error("messages-only prompt must not contain diffs")
	}
	if !strings.Contains(strings.ToLower(system), "single") {
		t.Error("system prompt should ask for a single combined message")
	}
}

func TestBuildMessages_withDiffsIncludesPatch(t *testing.T) {
	diffs := []string{"diff --git a/x b/x\n@@ -1 +1 @@\n-a\n+b\n", ""}
	_, user := BuildMessages(twoMemberChain(), diffs)
	if !strings.Contains(user, "diff --git a/x b/x") {
		t.Error("with-diffs prompt should embed the provided patch")
	}
}

func TestParseProposal_extractsJSONFromProse(t *testing.T) {
	raw := "Sure!\n{\n \"subject\": \"Add user model and validation\", \"body\": \"Combined.\"\n}\nDone."
	p, err := ParseProposal(raw)
	if err != nil {
		t.Fatalf("ParseProposal: %v", err)
	}
	if p.Subject != "Add user model and validation" || p.Body != "Combined." {
		t.Errorf("parsed %+v", p)
	}
}

func TestValidateProposalJSON_requiresSubject(t *testing.T) {
	if err := validateProposalJSON(`{"body":"x"}`); err == nil {
		t.Error("missing subject should be rejected")
	}
	if err := validateProposalJSON(`{"subject":"Add x"}`); err != nil {
		t.Errorf("subject-only should be valid, got %v", err)
	}
}

// TestExtractJSON_invalidInputReturnsEmpty verifies that extractJSON returns ""
// when the input contains no JSON object delimiters or they are in the wrong order.
func TestExtractJSON_invalidInputReturnsEmpty(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"empty string", ""},
		{"no braces", "just plain text"},
		{"only open brace", "{ no close"},
		{"close before open", "} { misorder "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractJSON(tc.input); got != "" {
				t.Errorf("extractJSON(%q) = %q, want empty string", tc.input, got)
			}
		})
	}
}

// TestValidateProposalJSON_noObjectErrors verifies that text with no JSON object
// is rejected before any unmarshal attempt.
func TestValidateProposalJSON_noObjectErrors(t *testing.T) {
	if err := validateProposalJSON("no json here"); err == nil {
		t.Error("input with no JSON object should return an error")
	}
}

// TestValidateProposalJSON_malformedJSONErrors verifies that a string with
// braces but invalid JSON content is rejected.
func TestValidateProposalJSON_malformedJSONErrors(t *testing.T) {
	if err := validateProposalJSON("{not valid json at all!!}"); err == nil {
		t.Error("malformed JSON should return an error")
	}
}

// TestParseProposal_noJSONObjectErrors verifies that ParseProposal returns an
// error when the raw string contains no JSON object.
func TestParseProposal_noJSONObjectErrors(t *testing.T) {
	if _, err := ParseProposal("no braces here"); err == nil {
		t.Error("ParseProposal with no JSON should return an error")
	}
}

// TestParseProposal_malformedJSONErrors verifies that ParseProposal returns an
// error when the extracted JSON cannot be unmarshalled.
func TestParseProposal_malformedJSONErrors(t *testing.T) {
	if _, err := ParseProposal("{invalid!}"); err == nil {
		t.Error("ParseProposal with invalid JSON should return an error")
	}
}

// TestBuildMessages_capsGiantMemberDiff verifies a huge chain-member diff is
// excerpted under the shared prompt budget instead of embedded whole.
func TestBuildMessages_capsGiantMemberDiff(t *testing.T) {
	giant := strings.Repeat("+added line of code\n", 30_000) // ~600 KiB
	_, user := BuildMessages(twoMemberChain(), []string{giant, giant})
	if len(user) > llm.MaxPromptDiffBytes+8192 {
		t.Errorf("squash prompt not capped: %d bytes", len(user))
	}
	if !strings.Contains(user, "diff truncated") {
		t.Errorf("capped prompt missing the excerpt marker")
	}
}
