package squash

import (
	"strings"
	"testing"

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
