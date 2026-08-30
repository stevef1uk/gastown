package specprofile

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestValidatePhaseUpdates_FailClosed_NoEndpoint(t *testing.T) {
	updates := map[string]string{"phase-a": "echo ok"}
	got := validatePhaseUpdates(context.Background(), "", "some-model", nil, updates)
	if got != nil {
		t.Fatalf("expected nil (fail-closed), got %v", got)
	}
}

func TestValidatePhaseUpdates_FailClosed_NoModel(t *testing.T) {
	updates := map[string]string{"phase-a": "echo ok"}
	got := validatePhaseUpdates(context.Background(), "http://localhost/v1", "", nil, updates)
	if got != nil {
		t.Fatalf("expected nil (fail-closed), got %v", got)
	}
}

func TestValidatePhaseUpdates_FailClosed_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	}))
	defer srv.Close()

	updates := map[string]string{"phase-a": "echo ok"}
	got := validatePhaseUpdates(context.Background(), srv.URL, "test-model", nil, updates)
	if got != nil {
		t.Fatalf("expected nil on server error (fail-closed), got %v", got)
	}
}

func TestValidatePhaseUpdates_FailClosed_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("not json at all"))
	}))
	defer srv.Close()

	updates := map[string]string{"phase-a": "echo ok"}
	got := validatePhaseUpdates(context.Background(), srv.URL, "test-model", nil, updates)
	if got != nil {
		t.Fatalf("expected nil on invalid JSON (fail-closed), got %v", got)
	}
}

func TestValidatePhaseUpdates_FailClosed_EmptyChoices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []interface{}{},
		})
	}))
	defer srv.Close()

	updates := map[string]string{"phase-a": "echo ok"}
	got := validatePhaseUpdates(context.Background(), srv.URL, "test-model", nil, updates)
	if got != nil {
		t.Fatalf("expected nil on empty choices (fail-closed), got %v", got)
	}
}

func TestValidatePhaseUpdates_FailClosed_TruncatedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{
					"message":        map[string]string{"content": `{"phase-a": "ap`},
					"finish_reason": "length",
				},
			},
		})
	}))
	defer srv.Close()

	updates := map[string]string{"phase-a": "echo ok"}
	got := validatePhaseUpdates(context.Background(), srv.URL, "test-model", nil, updates)
	if got != nil {
		t.Fatalf("expected nil on truncated response (fail-closed), got %v", got)
	}
}

func TestValidatePhaseUpdates_ApprovedOnly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{
					"message": map[string]string{
						"content": `{"phase-a": "approve", "phase-b": "reject"}`,
					},
					"finish_reason": "stop",
				},
			},
		})
	}))
	defer srv.Close()

	updates := map[string]string{
		"phase-a": "echo approved",
		"phase-b": "echo rejected",
	}
	got := validatePhaseUpdates(context.Background(), srv.URL, "test-model", nil, updates)
	if got == nil {
		t.Fatal("expected non-nil result")
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 approved update, got %d: %v", len(got), got)
	}
	if _, ok := got["phase-a"]; !ok {
		t.Fatalf("expected phase-a approved, got %v", got)
	}
	if _, ok := got["phase-b"]; ok {
		t.Fatalf("expected phase-b rejected, but it was included: %v", got)
	}
}

func TestValidatePhaseUpdates_AllRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{
					"message": map[string]string{
						"content": `{"phase-a": "reject", "phase-b": "reject"}`,
					},
					"finish_reason": "stop",
				},
			},
		})
	}))
	defer srv.Close()

	updates := map[string]string{
		"phase-a": "echo a",
		"phase-b": "echo b",
	}
	got := validatePhaseUpdates(context.Background(), srv.URL, "test-model", nil, updates)
	if got == nil {
		t.Fatal("expected non-nil result (empty map)")
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 approved updates, got %d: %v", len(got), got)
	}
}

func TestValidatePhaseUpdates_MissingVerdictRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{
					"message": map[string]string{
						"content": `{"phase-a": "approve"}`,
					},
					"finish_reason": "stop",
				},
			},
		})
	}))
	defer srv.Close()

	updates := map[string]string{
		"phase-a": "echo a",
		"phase-b": "echo b",
	}
	got := validatePhaseUpdates(context.Background(), srv.URL, "test-model", nil, updates)
	if got == nil {
		t.Fatal("expected non-nil result")
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 approved (phase-a), got %d: %v", len(got), got)
	}
	if _, ok := got["phase-b"]; ok {
		t.Fatal("phase-b should be rejected (no verdict = rejected)")
	}
}
