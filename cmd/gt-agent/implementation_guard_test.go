package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestIsRetriableLLMError(t *testing.T) {
	t.Parallel()
	err := errors.New(`LLM API error (status 403): {"error":{"message":"Key limit exceeded (monthly limit)"}}`)
	if !isRetriableLLMError(err) {
		t.Fatal("expected 403 quota to be retriable")
	}
	if !isRetriableLLMError(errors.New(`LLM API error (status 402): {"error":{"message":"This request requires more credits"}}`)) {
		t.Fatal("expected 402 credits to be retriable")
	}
	if !isRetriableLLMError(errors.New(`empty completion (no choices in response): requested=openrouter/free actual=openrouter/free`)) {
		t.Fatal("expected empty completion to be retriable")
	}
	if !isRetriableLLMError(errors.New(`LLM API error (status 502): provider_unavailable`)) {
		t.Fatal("expected 502 provider unavailability to be retriable")
	}
	if !isRetriableLLMError(errors.New("no models available")) {
		t.Fatal("expected no-models-available to be retriable")
	}
	if isRetriableLLMError(errors.New("context canceled")) {
		t.Fatal("generic errors should not be retriable")
	}
}

func TestQAReworkCompletionReportsFailingGate(t *testing.T) {
	r := &stateRunner{
		townRoot: t.TempDir(), rig: "sample",
		task: &orchestrator.Task{PendingRework: &orchestrator.WorkflowRework{FromState: "qa_review", Summary: "Improve test coverage"}},
		v:    orchestrator.WorkflowValidation{RequiredFiles: []string{"service/test_main.py"}, TestRunner: "pytest"},
	}
	implementationPhaseVerifyOKHook = func(_, _ string, _ orchestrator.WorkflowValidation) error {
		return errors.New("python module verify failed: executable missing in configured environment")
	}
	countOpenMatchingBeadsHook = func(_, _ string, _ orchestrator.WorkflowValidation) (int, error) { return 0, nil }
	t.Cleanup(func() { implementationPhaseVerifyOKHook = nil; countOpenMatchingBeadsHook = nil })
	err := r.pendingQAReworkCompletionError()
	if err == nil || !strings.Contains(err.Error(), "executable missing") || !strings.Contains(err.Error(), "phase verification") {
		t.Fatalf("lost actual phase failure: %v", err)
	}
	implementationPhaseVerifyOKHook = func(_, _ string, _ orchestrator.WorkflowValidation) error { return nil }
	if err := r.pendingQAReworkCompletionError(); err == nil || !strings.Contains(err.Error(), "required artifacts") {
		t.Fatalf("green phase must still check required artifacts: %v", err)
	}
}

func TestCompletionRejectionBudgetTracksArtifactsNotSmokeCommands(t *testing.T) {
	for _, file := range []string{"test_main.py", "main_test.go"} {
		t.Run(file, func(t *testing.T) { testCompletionRejectionBudget(t, file) })
	}
}

func testCompletionRejectionBudget(t *testing.T, file string) {
	r := &stateRunner{
		townRoot: t.TempDir(), rig: "sample",
		hooks: orchestrator.StateHooks{NoProgressLimit: 3},
		v:     orchestrator.WorkflowValidation{LayoutRoot: "service", RequiredFiles: []string{"service/" + file}},
		track: &cmdTracker{},
	}
	path := filepath.Join(rigMayorRigDir(r.townRoot, r.rig), "service", file)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("assert True\n")
	if r.completionRejectionLimitReached() {
		t.Fatal("must allow an initial repair attempt")
	}
	// Successful manual smoke and a same-content rewrite are not progress.
	r.track.verifyOK = true
	write("assert True\n")
	if r.completionRejectionLimitReached() || !r.completionRejectionLimitReached() {
		t.Fatal("unchanged completion rejections must exhaust the budget")
	}
	write("assert response.status_code == 200\n")
	if r.completionRejectionLimitReached() {
		t.Fatal("real test changes must grant a fresh repair budget")
	}
	r.v.ActivePhaseIDField = "next-phase"
	if r.completionRejectionLimitReached() || r.completionRejections.count != 1 {
		t.Fatal("effective profile changes must grant a fresh repair budget")
	}
}

func TestRejectImplementationNoOpFailure_blocksWithoutFixWork(t *testing.T) {
	countOpenMatchingBeadsHook = func(_, _ string, _ orchestrator.WorkflowValidation) (int, error) { return 2, nil }
	defer func() { countOpenMatchingBeadsHook = nil }()

	task := &orchestrator.Task{
		WorkflowID: "wf-1",
		State:      "implementation",
		Hooks:      orchestrator.StateHooks{Track: "implementation", Artifacts: "implementation"},
		PendingRework: &orchestrator.WorkflowRework{
			FromState: "qa_review",
			Summary:   "GET /api/links returned 404",
		},
	}
	r := newStateRunner(task, t.TempDir(), "mockrig")
	msg, reject := r.rejectImplementationNoOpFailure("failure")
	if !reject || !strings.Contains(msg, "Rejected") || !strings.Contains(msg, "EDIT:") {
		t.Fatalf("reject=%v msg=%q", reject, msg)
	}
}

