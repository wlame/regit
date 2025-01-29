package reword

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/wlame/regit/internal/llm"
)

// Request is the context for generating message variants for one commit.
type Request struct {
	RepoName   string
	Readme     string
	Diff       string // unified diff with wide context (-U30)
	OldSubject string
	OldBody    string
	UserHint   string
	Variants   int // how many ranked variants to ask for
}

// SchemaJSON is the minimal portable schema for the variants response.
func SchemaJSON() []byte {
	return []byte(`{
  "type":"object","required":["variants"],
  "properties":{"variants":{"type":"array","items":{"type":"object",
    "required":["title"],
    "properties":{"title":{"type":"string"},"body":{"type":"string"}}}}}}`)
}

type variantsWire struct {
	Variants []struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	} `json:"variants"`
}

// BuildMessages renders the system + user prompt. The system prompt fixes the
// task and output contract; the user prompt carries all per-commit context.
func BuildMessages(req Request) (system, user string) {
	system = "You write high-quality git commit messages. Given a commit's diff and " +
		"context, propose " + fmt.Sprintf("%d", max1(req.Variants)) + " alternative messages, " +
		"ranked most-relevant first. Each has a concise one-line title (imperative mood) and an " +
		"optional body explaining the why. Return ONLY JSON matching the schema; no prose."
	var b strings.Builder
	if req.RepoName != "" {
		fmt.Fprintf(&b, "Repository: %s\n", req.RepoName)
	}
	if req.Readme != "" {
		fmt.Fprintf(&b, "Project overview (README):\n%s\n\n", req.Readme)
	}
	if req.OldSubject != "" {
		fmt.Fprintf(&b, "Existing title: %s\n", req.OldSubject)
	}
	if req.OldBody != "" {
		fmt.Fprintf(&b, "Existing body:\n%s\n", req.OldBody)
	}
	if req.UserHint != "" {
		fmt.Fprintf(&b, "Additional user guidance: %s\n", req.UserHint)
	}
	fmt.Fprintf(&b, "\nPropose %d ranked variants.\n\nDiff:\n%s\n", max1(req.Variants), req.Diff)
	return system, b.String()
}

func max1(n int) int {
	if n < 1 {
		return 1
	}
	return n
}

// ParseVariants extracts the JSON object from the model text and validates it:
// at least one variant, every title a non-blank single line of text.
func ParseVariants(raw string) (Result, error) {
	js := llm.ExtractJSON(raw)
	if js == "" {
		return Result{}, fmt.Errorf("no JSON object found in response")
	}
	var w variantsWire
	if err := json.Unmarshal([]byte(js), &w); err != nil {
		return Result{}, fmt.Errorf("invalid JSON: %w", err)
	}
	if len(w.Variants) == 0 {
		return Result{}, fmt.Errorf("no variants returned")
	}
	out := Result{Variants: make([]Variant, len(w.Variants))}
	for i, v := range w.Variants {
		if strings.TrimSpace(v.Title) == "" {
			return Result{}, fmt.Errorf("variant %d has a blank title", i)
		}
		// A title is a single line; collapse any stray newlines defensively.
		title := strings.TrimSpace(strings.SplitN(v.Title, "\n", 2)[0])
		out.Variants[i] = Variant{Title: title, Body: strings.TrimSpace(v.Body)}
	}
	return out, nil
}
