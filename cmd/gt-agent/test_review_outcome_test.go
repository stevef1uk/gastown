package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/gastown/internal/llm"
	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestTestReviewFailureSurvivesInspection(t *testing.T) {
	verify := "cd linkshelf && npx playwright test"
	r := &stateRunner{
		v: orchestrator.WorkflowValidation{
			LayoutRoot: "linkshelf", QAVerifyCommand: "cd linkshelf && go test ./...",
			ActivePhaseIDField: "e2e-testing",
			DeliveryPhases:     []orchestrator.DeliveryPhase{{ID: "e2e-testing", QAVerifyCommand: verify}},
		},
		track: &cmdTracker{verifyOK: true}, // Earlier verification must not survive failure.
	}
	track := trackHandlers["test_review"]
	track(r, verify, errors.New("No tests found"))
	for _, cmd := range []string{
		"cd linkshelf && go test ./...",
		"grep -n test linkshelf/tests/e2e.spec.js",
		"find linkshelf -name test-results",
		"cat test-report.md",
	} {
		track(r, cmd, nil)
		if r.track.verifyOK || !r.track.hadCmdFailure {
			t.Fatalf("%q erased failed phase verification: %+v", cmd, r.track)
		}
	}
	track(r, verify, nil)
	if !r.track.verifyOK || r.track.hadCmdFailure {
		t.Fatal("a successful phase verification must allow recovery")
	}
	// A failed test command that also reads output must still revoke success.
	track(r, verify+" | cat", errors.New("3 failed"))
	if r.track.verifyOK || !r.track.hadCmdFailure {
		t.Fatal("failed verify with read-only output command retained success")
	}
}

func TestTestReviewPhasedSuccessCannotIgnoreFailedCommands(t *testing.T) {
	v := orchestrator.WorkflowValidation{
		ActivePhaseIDField: "e2e-testing",
		DeliveryPhases:     []orchestrator.DeliveryPhase{{ID: "e2e-testing"}},
	}
	err := validateTestReviewArtifacts(t.TempDir(), "mockrig", "success", true, true, v)
	if err == nil || !strings.Contains(err.Error(), "failed commands") {
		t.Fatalf("expected failed-command gate despite earlier green verify, got %v", err)
	}
}

func TestExplicitReviewCannotAutoComplete(t *testing.T) {
	// A valid artifact must not bypass the reviewing agent, including at startup.
	r := &stateRunner{
		task:  &orchestrator.Task{AllowedOutcomes: []string{"success", "failure"}},
		hooks: orchestrator.StateHooks{Artifacts: "qa_review", RequireExplicitOutcome: true},
		track: &cmdTracker{verifyOK: true},
	}
	if err := artifactAutoCompleters["qa_review"](r); err != nil {
		t.Fatalf("fixture must otherwise auto-complete: %v", err)
	}
	if o, _, ok := r.tryAutoOutcome(); ok {
		t.Fatalf("review auto-completed with %q before explicit verdict", o)
	}
}

func TestTestReviewAcceptsEarlyFailureWithCommands(t *testing.T) {
	town, rig := t.TempDir(), "mockrig"
	if err := os.MkdirAll(rigMayorRigDir(town, rig), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rigMayorRigDir(town, rig), "SPEC.md"), []byte("Test fixture"), 0644); err != nil {
		t.Fatal(err)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls++
		if calls > 1 {
			t.Error("Tester failure was ignored instead of returning to implementation")
		}
		content := "CMD: cd mockrig/mayor/rig && cat SPEC.md\n\n" +
			`{"outcome":"failure","summary":"mock-123: Playwright reported No tests found"}`
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]string{"content": content}}},
		})
	}))
	defer server.Close()
	task := &orchestrator.Task{
		State: "test_review", Rig: rig,
		AllowedOutcomes: []string{"success", "failure", "plan_gap", "architecture_failure"},
	}
	client := llm.NewClient(server.URL, "test", "tester", time.Second)
	outcome, summary, _, err := executeOrchestratedTask(context.Background(), client, town, rig, "", task, nil)
	if err != nil || outcome != "failure" || !strings.Contains(summary, "No tests found") || calls != 1 {
		t.Fatalf("outcome=%q summary=%q calls=%d err=%v", outcome, summary, calls, err)
	}
}