func TestRejectImplementationNoOpFailure_allowsAfterFixWork(t *testing.T) {
	countOpenMatchingBeadsHook = func(_, _ string, _ orchestrator.WorkflowValidation) (int, error) { return 1, nil }
	defer func() { countOpenMatchingBeadsHook = nil }()

	task := &orchestrator.Task{
		WorkflowID: "wf-1",
		State:      "implementation",
		Hooks:      orchestrator.StateHooks{Track: "implementation", Artifacts: "implementation"},
	}
	r := newStateRunner(task, t.TempDir(), "mockrig")
	r.attemptFixWork = true
	if _, reject := r.rejectImplementationNoOpFailure("failure"); reject {
		t.Fatal("should allow failure after fix attempt")
	}
}

func TestRejectImplementationNoOpFailure_blocksAfterEditSearchMiss(t *testing.T) {
	countOpenMatchingBeadsHook = func(_, _ string, _ orchestrator.WorkflowValidation) (int, error) { return 3, nil }
	defer func() { countOpenMatchingBeadsHook = nil }()

	task := &orchestrator.Task{
		WorkflowID: "wf-1",
		State:      "implementation",
		Hooks:      orchestrator.StateHooks{Track: "implementation", Artifacts: "implementation"},
	}
	r := newStateRunner(task, t.TempDir(), "mockrig")
	r.attemptEditSearchMiss = true
	msg, reject := r.rejectImplementationNoOpFailure("failure")
	if !reject || !strings.Contains(msg, "SEARCH") || !strings.Contains(msg, "Auto-READ") {
		t.Fatalf("reject=%v msg=%q", reject, msg)
	}
}

func TestRejectImplementationPrematureSuccess_blocksUnusedImport(t *testing.T) {
	countOpenMatchingBeadsHook = func(_, _ string, _ orchestrator.WorkflowValidation) (int, error) { return 2, nil }
	defer func() { countOpenMatchingBeadsHook = nil }()

	task := &orchestrator.Task{
		WorkflowID: "wf-1",
		State:      "implementation",
		Hooks:      orchestrator.StateHooks{Track: "implementation", Artifacts: "implementation"},
	}
	r := newStateRunner(task, t.TempDir(), "mockrig")
	r.track.verifyOK = false
	r.track.hadCmdFailure = true
	r.track.lastVerifyOutput = `./handlers_test.go:6:2: "fmt" imported and not used`
	msg, reject := r.rejectImplementationPrematureSuccess("success")
	if !reject || !strings.Contains(msg, "Rejected") || !strings.Contains(msg, "goimports") {
		t.Fatalf("reject=%v msg=%q", reject, msg)
	}
}

func TestRejectImplementationPrematureSuccess_allowsAfterVerifyOK(t *testing.T) {
	countOpenMatchingBeadsHook = func(_, _ string, _ orchestrator.WorkflowValidation) (int, error) { return 1, nil }
	defer func() { countOpenMatchingBeadsHook = nil }()

	task := &orchestrator.Task{
		WorkflowID: "wf-1",
		State:      "implementation",
		Hooks:      orchestrator.StateHooks{Track: "implementation", Artifacts: "implementation"},
	}
	r := newStateRunner(task, t.TempDir(), "mockrig")
	r.track.verifyOK = true
	r.track.lastVerifyOutput = "ok"
	if _, reject := r.rejectImplementationPrematureSuccess("success"); reject {
		t.Fatal("should allow success when verifyOK")
	}
}

func TestTryAutoOutcome_blockedOnQAPendingRework(t *testing.T) {
	countOpenMatchingBeadsHook = func(_, _ string, _ orchestrator.WorkflowValidation) (int, error) { return 0, nil }
	defer func() { countOpenMatchingBeadsHook = nil }()

	task := &orchestrator.Task{
		State: "implementation",
		Hooks: orchestrator.StateHooks{Track: "implementation", Artifacts: "implementation"},
		Validation: orchestrator.WorkflowValidation{
			QAVerifyCommand: "cd linkshelf && go test ./...",
		},
		PendingRework: &orchestrator.WorkflowRework{
			FromState: "qa_review",
			Summary:   "runtime smoke failed: GET / 404",
		},
	}
	r := newStateRunner(task, t.TempDir(), "mockrig")
	r.track.verifyOK = true
	if _, _, ok := r.tryAutoOutcome(); ok {
		t.Fatal("must not auto-complete implementation while QA rework is pending")
	}
}

