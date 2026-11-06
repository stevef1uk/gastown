package llm

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type admissionTransport func(*http.Request) (*http.Response, error)

func (fn admissionTransport) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

func TestAdmissionRunsBeforeEachRetry(t *testing.T) {
	client := NewClient("http://example.invalid/chat/completions", "sample", "polecat", 0)
	admitted, sent := 0, 0
	client.BeforeRequest = func(tokens int64) error {
		admitted++
		if tokens <= 8192 {
			t.Fatal("missing input reservation")
		}
		if admitted > 1 {
			return errors.New("execution budget exhausted")
		}
		return nil
	}
	client.client.Transport = admissionTransport(func(*http.Request) (*http.Response, error) {
		sent++
		return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader("temporary")), Header: make(http.Header)}, nil
	})
	if _, err := client.CompleteMessages(context.Background(), []Message{{Role: "user", Content: "sample"}}); err == nil || !strings.Contains(err.Error(), "execution budget") {
		t.Fatalf("err=%v", err)
	}
	if sent != 1 || admitted != 2 {
		t.Fatalf("sent=%d admissions=%d", sent, admitted)
	}
}

func TestUsageAndWorkflowHeadersSurviveResponse(t *testing.T) {
	client := NewClient("http://example.invalid/chat/completions", "sample", "qa", 0)
	client.TaskHeaders = map[string]string{"X-GasTown-Workflow": "wf-5"}
	client.client.Transport = admissionTransport(func(req *http.Request) (*http.Response, error) {
		if req.Header.Get("X-GasTown-Workflow") != "wf-5" {
			t.Fatal("workflow identity missing")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"model":"actual","choices":[{"message":{"content":"done"}}],"usage":{"prompt_tokens":12,"completion_tokens":4,"total_tokens":16,"cost":0.01}}`)), Header: make(http.Header)}, nil
	})
	if _, err := client.CompleteMessages(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if client.LastUsage.TotalTokens != 16 || client.LastUsage.Cost != 0.01 || client.LastModel != "actual" {
		t.Fatalf("lost usage: %+v", client.LastUsage)
	}
}

func TestRetryAfterUnderstandsSecondsAndHTTPDate(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, test := range []struct {
		raw  string
		want time.Duration
	}{{"12", 12 * time.Second}, {now.Add(30 * time.Second).Format(http.TimeFormat), 30 * time.Second}, {"9999", 2 * time.Minute}, {"broken", 0}} {
		if got := llmRetryAfter(test.raw, now); got != test.want {
			t.Fatalf("%s: %s", test.raw, got)
		}
	}
}
