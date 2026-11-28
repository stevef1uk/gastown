package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestIsRetriableLLMError(t *testing.T) {
	t.Parallel()
	err := errors.New(`LLM API error (status 403): {"error":{"message":"Key limit exceeded (monthly limit)"}}`)
	if isRetriableLLMError(err) {
		t.Fatal("403 quota must stop dispatch")
	}
	if isRetriableLLMError(errors.New(`LLM API error (status 402): {"error":{"message":"This request requires more credits"}}`)) {
		t.Fatal("402 credits must stop dispatch")
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

func TestValidateNodeImplementationCommand_blocksHostPlaywrightInstallForDocker(t *testing.T) {
	t.Parallel()

	v := orchestrator.WorkflowValidation{
		LayoutRoot:      "pingapp",
		TestRunner:      "go",
		QAVerifyCommand: "docker compose -f pingapp/docker-compose.yml up --exit-code-from playwright",
		RequiredFiles:   []string{"pingapp/docker-compose.yml"},
	}
	if err := validateNodeImplementationCommand("npm install --ignore-scripts && npx playwright install --with-deps chromium", v); err == nil ||
		!strings.Contains(err.Error(), "do not install Playwright browsers or system dependencies on the host") {
		t.Fatalf("Docker workflow should reject host Playwright dependency installation, got %v", err)
	}
	if err := validateNodeImplementationCommand("docker compose -f pingapp/docker-compose.yml up --exit-code-from playwright", v); err != nil {
		t.Fatalf("Docker Compose verification should remain allowed: %v", err)
	}
}

func TestValidateNodeImplementationCommand_allowsPlaywrightInstallForNonDockerNode(t *testing.T) {
	t.Parallel()

	v := orchestrator.WorkflowValidation{
		LayoutRoot:      "web",
		TestRunner:      "npm",
		QAVerifyCommand: "npm test",
		RequiredFiles:   []string{"web/package.json"},
	}
	if err := validateNodeImplementationCommand("npx playwright install --with-deps chromium", v); err != nil {
		t.Fatalf("non-Docker Node workflow should preserve existing behavior: %v", err)
	}
}

func TestValidateNodeImplementationCommand_allowsDeclaredDependencyFreeNodeVerify(t *testing.T) {
	t.Parallel()

	v := orchestrator.WorkflowValidation{
		LayoutRoot:      "linkshelf",
		TestRunner:      "go",
		QAVerifyCommand: "node linkshelf/tests/frontend-ui.spec.js",
		RequiredFiles:   []string{"linkshelf/tests/frontend-ui.spec.js"},
	}
	if orchestrator.WorkflowUsesNodeJS(v) {
		t.Fatal("a direct Node verification script should not require Node project setup")
	}
	for _, cmd := range []string{
		"node linkshelf/tests/frontend-ui.spec.js",
		"cd testgt3/mayor/rig && node linkshelf/tests/frontend-ui.spec.js",
	} {
		if err := validateNodeImplementationCommand(cmd, v); err != nil {
			t.Errorf("declared Node verification %q should be allowed: %v", cmd, err)
		}
	}
	if err := validateNodeImplementationCommand("node unrelated.js", v); err == nil {
		t.Fatal("an undeclared Node command should remain blocked")
	}
	if err := validateNodeImplementationCommand("node linkshelf/tests/frontend-ui.spec.js && node unrelated.js", v); err == nil {
		t.Fatal("additional Node commands should remain blocked")
	}
	if err := validateNodeImplementationCommand("npm install", v); err == nil {
		t.Fatal("npm should remain blocked when the phase declares only a direct Node script")
	}
}

func TestValidateNodeImplementationCommand_allowsDeclaredMultiCommandNodeVerify(t *testing.T) {
	t.Parallel()

	v := orchestrator.WorkflowValidation{
		LayoutRoot:         "linkshelf",
		TestRunner:         "go",
		ActivePhaseIDField: "frontend-ui",
		QAVerifyCommand:    "node linkshelf/tests/frontend-ui.spec.js && node --check linkshelf/web/app.js",
		RequiredFiles:      []string{"linkshelf/tests/frontend-ui.spec.js", "linkshelf/web/app.js"},
		DeliveryPhases: []orchestrator.DeliveryPhase{{
			ID:              "frontend-ui",
			Title:           "frontend-ui",
			RequiredFiles:   []string{"linkshelf/tests/frontend-ui.spec.js", "linkshelf/web/app.js"},
			QAVerifyCommand: "node linkshelf/tests/frontend-ui.spec.js && node --check linkshelf/web/app.js",
		}},
	}
	for _, cmd := range []string{
		"node linkshelf/tests/frontend-ui.spec.js && node --check linkshelf/web/app.js",
		"cd testgt3/mayor/rig/linkshelf && node ./tests/frontend-ui.spec.js && node --check ./web/app.js",
	} {
		if err := validateNodeImplementationCommand(cmd, v); err != nil {
			t.Errorf("declared Node verification %q should be allowed: %v", cmd, err)
		}
	}
	for _, cmd := range []string{
		"node --check linkshelf/web/app.js",
		"node linkshelf/tests/frontend-ui.spec.js && node --check linkshelf/web/app.js && node unrelated.js",
		"npm install",
	} {
		if err := validateNodeImplementationCommand(cmd, v); err == nil {
			t.Errorf("undeclared or partial Node command %q should remain blocked", cmd)
		}
	}
}

func TestIsImplementationVerifyCommandOK_matchesDeclaredMultiCommandNodeVerify(t *testing.T) {
	t.Parallel()

	v := orchestrator.WorkflowValidation{
		LayoutRoot:         "linkshelf",
		TestRunner:         "go",
		ActivePhaseIDField: "frontend-ui",
		QAVerifyCommand:    "node linkshelf/tests/frontend-ui.spec.js && node --check linkshelf/web/app.js",
		RequiredFiles:      []string{"linkshelf/tests/frontend-ui.spec.js", "linkshelf/web/app.js"},
		DeliveryPhases: []orchestrator.DeliveryPhase{{
			ID:              "frontend-ui",
			Title:           "frontend-ui",
			RequiredFiles:   []string{"linkshelf/tests/frontend-ui.spec.js", "linkshelf/web/app.js"},
			QAVerifyCommand: "node linkshelf/tests/frontend-ui.spec.js && node --check linkshelf/web/app.js",
		}},
	}
	cmd := "cd testgt3/mayor/rig/linkshelf && node ./tests/frontend-ui.spec.js && node --check ./web/app.js"
	if !isImplementationVerifyCommandOK(cmd, "", "", "", v) {
		t.Fatal("configured relative Node verification should count as green implementation verify")
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

func TestTesterReworkCannotFailWithoutRepairEvenWithEmptyQueue(t *testing.T) {
	countOpenMatchingBeadsHook = func(_, _ string, _ orchestrator.WorkflowValidation) (int, error) { return 0, nil }
	implementationPhaseVerifyOKHook = func(_, _ string, _ orchestrator.WorkflowValidation) error { return nil }
	t.Cleanup(func() { countOpenMatchingBeadsHook = nil; implementationPhaseVerifyOKHook = nil })
	for _, runner := range []string{"go", "pytest"} {
		t.Run(runner, func(t *testing.T) {
			r := newStateRunner(&orchestrator.Task{
				State:         "implementation",
				Hooks:         orchestrator.StateHooks{Track: "implementation", Artifacts: "implementation"},
				Validation:    orchestrator.WorkflowValidation{TestRunner: runner},
				PendingRework: &orchestrator.WorkflowRework{FromState: "test_review", Summary: "Planned coverage is inadequate"},
			}, t.TempDir(), "sample")
			if !r.hasQAPendingRework() || r.pendingQAReworkShouldNotBlockPolecat() {
				t.Fatal("green phase verification cannot dismiss Tester coverage rework")
			}
			if msg, reject := r.rejectImplementationNoOpFailure("failure"); !reject || msg == "" {
				t.Fatalf("read-only inspection escaped to planning: reject=%v msg=%q", reject, msg)
			}
			r.attemptFixWork = true
			if _, reject := r.rejectImplementationNoOpFailure("failure"); reject {
				t.Fatal("a real repair attempt may report its blocker")
			}
		})
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

func TestTestRepairGuardsBlockUnchangedBeadAndCompletion(t *testing.T) {
	town, rig := t.TempDir(), "sample"
	rigDir := rigMayorRigDir(town, rig)
	path := filepath.Join(rigDir, "tests", "check.py")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	content := []byte("assert True\n")
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}
	r := newStateRunner(&orchestrator.Task{
		State: "implementation",
		Hooks: orchestrator.StateHooks{CmdGuard: "implementation", Artifacts: "implementation"},
		PendingRework: &orchestrator.WorkflowRework{
			FromState: "test_review",
			TestRepairFiles: map[string]map[string]string{
				"te-repair": {"tests/check.py": fmt.Sprintf("%x", sha256.Sum256(content))},
			},
		},
	}, town, rig)
	for _, cmd := range []string{"bd close te-repair", "bd close te-other te-repair"} {
		if err := r.validateCommand(cmd); err == nil || !strings.Contains(err.Error(), "unchanged") {
			t.Fatalf("%q did not block unchanged repair: %v", cmd, err)
		}
	}
	if err := r.validateCommand("bd close te-repair-extra"); err != nil && strings.Contains(err.Error(), "unchanged") {
		t.Fatalf("different bead matched repair ID prefix: %v", err)
	}
	if err := r.validateArtifacts("success"); err == nil || !strings.Contains(err.Error(), "unchanged") {
		t.Fatalf("explicit success did not block unchanged repair: %v", err)
	}
	if err := os.WriteFile(path, []byte("assert result == expected\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.ValidateTestRepairChanges(rigDir, r.task.PendingRework.TestRepairFiles, "te-repair"); err != nil {
		t.Fatal(err)
	}
}

func TestSummaryClaimsMissingFiles_doesNotRejectExistingRequiredFilesMention(t *testing.T) {
	t.Parallel()
	// Regression: QA confirming files exist while reporting real SPEC defects must NOT be rejected.
	if summaryClaimsMissingFiles("All foundation required files exist and go test ./... passes, but UpdateDeal ignores the SPEC stage-probability rule.") {
		t.Fatal("summary stating required files exist must not trigger missing-files rejection")
	}
	if summaryClaimsMissingFiles("Required files exist on disk, but ListActivities lacks contactID/dealID filters.") {
		t.Fatal("summary stating required files exist on disk must not trigger missing-files rejection")
	}
	// Genuine false missing-file claims still trigger.
	if !summaryClaimsMissingFiles("Required files are missing from accountable/internal/api/") {
		t.Fatal("expected missing required files claim to trigger")
	}
	if !summaryClaimsMissingFiles("missing required file accountable/go.mod") {
		t.Fatal("expected missing required file claim to trigger")
	}
}