func TestSummaryClaimsFalseBeadInfraFailure(t *testing.T) {
	if !summaryClaimsFalseBeadInfraFailure("Bead system corruption detected; reset .beads") {
		t.Fatal("expected corruption claim")
	}
	if summaryClaimsFalseBeadInfraFailure("handlers_test.go undefined: getLinksHandler") {
		t.Fatal("compile errors are not bead infra claims")
	}
}

func TestRejectImplementationFalseBeadInfraFailure_blocksHallucination(t *testing.T) {
	townRoot := "/home/stevef/gt"
	rig := "testgt3"
	if _, err := os.Stat(filepath.Join(townRoot, rig, "mayor", "rig")); err != nil {
		t.Skip("testgt3 rig not present")
	}
	if !orchestrator.BeadsDatabaseReady(townRoot, rig) {
		t.Skip("testgt3 beads DB not ready")
	}
	task := &orchestrator.Task{
		WorkflowID: "wf-1",
		State:      "implementation",
		Hooks:      orchestrator.StateHooks{Track: "implementation", Artifacts: "implementation"},
	}
	r := newStateRunner(task, townRoot, rig)
	msg, reject := r.rejectImplementationFalseBeadInfraFailure("failure", "Bead system corruption detected; reset .beads")
	if !reject || !strings.Contains(msg, "do **not** reset") {
		t.Fatalf("reject=%v msg=%q", reject, msg)
	}
}

func TestNoteImplementationFixAttempt_bdUpdate(t *testing.T) {
	task := &orchestrator.Task{
		State: "implementation",
		Hooks: orchestrator.StateHooks{Track: "implementation"},
	}
	r := newStateRunner(task, t.TempDir(), "mockrig")
	r.noteImplementationFixAttempt("export BEADS_DIR=x && bd update te-d0t --status=in_progress", false)
	if !r.attemptFixWork {
		t.Fatal("bd update should count as fix work")
	}
	r.attemptFixWork = false
	r.noteImplementationFixAttempt("cat mockrig/mayor/rig/SPEC.md", false)
	if r.attemptFixWork {
		t.Fatal("read-only cat should not count as fix work")
	}
}

func TestRejectQAVerifyPassedButOutputNoise_allowsRealCmdFailure(t *testing.T) {
	// FinAlly regression: the E2E compose command genuinely failed
	// ("no such service: playwright") and the summary cites it. The weak
	// auto-verify (`test -f docker-compose.yml`) exited 0, so verifyOK is true —
	// but the session DID have a real command failure. The guard must NOT reject
	// this as output noise.
	task := &orchestrator.Task{
		State: "qa_review",
		Hooks: orchestrator.StateHooks{Track: "qa"},
	}
	r := newStateRunner(task, t.TempDir(), "mockrig")
	r.track.verifyOK = true
	r.track.hadCmdFailure = true
	summary := "the E2E command failed before tests ran: no such service: playwright"
	if _, reject := r.rejectQAVerifyPassedButOutputNoise("failure", summary); reject {
		t.Fatal("real command failure must not be rejected as output noise")
	}
}

func TestRejectQAVerifyPassedButOutputNoise_blocksAuditNoise(t *testing.T) {
	// The intended purpose: verify exited 0, nothing failed in the session, but
	// the LLM reports failure citing npm audit output. That is spurious.
	task := &orchestrator.Task{
		State: "qa_review",
		Hooks: orchestrator.StateHooks{Track: "qa"},
	}
	r := newStateRunner(task, t.TempDir(), "mockrig")
	r.track.verifyOK = true
	r.track.hadCmdFailure = false
	summary := "npm install reported 3 vulnerabilities and 1 high severity funding issue"
	msg, reject := r.rejectQAVerifyPassedButOutputNoise("failure", summary)
	if !reject || !strings.Contains(msg, "Rejected") {
		t.Fatalf("audit noise should be rejected: reject=%v msg=%q", reject, msg)
	}
}

func TestRejectQAVerifyPassedButOutputNoise_ignoresNonQAState(t *testing.T) {
	task := &orchestrator.Task{
		State: "implementation",
		Hooks: orchestrator.StateHooks{Track: "implementation"},
	}
	r := newStateRunner(task, t.TempDir(), "mockrig")
	r.track.verifyOK = true
	r.track.hadCmdFailure = false
	if _, reject := r.rejectQAVerifyPassedButOutputNoise("failure", "npm audit found 3 vulnerabilities"); reject {
		t.Fatal("guard is scoped to qa_review state only")
	}
}
