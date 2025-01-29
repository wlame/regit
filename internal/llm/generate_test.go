// internal/llm/generate_test.go
package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
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
