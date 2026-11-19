package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestQAExecutionGuard_BlocksBroadCleanupBeforeExecution(t *testing.T) {
	for _, cmd := range []string{
		`pkill -f "uvicorn app.main:app" && docker compose build`,
		`cd sample/mayor/rig && pkill -f "uvicorn app.main:app" || true`,
		`sudo /usr/bin/pkill uvicorn; curl http://localhost:8000/`,
		`killall uvicorn`,
		`fuser -k 8000/tcp`,
		`pkill -P "$pid" -f uvicorn`,
	} {
		if err := validateQACommand(cmd, "sample", "", orchestrator.WorkflowValidation{}); err == nil || !strings.Contains(err.Error(), "broad process cleanup") {
			t.Errorf("broad QA cleanup accepted: %s (%v)", cmd, err)
		}
	}
	for _, cmd := range []string{
		`docker compose -f product/docker-compose.yml up -d`,
		`grep 'pkill -f' SPEC.md`,
		`fuser 8000/tcp`,
		`pkill -TERM -P "$_gtsrv"`, // harness-owned child cleanup
		`kill "$owned_server_pid"`,
	} {
		if err := rejectBroadQAProcessCleanup(cmd); err != nil {
			t.Errorf("scoped lifecycle/read command rejected: %s: %v", cmd, err)
		}
	}
}

func TestQAExecutionGuard_BlocksPlaywrightSystemDependencyInstall(t *testing.T) {
	for _, cmd := range []string{
		`cd testgt3/mayor/rig && cd linkshelf && npx playwright install --with-deps chromium 2>&1 | tail -10`,
		`npm exec playwright install --with-deps chromium`,
		`pnpm exec playwright install --with-deps chromium`,
		`yarn playwright install --with-deps chromium`,
	} {
		if err := validateQACommand(cmd, "testgt3", "", orchestrator.WorkflowValidation{}); err == nil ||
			!strings.Contains(err.Error(), "must not run Playwright with --with-deps") {
			t.Errorf("Playwright system dependency installation accepted: %s (%v)", cmd, err)
		}
	}

	for _, cmd := range []string{
		`cd testgt3/mayor/rig && cd linkshelf && npx playwright test`,
		`npx playwright install chromium`,
		`grep 'npx playwright install --with-deps chromium' TEST_PLAN.md`,
	} {
		if err := validateQACommand(cmd, "testgt3", "", orchestrator.WorkflowValidation{}); err != nil {
			t.Errorf("safe QA command rejected: %s (%v)", cmd, err)
		}
	}
}

func TestQAExecutionGuard_RecognizesReadOnlySed(t *testing.T) {
	town := t.TempDir()
	cmd := `cd sample/mayor/rig && sed -n '1,20p' sample/main.py`
	if !isQAReadOnlyCommand(cmd) || !isQAFileReadCommand(cmd) {
		t.Fatal("read-only sed was not classified as a source read")
	}
	if err := validateQAReadPath(cmd, town); err != nil {
		t.Fatalf("read-only sed in the project tree was rejected: %v", err)
	}
	if err := validateQAReadPath(`sed -n '1,20p' /etc/passwd`, town); err == nil {
		t.Fatal("read-only sed outside the project tree was accepted")
	}
	if isQAReadOnlyCommand(`sed -i 's|old|new|' sample/main.py`) {
		t.Fatal("in-place sed was classified as read-only")
	}
}

func TestQAExecutionResult_PreservesTargetsWithAndWithoutCommands(t *testing.T) {
	json := `{"outcome":"failure","summary":"b-web is complete; health endpoint is broken","repair_bead_ids":["b-main"],"evidence":"product/main.py GET /api/health returned 500"}`
	for _, response := range []string{json, "CMD: bd list --status=closed\n" + json} {
		outcome, summary, ok := parseOrchestratedResult(response, []string{"failure", "verification_error"})
		report, structured := orchestrator.ParseQAFailureReport(summary)
		if !ok || outcome != "failure" || !structured || strings.Join(report.RepairBeadIDs, ",") != "b-main" || report.Evidence == "" {
			t.Fatalf("lost explicit report: %s %s %v", outcome, summary, ok)
		}
	}
	outcome, _, ok := parseOrchestratedResult(`{"outcome":"verification_error","summary":"pkill: Operation not permitted; checks never ran"}`, []string{"failure", "verification_error"})
	if !ok || outcome != "verification_error" {
		t.Fatal("verification error was not accepted as a distinct outcome")
	}
	if err := validateQAArtifacts("", "sample", outcome, true, false, false, false, false, orchestrator.WorkflowValidation{}); err != nil {
		t.Fatalf("failed execution required success artifacts: %v", err)
	}
	if err := validateQAArtifacts("", "sample", "all_passed", true, false, false, false, false, orchestrator.WorkflowValidation{}); err == nil {
		t.Fatal("execution error weakened approval gates")
	}
}

func TestQAExecutionRetry_DropsVerificationButPreservesReviewProgress(t *testing.T) {
	town, rig := t.TempDir(), "sample"
	p := newQAReviewProgress("wf-qa", "qa_review", rig)
	for _, milestone := range []string{qaMilestoneUnittest, qaMilestoneRuntimeSmoke, qaMilestoneClosedBeads, qaMilestoneSpecRead} {
		p.mark(milestone)
	}
	if err := saveQAReviewProgress(town, rig, p); err != nil {
		t.Fatal(err)
	}
	r := newStateRunner(&orchestrator.Task{
		WorkflowID: "wf-qa", State: "qa_review", Hooks: orchestrator.StateHooks{Track: "qa", VerificationErrorLimit: 3},
		PendingRework: &orchestrator.WorkflowRework{FromState: "qa_review", Outcome: "verification_error"},
	}, town, rig)
	r.initQAReviewProgress()
	if r.track.unittestOK || r.track.qaSmokeOK || r.qaProgress.done(qaMilestoneUnittest) || r.qaProgress.done(qaMilestoneRuntimeSmoke) {
		t.Fatal("execution retry restored stale verification credit")
	}
	if !r.qaProgress.done(qaMilestoneSpecRead) || !r.qaProgress.done(qaMilestoneClosedBeads) {
		t.Fatal("execution retry discarded unrelated review progress")
	}
	if _, err := os.Stat(filepath.Join(town, rig, "qa", "qa-review-progress.json")); err != nil {
		t.Fatal(err)
	}
}
