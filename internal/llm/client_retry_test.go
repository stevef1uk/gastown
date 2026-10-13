package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func emptyCompletionBody(model string) string {
	return `{"model":"` + model + `","choices":[]}`
}

func validCompletionBody(model, content string) string {
	body := map[string]any{
		"model": model,
		"choices": []any{
			map[string]any{"message": map[string]any{"content": content}},
		},
	}
	b, _ := json.Marshal(body)
	return string(b)
}

// TestClient_EmptyChoicesRetried verifies a 200 response with an empty choices
// array is retried (previously it returned an error immediately, causing the
// agent to complete a workflow step with an empty summary and loop).
func TestClient_EmptyChoicesRetried(t *testing.T) {
	t.Parallel()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusOK)
		if n == 1 {
			_, _ = w.Write([]byte(emptyCompletionBody("openrouter/free")))
			return
		}
		_, _ = w.Write([]byte(validCompletionBody("openrouter/free", "hello")))
	}))
	defer srv.Close()

	c := NewClient(srv.URL+"/v1/chat/completions", "openrouter/free", "", 5*time.Second)
	got, err := c.CompleteMessages(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "hello" {
		t.Fatalf("content = %q, want hello", got)
	}
	if n := atomic.LoadInt32(&calls); n != 2 {
		t.Fatalf("calls = %d, want 2 (empty response must trigger a retry)", n)
	}
}

// TestClient_EmptyChoicesErrorNamesModel verifies that when empty completions
// persist, the surfaced error is distinguishable and names the model.
func TestClient_EmptyChoicesErrorNamesModel(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(emptyCompletionBody("openrouter/free")))
	}))
	defer srv.Close()

	c := NewClient(srv.URL+"/v1/chat/completions", "openrouter/free", "", 1200*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()
	_, err := c.CompleteMessages(ctx, []Message{{Role: "user", Content: "hi"}})
	if err == nil {
		t.Fatal("expected error for persistent empty completions")
	}
	if !strings.Contains(err.Error(), "empty completion") {
		t.Fatalf("error %q should mention empty completion", err.Error())
	}
	if !strings.Contains(err.Error(), "openrouter/free") {
		t.Fatalf("error %q should name the model", err.Error())
	}
}
