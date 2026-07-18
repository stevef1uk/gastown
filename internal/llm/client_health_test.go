package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHealthCheckURL(t *testing.T) {
	t.Parallel()
	got := HealthCheckURL("http://127.0.0.1:11434/v1/chat/completions")
	if got != "http://127.0.0.1:11434/v1/models" {
		t.Fatalf("got %q", got)
	}
}

func TestClient_Ping_ok(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL+"/v1/chat/completions", "test", "", 2*time.Second)
	if err := c.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestClient_Ping_connectionRefused(t *testing.T) {
	t.Parallel()
	c := NewClient("http://127.0.0.1:1/v1/chat/completions", "test", "", time.Second)
	if err := c.Ping(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}
