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

func TestTestReviewRejectsMaskedVerification(t *testing.T) {
	v := orchestrator.WorkflowValidation{
		LayoutRoot: "linkshelf", TestRunner: "go",
		QAVerifyCommand: "cd linkshelf && npx playwright test",
	}
	for _, cmd := range []string{
		"cd mockrig/mayor/rig && cd linkshelf && npx playwright test & sleep 4 && curl -sf http://127.0.0.1:8080/health",
		"cd linkshelf && npx playwright test; true",
		"cd linkshelf && npx playwright test || true",
		"cd linkshelf && npx playwright test | cat",
		"cd linkshelf && npx playwright test --list",
		"cd linkshelf && npx playwright test --config=playwright.config.js",
		"cd linkshelf && npx playwright test --pass-with-no-tests",
		"printf 'npx playwright test'",
	} {
		t.Run(cmd, func(t *testing.T) {
			r := &stateRunner{
				v: v, rig: "mockrig", townRoot: t.TempDir(),
				hooks: orchestrator.StateHooks{Track: "test_review"},
				track: &cmdTracker{hadCmdFailure: true},
			}
			rewritten := r.rewriteCommand(cmd)
			trackHandlers["test_review"](r, rewritten, nil)
			if r.track.verifyOK || !r.track.hadCmdFailure {
				t.Fatalf("masked verification credited: %q → %q", cmd, rewritten)
			}
		})
	}
	for _, cmd := range []string{
		v.QAVerifyCommand,
		"cd mockrig/mayor/rig && cd linkshelf && npx playwright test",
		"cd mockrig/mayor/rig/linkshelf && npx playwright test",
	} {
		if !testReviewVerifyCommandMatches(cmd, v) {
			t.Errorf("canonical verification rejected: %q", cmd)
		}
	}
}

func TestTestReviewRequiresCompleteVerifyChain(t *testing.T) {
	v := orchestrator.WorkflowValidation{QAVerifyCommand: "cd linkshelf && npm ci && npx playwright test"}
	if testReviewVerifyCommandMatches("cd linkshelf && npm ci", v) {
		t.Fatal("dependency installation alone must not satisfy a test suite")
	}
	if !testReviewVerifyCommandMatches("cd mockrig/mayor/rig/linkshelf && npm ci && npx playwright test", v) {
		t.Fatal("complete canonical verification chain must match")
	}
}

func TestTestReviewMaskedShellSuccessDoesNotPass(t *testing.T) {
	town, rig := t.TempDir(), "mockrig"
	project := filepath.Join(rigMayorRigDir(town, rig), "linkshelf")
	tools := filepath.Join(town, "tools")
	for _, dir := range []string{project, tools} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	for name, script := range map[string]string{
		"npx":  "#!/bin/sh\nprintf '3 failed\\n'\nexit 1\n",
		"curl": "#!/bin/sh\nprintf '{\"status\":\"ok\"}\\n'\nexit 0\n",
	} {
		if err := os.WriteFile(filepath.Join(tools, name), []byte(script), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	r := &stateRunner{
		townRoot: town, rig: rig,
		task:  &orchestrator.Task{State: "test_review"},
		hooks: orchestrator.StateHooks{CmdGuard: "test_review", Track: "test_review", Artifacts: "test_review"},
		v: orchestrator.WorkflowValidation{
			LayoutRoot: "linkshelf", TestRunner: "go", QAVerifyCommand: "cd linkshelf && npx playwright test",
		},
		track: &cmdTracker{},
	}
	var feedback strings.Builder
	r.processOrchestratedTools("CMD: cd mockrig/mayor/rig/linkshelf && npx playwright test", "test", &feedback)
	if r.track.verifyOK || !r.track.hadCmdFailure {
		t.Fatal("fixture's failing test suite must register failure")
	}
	// This really exits zero: the shell returns curl's status, even though npx
	// fails. It must not replace the failed foreground verification result.
	masked := "cd mockrig/mayor/rig/linkshelf && npx playwright test & curl -sf http://127.0.0.1:8080/health"
	out, err := r.runShellCommand(masked, town, "test", os.Environ())
	if err != nil || !strings.Contains(string(out), "3 failed") || !strings.Contains(string(out), `"status":"ok"`) {
		t.Fatalf("fixture must reproduce failed tests with exit 0: out=%q err=%v", out, err)
	}
	r.afterCommand(masked, err, town, "test", os.Environ(), &feedback)
	if r.track.verifyOK || !r.track.hadCmdFailure {
		t.Fatal("healthy HTTP probe overrode failed test suite")
	}
	if err := r.validateArtifacts("success"); err == nil || !strings.Contains(err.Error(), "failed commands") {
		t.Fatalf("false success verdict must be rejected: %v", err)
	}
}

func TestPlaywrightIsNotRewrittenAsDevServer(t *testing.T) {
	cmd := "cd mockrig/mayor/rig && cd linkshelf && npx playwright test"
	v := orchestrator.WorkflowValidation{
		LayoutRoot: "linkshelf", TestRunner: "go",
		QAVerifyCommand: "cd linkshelf && npx playwright test",
		RequiredFiles:   []string{"linkshelf/cmd/server/main.go", "linkshelf/web/index.html"},
	}
	if rewritten, changed := normalizeGoDevServerSmokeCommand(cmd, t.TempDir(), "mockrig", v); changed || rewritten != cmd {
		t.Fatalf("Playwright was rewritten as a background server: %q", rewritten)
	}
}

func TestTestReviewFailedReportWriteRevokesSuccess(t *testing.T) {
	r := &stateRunner{track: &cmdTracker{verifyOK: true}}
	trackHandlers["test_review"](r, "cat > test-report.md <<'EOF'\nPASS\nEOF\n.", errors.New("exit status 2"))
	if r.track.verifyOK || !r.track.hadCmdFailure {
		t.Fatal("failed report heredoc must not be treated as a read-only cat")
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
