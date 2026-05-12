// internal/llm/http_test.go
package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wlame/regit/internal/model"
)

func sampleReq() SplitRequest {
	return SplitRequest{
		CommitMessage: "do two things",
		Hunks: []model.Hunk{
			{ID: "H1", File: "a"}, {ID: "H2", File: "b"},
		},
		MaxCommits: 4,
	}
}

// TestHTTPBackend_truncatesHugeErrorBody proves a giant non-200 body is bounded in
// the returned error rather than flooding the terminal or logs.
func TestHTTPBackend_truncatesHugeErrorBody(t *testing.T) {
	huge := strings.Repeat("A", 5<<20) // 5 MiB error page
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(huge))
	}))
	defer srv.Close()

	b := NewHTTPBackend(srv.URL, "test-model", "", 5*time.Second)
	_, err := b.Generate(context.Background(), "sys", "user", nil, func(string) error { return nil })
	if err == nil {
		t.Fatal("expected an error for a 500 response")
	}
	if len(err.Error()) > maxErrorBodyBytes+200 {
		t.Errorf("error body not truncated: len=%d", len(err.Error()))
	}
	if !strings.Contains(err.Error(), "truncated") {
		t.Errorf("expected a truncation marker, got: %.80s…", err.Error())
	}
}

func TestHTTPBackend_validJSON_parsesProposal(t *testing.T) {
	body := `{"choices":[{"message":{"content":"{\"should_split\":true,\"groups\":[{\"order\":1,\"hunk_ids\":[\"H1\"],\"subject\":\"first\"},{\"order\":2,\"hunk_ids\":[\"H2\"],\"subject\":\"second\"}]}"}}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	}))
	defer srv.Close()

	b := NewHTTPBackend(srv.URL, "test-model", "", 5*time.Second)
	prop, err := ProposeSplit(context.Background(), b, sampleReq(), SchemaJSON())
	if err != nil {
		t.Fatal(err)
	}
	if len(prop.Groups) != 2 {
		t.Fatalf("want 2 groups, got %d", len(prop.Groups))
	}
}

func TestHTTPBackend_shouldSplitFalse_skipsValidation(t *testing.T) {
	// When the model decides the commit is already small enough it returns
	// should_split=false with an empty groups array. ParseAndValidate must
	// not run coverage validation in that case (there are no groups to check),
	// so ProposeSplit should succeed without error.
	body := `{"choices":[{"message":{"content":"{\"should_split\":false,\"groups\":[],\"reason\":\"too small\"}"}}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	}))
	defer srv.Close()

	b := NewHTTPBackend(srv.URL, "test-model", "", 5*time.Second)
	prop, err := ProposeSplit(context.Background(), b, sampleReq(), SchemaJSON())
	if err != nil {
		t.Fatalf("expected no error for should_split=false, got %v", err)
	}
	if prop.ShouldSplit {
		t.Fatal("expected ShouldSplit==false")
	}
	if len(prop.Groups) != 0 {
		t.Fatalf("expected empty groups, got %d", len(prop.Groups))
	}
}

func TestHTTPBackend_badThenGoodJSON_retries(t *testing.T) {
	calls := 0
	good := `{"choices":[{"message":{"content":"{\"should_split\":true,\"groups\":[{\"order\":1,\"hunk_ids\":[\"H1\",\"H2\"],\"subject\":\"all\"}]}"}}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Write([]byte(`{"choices":[{"message":{"content":"not json"}}]}`))
			return
		}
		w.Write([]byte(good))
	}))
	defer srv.Close()

	b := NewHTTPBackend(srv.URL, "test-model", "", 5*time.Second)
	prop, err := ProposeSplit(context.Background(), b, sampleReq(), SchemaJSON())
	if err != nil {
		t.Fatalf("expected retry success, got %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected 2 calls (1 retry), got %d", calls)
	}
	if len(prop.Groups) != 1 {
		t.Fatalf("want 1 group, got %d", len(prop.Groups))
	}
}
