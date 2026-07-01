// internal/llm/generate_test.go
package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var errInvalid = fmt.Errorf("invalid")

// alwaysOK accepts any raw string.
func alwaysOK(string) error { return nil }

func TestHTTPGenerate_returnsContent(t *testing.T) {
	body := `{"choices":[{"message":{"content":"{\"ok\":true}"}}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	defer srv.Close()
	b := NewHTTPBackend(srv.URL, "m", "", 5*time.Second)
	raw, err := b.Generate(context.Background(), "sys", "usr", []byte(`{"type":"object"}`), alwaysOK)
	if err != nil {
		t.Fatal(err)
	}
	if raw != `{"ok":true}` {
		t.Fatalf("raw = %q", raw)
	}
}

func TestHTTPGenerate_retriesOnValidateFail(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Write([]byte(`{"choices":[{"message":{"content":"garbage"}}]}`))
			return
		}
		w.Write([]byte(`{"choices":[{"message":{"content":"{\"ok\":true}"}}]}`))
	}))
	defer srv.Close()
	b := NewHTTPBackend(srv.URL, "m", "", 5*time.Second)
	// validate rejects anything without "ok".
	validate := func(raw string) error {
		if raw != `{"ok":true}` {
			return errInvalid
		}
		return nil
	}
	raw, err := b.Generate(context.Background(), "sys", "usr", []byte(`{}`), validate)
	if err != nil {
		t.Fatalf("expected retry success: %v", err)
	}
	if calls != 2 || raw != `{"ok":true}` {
		t.Fatalf("calls=%d raw=%q", calls, raw)
	}
}

// TestGenerateWithRetry_contract unit-tests the shared retry loop both
// backends delegate to: a transport error retries with the unchanged prompt, a
// validation failure retries with a strengthened prompt, and exhaustion
// reports the last error under the backend label.
func TestGenerateWithRetry_contract(t *testing.T) {
	// Validation failure → the second call sees the strengthened prompt.
	var prompts []string
	raw, err := generateWithRetry("test", "base", func(s string) error {
		if len(prompts) < 2 {
			return errInvalid
		}
		return nil
	}, func(user string) (string, error) {
		prompts = append(prompts, user)
		return "raw", nil
	})
	if err != nil || raw != "raw" {
		t.Fatalf("got %q, %v", raw, err)
	}
	if len(prompts) != 2 || prompts[0] != "base" {
		t.Fatalf("prompts = %q", prompts)
	}
	if prompts[1] == "base" {
		t.Error("retry prompt should be strengthened after a validation failure")
	}

	// Transport error → unchanged prompt on retry.
	prompts = nil
	calls := 0
	_, err = generateWithRetry("test", "base", alwaysOK, func(user string) (string, error) {
		prompts = append(prompts, user)
		calls++
		return "", fmt.Errorf("boom")
	})
	if err == nil || calls != maxAttempts {
		t.Fatalf("expected exhaustion after %d attempts, got calls=%d err=%v", maxAttempts, calls, err)
	}
	for _, p := range prompts {
		if p != "base" {
			t.Errorf("transport retry must not strengthen the prompt, got %q", p)
		}
	}
	if !strings.Contains(err.Error(), "test generation failed") {
		t.Errorf("error should carry the backend label: %v", err)
	}
}
