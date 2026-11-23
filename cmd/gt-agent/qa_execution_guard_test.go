package main

import (
	"context"
	"encoding/json"
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

func TestQAExecutionResult_RequestsMissingEvidenceBeforeSubmitting(t *testing.T) {
	for _, tc := range []struct {
		name         string
		withCommands bool
		complete     bool
	}{
		{name: "json-only"},
		{name: "with-commands", withCommands: true},
		{name: "complete-with-commands", withCommands: true, complete: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			town, rig := t.TempDir(), "sample"
			dir := rigMayorRigDir(town, rig)
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "SPEC.md"), []byte("fixture"), 0644); err != nil {
				t.Fatal(err)
			}
			incomplete := `{"outcome":"failure","summary":"Evidence: main.py lacks task routes","repair_bead_ids":["b-main"]}`
			complete := `{"outcome":"failure","summary":"Task routes missing","repair_bead_ids":["b-main"],"evidence":"main.py has no task routes"}`
			var responses []string
			if tc.complete {
				responses = []string{"CMD: cat sample/mayor/rig/SPEC.md\n" + complete}
			} else if tc.withCommands {
				responses = []string{"CMD: cat sample/mayor/rig/SPEC.md\n" + incomplete, complete}
			} else {
				responses = []string{"CMD: cat sample/mayor/rig/SPEC.md", incomplete, complete}
			}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				var body struct {
					Messages []struct {
						Content string `json:"content"`
					} `json:"messages"`
				}
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
					t.Errorf("decode request: %v", err)
				}
				if !tc.complete && calls == len(responses)-1 {
					last := body.Messages[len(body.Messages)-1].Content
					if !strings.Contains(last, "evidence") || !strings.Contains(last, "repair_bead_ids") {
						t.Errorf("missing report correction guidance: %q", last)
					}
				}
				if calls >= len(responses) {
					t.Error("unexpected extra request")
					http.Error(w, "unexpected request", http.StatusInternalServerError)
					return
				}
				content := responses[calls]
				calls++
				_ = json.NewEncoder(w).Encode(map[string]any{
					"choices": []any{map[string]any{"message": map[string]string{"content": content}}},
				})
			}))
			defer server.Close()
			task := &orchestrator.Task{
				WorkflowID: "wf-qa-fields", State: "qa-fixture", Rig: rig,
				Hooks:           orchestrator.StateHooks{Track: "qa", VerificationErrorLimit: 3, MaxCmdTurns: 4, RequireExplicitOutcome: true},
				AllowedOutcomes: []string{"failure", "verification_error"},
			}
			budgetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Write([]byte(`{"accounted_cost_usd":0,"rig_accounted_cost_usd":0,"accounting":"no completed priced calls","dollar_coverage_complete":true}`))
			}))
			defer budgetServer.Close()
			t.Setenv("GT_BUDGET_STATUS_URL", budgetServer.URL+"/api/budgets")
			client := llm.NewClient(server.URL, "test", "qa", time.Second)
			outcome, summary, _, err := executeOrchestratedTask(context.Background(), client, town, rig, "", task, nil)
			report, ok := orchestrator.ParseQAFailureReport(summary)
			if err != nil || outcome != "failure" || !ok || report.Evidence != "main.py has no task routes" || calls != len(responses) {
				t.Fatalf("outcome=%q report=%+v calls=%d err=%v", outcome, report, calls, err)
			}
		})
	}
}

func TestQAExecutionResult_IncompleteReportExhaustsTurns(t *testing.T) {
	town, rig := t.TempDir(), "sample"
	dir := rigMayorRigDir(town, rig)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SPEC.md"), []byte("fixture"), 0644); err != nil {
		t.Fatal(err)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]string{
				"content": `{"outcome":"failure","summary":"Task routes missing","repair_bead_ids":["b-main"]}`,
			}}},
		})
	}))
	defer server.Close()
	task := &orchestrator.Task{
		WorkflowID: "wf-qa-fields-exhausted", State: "qa-fixture", Rig: rig,
		Hooks:           orchestrator.StateHooks{Track: "qa", VerificationErrorLimit: 3, MaxCmdTurns: 2},
		AllowedOutcomes: []string{"failure", "verification_error"},
	}
	budgetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"accounted_cost_usd":0,"rig_accounted_cost_usd":0,"accounting":"no completed priced calls","dollar_coverage_complete":true}`))
	}))
	defer budgetServer.Close()
	t.Setenv("GT_BUDGET_STATUS_URL", budgetServer.URL+"/api/budgets")
	client := llm.NewClient(server.URL, "test", "qa", time.Second)
	outcome, summary, feedback, err := executeOrchestratedTask(context.Background(), client, town, rig, "", task, nil)
	if err == nil || outcome != "fail" || calls != 2 || !strings.Contains(feedback, "evidence") ||
		!strings.Contains(summary, "Task routes missing") || orchestrator.ValidateQAFailureReportFields(summary) == nil {
		t.Fatalf("outcome=%q summary=%q feedback=%q calls=%d err=%v", outcome, summary, feedback, calls, err)
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
