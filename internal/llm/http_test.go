// internal/llm/http_test.go
package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
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

func TestHTTPBackend_validJSON_parsesProposal(t *testing.T) {
	body := `{"choices":[{"message":{"content":"{\"should_split\":true,\"groups\":[{\"order\":1,\"hunk_ids\":[\"H1\"],\"subject\":\"first\"},{\"order\":2,\"hunk_ids\":[\"H2\"],\"subject\":\"second\"}]}"}}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	}))
	defer srv.Close()

	b := NewHTTPBackend(srv.URL, "test-model", "", 5*time.Second)
	prop, err := b.Propose(context.Background(), sampleReq(), SchemaJSON())
	if err != nil {
		t.Fatal(err)
	}
	if len(prop.Groups) != 2 {
		t.Fatalf("want 2 groups, got %d", len(prop.Groups))
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
	prop, err := b.Propose(context.Background(), sampleReq(), SchemaJSON())
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
