// internal/llm/http.go
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// HTTPBackend talks to any OpenAI-compatible /v1/chat/completions endpoint
// (Ollama, LM Studio, vLLM, OpenAI, OpenRouter). It retries once when the model
// returns output that fails schema/coverage validation, strengthening the prompt
// on retry so the model understands what it did wrong.
type HTTPBackend struct {
	baseURL string
	model   string
	apiKey  string
	client  *http.Client
}

// NewHTTPBackend builds a backend. baseURL should be the provider root including
// the /v1 suffix (e.g. "http://localhost:11434/v1" for Ollama). An empty apiKey
// omits the Authorization header, which is correct for local providers.
func NewHTTPBackend(baseURL, modelName, apiKey string, timeout time.Duration) *HTTPBackend {
	return &HTTPBackend{
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   modelName, apiKey: apiKey,
		client: &http.Client{Timeout: timeout},
	}
}

// Name returns a human-readable identifier for logs and the TUI status line.
func (b *HTTPBackend) Name() string { return "http:" + b.model }

// maxAttempts is 2: one initial call plus one retry on validation failure.
// We deliberately cap this low — if the model fails twice, the error message
// should reach the user rather than burning tokens on further guesses.
const maxAttempts = 2

// Generate runs the given prompt against the endpoint and returns the raw model
// text. It retries up to maxAttempts when validate(raw) returns an error,
// strengthening the user prompt on each validation failure so the model can
// self-correct. Transport errors retry with the unchanged prompt.
func (b *HTTPBackend) Generate(ctx context.Context, system, user string, schema []byte, validate func(string) error) (string, error) {
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		raw, err := b.call(ctx, system, user, schema)
		if err != nil {
			// Transport-level error (network failure, timeout, non-200 HTTP status).
			// Retrying with the unchanged prompt is correct here: there is nothing
			// useful to tell the model about a network hiccup.
			lastErr = err
			continue
		}
		if verr := validate(raw); verr == nil {
			return raw, nil
		} else {
			lastErr = verr
			// Strengthen the instruction for the retry so the model knows exactly
			// what constraint it violated.
			user += "\n\nYour previous answer was invalid: " + verr.Error() +
				". Return ONLY valid JSON matching the requested schema."
		}
	}
	return "", fmt.Errorf("llm generation failed after %d attempts: %w", maxAttempts, lastErr)
}

// chatRequest is the subset of the OpenAI chat schema we send.
// response_format carries the JSON schema when the provider supports
// structured output (OpenAI, compatible vLLM builds). Local providers
// (Ollama, LM Studio) silently ignore the field, so it is always safe to send.
type chatRequest struct {
	Model          string        `json:"model"`
	Messages       []chatMessage `json:"messages"`
	Temperature    float64       `json:"temperature"`
	ResponseFormat any           `json:"response_format,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// call performs a single HTTP POST to /chat/completions and returns the
// content string from the first choice. It does not interpret or validate
// the content — that is ParseAndValidate's job.
func (b *HTTPBackend) call(ctx context.Context, system, user string, schema []byte) (string, error) {
	payload := chatRequest{
		Model:       b.model,
		Temperature: 0,
		Messages: []chatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		// json_schema structured output: the provider constrains token sampling
		// so the output is always valid JSON matching our schema. Temperature=0
		// combined with structured output gives the most deterministic results.
		ResponseFormat: map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   "response",
				"schema": json.RawMessage(schema),
			},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal chat request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, b.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if b.apiKey != "" {
		// Bearer token covers OpenAI, OpenRouter, and most hosted APIs.
		// Local providers (Ollama, LM Studio) work fine without it.
		httpReq.Header.Set("Authorization", "Bearer "+b.apiKey)
	}
	resp, err := b.client.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read chat response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("llm http %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	// Inline the response shape here rather than defining a full struct — the
	// OpenAI response has many fields we don't use, and a minimal anonymous
	// struct is clearest for readers unfamiliar with the API shape.
	var out struct {
		Choices []struct {
			Message chatMessage `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", fmt.Errorf("decode chat response: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("no choices in chat response")
	}
	return out.Choices[0].Message.Content, nil
}
