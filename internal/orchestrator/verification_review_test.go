package orchestrator

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const qaCleanupFailure = `Verify chain aborted before any HTTP probe ran — not an application failure. pkill -f "uvicorn app.main:app" hit pid 1600309 with Operation not permitted, then the whole CMD was terminated by signal before docker compose or curls. No bead reopened work is warranted: all implementation beads remain closed (b-main, b-web). Prior manual smoke passed pytest 4 passed and GET /api/health. Drop the cross-session pkill and run the configured compose build/up and curls directly.`

func TestValidateQAFailureReportFields(t *testing.T) {
	for _, tt := range []struct {
		name, summary, missing string
	}{
		{"unstructured", "defect found", "summary"},
		{"missing targets", `{"summary":"defect","evidence":"observed failure"}`, "repair_bead_ids"},
		{"empty target", `{"summary":"defect","repair_bead_ids":[" "],"evidence":"observed failure"}`, "empty IDs"},
		{"missing evidence", `{"summary":"evidence in prose","repair_bead_ids":["b-main"]}`, "evidence"},
		{"whitespace evidence", `{"summary":"defect","repair_bead_ids":["b-main"],"evidence":" "}`, "evidence"},
		{"finding summary", `{"summary":"two defects","findings":[{"repair_bead_ids":["b-main"],"evidence":"observed failure"}]}`, "findings[0].summary"},
		{"finding beads", `{"summary":"two defects","findings":[{"summary":"bad route","evidence":"observed failure"}]}`, "findings[0].repair_bead_ids"},
		{"finding evidence", `{"summary":"two defects","findings":[{"summary":"bad route","repair_bead_ids":["b-main"],"evidence":"observed route"},{"summary":"bad status","repair_bead_ids":["b-web"]}]}`, "findings[1].evidence"},
		{"complete findings", `{"summary":"two defects","findings":[{"summary":"bad route","repair_bead_ids":["b-main"],"evidence":"observed route"},{"summary":"bad status","repair_bead_ids":["b-web"],"evidence":"observed status"}]}`, ""},
		{"findings inherit top-level fields", `{"summary":"two defects","repair_bead_ids":["b-main"],"evidence":"observed failure","findings":[{"summary":"bad route"},{"summary":"bad status"}]}`, ""},
		{"complete", `{"summary":"defect","repair_bead_ids":["b-main"],"evidence":"observed failure"}`, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateQAFailureReportFields(tt.summary)
			if (err == nil) != (tt.missing == "") || (err != nil && !strings.Contains(err.Error(), tt.missing)) {
				t.Fatalf("error=%v, want %q", err, tt.missing)
			}
		})
	}
}

func TestQAFailureRepairBeadIDs_flattensAllFindings(t *testing.T) {
	report := QAFailureReport{
		Summary:       "two defects",
		RepairBeadIDs: []string{"b-main"},
		Findings: []QAFinding{
			{Summary: "bad route", RepairBeadIDs: []string{"b-web"}},
			{Summary: "bad status", RepairBeadIDs: []string{"b-main", " b-web "}},
		},
	}
	if got := strings.Join(qaFailureRepairBeadIDs(report), ","); got != "b-main,b-web" {
		t.Fatalf("repair IDs = %q", got)
	}
}

func verificationReviewFixture(t *testing.T) (*Manager, string, WorkflowValidation, string, string) {
	t.Helper()
	town, rig := t.TempDir(), "sample"
	v := WorkflowValidation{
		LayoutRoot: "product", BeadTitleContains: "Implement product/", ActivePhaseIDField: "foundation",
		DeliveryPhases: []DeliveryPhase{
			{ID: "foundation", RequiredFiles: []string{"product/main.py", "product/web/app.js"}, QAVerifyCommand: "docker compose -f product/docker-compose.yml build"},
			{ID: "future", RequiredFiles: []string{"product/worker.py"}},
		},
	}
	v.RequiredFiles = v.UnionRequiredFiles()
	for _, file := range v.RequiredFiles {
		recoveryFile(t, filepath.Join(town, rig, "mayor", "rig"), file, "preserve delivered content\n")
	}
	m := NewManager(town)
	m.LoadTemplate(&WorkflowTemplate{ID: "qa-errors", InitialState: "qa_review", Validation: v, States: map[string]State{
		"qa_review": {Role: "qa", Hooks: StateHooks{VerificationErrorLimit: 3}, Transitions: map[string]Transition{
			"failure": {To: "implementation"}, "architecture_failure": {To: "design"}, "verification_error": {To: "qa_review"}, "timeout": {To: "qa_review"}, "all_passed": {To: "done"},
		}},
		"implementation": {Role: "polecat"}, "design": {Role: "architect"}, "done": {},
	}})
	id, err := m.StartWorkflow("qa-errors", map[string]string{"rig": rig})
	if err != nil {
		t.Fatal(err)
	}
	return m, id, v, town, rig
}

func TestVerificationReview_ExecutionErrorNeverReturnsToImplementation(t *testing.T) {
	for _, outcome := range []string{"failure", "verification_error", "timeout", "architecture_failure"} {
		t.Run(outcome, func(t *testing.T) {
			m, id, _, town, rig := verificationReviewFixture(t)
			setListImplementBeadsByStatusHook(t, town, rig, func(_, _ string, _ WorkflowValidation, _ string) ([]PlanBead, error) {
				t.Fatal("execution failure must not select implementation repair targets")
				return nil, nil
			})
			next, err := m.CompleteTask(id, outcome, "", qaCleanupFailure, "inventory: b-main b-web", nil)
			if err != nil || next != "qa_review" {
				t.Fatalf("next=%s err=%v", next, err)
			}
			inst := m.instances[id]
			if inst.VerificationErrors["qa_review"] != 1 || inst.PendingRework == nil || inst.PendingRework.Outcome != "verification_error" || !strings.Contains(inst.PendingRework.Feedback, "docker compose") {
				t.Fatalf("lost execution diagnostics/retry: %+v", inst)
			}
			if ids, err := ReopenImplementationBeadsAfterQAFailure(town, rig, m.templates["qa-errors"].Validation, qaCleanupFailure); err != nil || len(ids) != 0 {
				t.Fatalf("execution prose reopened work: %v %v", ids, err)
			}
			for _, path := range []string{"product/main.py", "product/web/app.js"} {
				data, err := os.ReadFile(filepath.Join(town, rig, "mayor", "rig", path))
				if err != nil || string(data) != "preserve delivered content\n" {
					t.Fatalf("delivered work changed: %s %v", data, err)
				}
			}
		})
	}
}

func TestVerificationReview_BoundedAcrossRestartsAndResetsOnSuccess(t *testing.T) {
	m, id, _, town, _ := verificationReviewFixture(t)
	template := m.templates["qa-errors"]
	for count := 1; count <= 3; count++ {
		if next, err := m.CompleteTask(id, "failure", "", qaCleanupFailure, "", nil); err != nil || next != "qa_review" {
			t.Fatalf("retry %d: %s %v", count, next, err)
		}
		m = NewManager(town)
		m.LoadTemplate(template)
		inst := m.instances[id]
		if inst.VerificationErrors["qa_review"] != count || (inst.Status == "paused") != (count == 3) {
			t.Fatalf("retry budget not durable: %+v", inst)
		}
	}
	if !strings.Contains(m.instances[id].PauseReason, "pkill") || !strings.Contains(m.instances[id].PauseReason, "3") {
		t.Fatalf("pause lacks actual error: %s", m.instances[id].PauseReason)
	}
	if _, err := m.CompleteTask(id, "all_passed", "", "", "", nil); !errors.Is(err, ErrWorkflowPaused) {
		t.Fatalf("paused workflow accepted a verdict: %v", err)
	}
	if err := m.ResumeWorkflow(id); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CompleteTask(id, "all_passed", "", "configured verification and review passed", "", nil); err != nil {
		t.Fatal(err)
	}
	if m.instances[id].VerificationErrors["qa_review"] != 0 {
		t.Fatal("successful review did not reset retry budget")
	}
}

func TestVerificationReview_WallClockTimeoutStaysInQAAndIsBounded(t *testing.T) {
	m, id, _, _, _ := verificationReviewFixture(t)
	for count := 1; count <= 3; count++ {
		if next, err := m.applyStateTimeout(id); err != nil || next != "qa_review" {
			t.Fatalf("timeout %d: %s %v", count, next, err)
		}
	}
	if m.instances[id].Status != "paused" || m.instances[id].PendingRework.Outcome != "verification_error" {
		t.Fatalf("wall-clock retries unbounded: %+v", m.instances[id])
	}
}

func TestVerificationReview_OnlyExplicitDefectTargetsAreReopened(t *testing.T) {
	m, id, v, town, rig := verificationReviewFixture(t)
	status := map[string]string{"b-main": "closed", "b-web": "closed", "b-future": "closed"}
	beads := []PlanBead{{ID: "b-main", Title: "Implement product/main.py per architecture"}, {ID: "b-web", Title: "Implement product/web/app.js per architecture"}, {ID: "b-future", Title: "Implement product/worker.py per architecture"}}
	setListImplementBeadsByStatusHook(t, town, rig, func(_, _ string, _ WorkflowValidation, wanted string) ([]PlanBead, error) {
		var out []PlanBead
		for _, bead := range beads {
			if status[bead.ID] == wanted {
				out = append(out, bead)
			}
		}
		return out, nil
	})
	previous := bdUpdateImplementBeadStatusHook
	bdUpdateImplementBeadStatusHook = func(_, _, id, value string) error { status[id] = value; return nil }
	t.Cleanup(func() { bdUpdateImplementBeadStatusHook = previous })
	report := QAFailureReport{Summary: "Health response is wrong. b-web and b-future remain complete; do not reopen them.", RepairBeadIDs: []string{"b-main"}, Evidence: "product/main.py: GET /api/health returned 500 instead of 200"}
	data, _ := json.Marshal(report)
	m.instances[id].VerificationErrors = map[string]int{"qa_review": 2}
	if next, err := m.CompleteTask(id, "failure", "", string(data), "bd list output: b-main b-web b-future; old command not found noise", nil); err != nil || next != "implementation" {
		t.Fatalf("real defect did not route to repair: %s %v", next, err)
	}
	if status["b-main"] != "open" || status["b-web"] != "closed" || status["b-future"] != "closed" {
		t.Fatalf("incidental IDs became repair targets: %v", status)
	}
	if m.instances[id].VerificationErrors["qa_review"] != 0 {
		t.Fatal("valid defect report did not reset execution error count")
	}
	scope := QAReworkWriteScopeFromTransition(town, rig, "qa_review", "implementation", string(data))
	if !scope.BeadCited("b-main") || scope.BeadCited("b-web") || scope.BeadCited("b-future") {
		t.Fatalf("repair write scope ignored explicit targets: %+v", scope)
	}
	for _, invalid := range []QAFailureReport{
		{Summary: report.Summary},
		{Summary: report.Summary, RepairBeadIDs: []string{"b-web"}},
		{Summary: report.Summary, RepairBeadIDs: []string{"b-future"}, Evidence: "future-phase file"},
		{Summary: report.Summary, RepairBeadIDs: []string{"b-unknown"}, Evidence: "unknown owner"},
	} {
		data, _ := json.Marshal(invalid)
		if got, _ := normalizeVerificationReviewResult(town, rig, v, "failure", string(data)); got != "verification_error" {
			t.Fatalf("invalid repair report accepted: %+v", invalid)
		}
	}
}

func TestVerificationReview_MultiFindingReportReopensAllOwners(t *testing.T) {
	m, id, _, town, rig := verificationReviewFixture(t)
	status := map[string]string{"b-main": "closed", "b-web": "closed"}
	beads := []PlanBead{{ID: "b-main", Title: "Implement product/main.py per architecture"}, {ID: "b-web", Title: "Implement product/web/app.js per architecture"}}
	setListImplementBeadsByStatusHook(t, town, rig, func(_, _ string, _ WorkflowValidation, wanted string) ([]PlanBead, error) {
		var out []PlanBead
		for _, bead := range beads {
			if status[bead.ID] == wanted {
				out = append(out, bead)
			}
		}
		return out, nil
	})
	previous := bdUpdateImplementBeadStatusHook
	bdUpdateImplementBeadStatusHook = func(_, _, id, value string) error { status[id] = value; return nil }
	t.Cleanup(func() { bdUpdateImplementBeadStatusHook = previous })
	report := QAFailureReport{Summary: "Stage 1: two active-phase defects", Findings: []QAFinding{
		{Summary: "bad status value", RepairBeadIDs: []string{"b-main"}, Evidence: "main.py accepts in_progress"},
		{Summary: "out-of-scope route", RepairBeadIDs: []string{"b-web"}, Evidence: "app.js calls forbidden route", Paths: []string{"product/web/app.js"}},
	}}
	data, _ := json.Marshal(report)
	if next, err := m.CompleteTask(id, "failure", "", string(data), "", nil); err != nil || next != "implementation" {
		t.Fatalf("multi-finding defect did not route to repair: %s %v", next, err)
	}
	if status["b-main"] != "open" || status["b-web"] != "open" {
		t.Fatalf("not all finding owners reopened: %v", status)
	}
	scope := QAReworkWriteScopeFromTransition(town, rig, "qa_review", "implementation", string(data))
	if !scope.BeadCited("b-main") || !scope.BeadCited("b-web") {
		t.Fatalf("repair write scope dropped finding owners: %+v", scope)
	}
	summary := m.instances[id].PendingRework.Summary
	for _, want := range []string{"bad status value", "out-of-scope route"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary lost finding %q: %q", want, summary)
		}
	}
}

func TestVerificationReview_AcceptsCompletedPhaseRepairBead(t *testing.T) {
	town, rig := t.TempDir(), "sample"
	v := WorkflowValidation{
		LayoutRoot: "product", BeadTitleContains: "Implement product/", ActivePhaseIDField: "integration",
		DeliveryPhases: []DeliveryPhase{
			{ID: "foundation", RequiredFiles: []string{"product/main.py"}},
			{ID: "integration", RequiredFiles: []string{"product/market/provider.py"}},
			{ID: "future", RequiredFiles: []string{"product/frontend/page.tsx"}},
		},
	}
	beads := []PlanBead{
		{ID: "b-main", Title: "Implement product/main.py per architecture"},
		{ID: "b-provider", Title: "Implement product/market/provider.py per architecture"},
		{ID: "b-future", Title: "Implement product/frontend/page.tsx per architecture"},
	}
	setListImplementBeadsByStatusHook(t, town, rig, func(_, _ string, _ WorkflowValidation, status string) ([]PlanBead, error) {
		if status == "closed" {
			return beads, nil
		}
		return nil, nil
	})
	for _, beadID := range []string{"b-main", "b-provider"} {
		report, err := json.Marshal(QAFailureReport{
			Summary:       "Integration defect requires a focused repair.",
			RepairBeadIDs: []string{beadID},
			Evidence:      "The running application does not use this implementation.",
		})
		if err != nil {
			t.Fatal(err)
		}
		if outcome, _ := normalizeVerificationReviewResult(town, rig, v, "failure", string(report)); outcome != "failure" {
			t.Fatalf("current/completed phase bead %s was rejected as %q", beadID, outcome)
		}
	}
	futureReport, err := json.Marshal(QAFailureReport{
		Summary:       "Future phase work is incomplete.",
		RepairBeadIDs: []string{"b-future"},
		Evidence:      "The frontend has not been built.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if outcome, _ := normalizeVerificationReviewResult(town, rig, v, "failure", string(futureReport)); outcome != "verification_error" {
		t.Fatalf("future-phase repair target was accepted as %q", outcome)
	}
}

func TestVerificationReview_RewindsToCompletedPhaseRepairOwner(t *testing.T) {
	townRoot, rig, rigDir, beadsDir := setupPhasedRigTown(t)
	writeTestPhasedProfile(t, townRoot, rig, "backend")
	v, ok, err := LoadRigWorkflowProfileFile(townRoot, rig)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if _, err := SyncPlanningArtifacts(townRoot, rig, v.ForActivePhase(), true); err != nil {
		t.Fatal(err)
	}
	for _, rel := range v.ForActivePhase().RequiredFiles {
		writeBackendFileContent(t, rigDir, rel)
	}
	closeAllOpenImplementBeads(t, townRoot, rig, beadsDir, rigDir, v.ForActivePhase())

	if err := SetRigActivePhase(townRoot, rig, "frontend"); err != nil {
		t.Fatal(err)
	}
	if err := AddRigCompletedPhase(townRoot, rig, "backend"); err != nil {
		t.Fatal(err)
	}
	v, ok, err = LoadRigWorkflowProfileFile(townRoot, rig)
	if err != nil || !ok {
		t.Fatal(err)
	}
	var repairBead string
	for _, bead := range mustListImplementBeads(t, townRoot, rig, v, "closed") {
		path := NormalizePlannerBeadPath(ExtractPathFromBeadTitle(bead.Title, v.BeadTitleContains), v.LayoutRoot, rig)
		if path == "linkshelf/internal/store/store.go" {
			repairBead = bead.ID
			break
		}
	}
	if repairBead == "" {
		t.Fatal("closed backend implementation bead not found")
	}

	manager, workflowID := loadPhasedManager(t, townRoot, rig)
	report, err := json.Marshal(QAFailureReport{
		Summary:       "The API integration is incomplete because the backend store path is not wired.",
		RepairBeadIDs: []string{repairBead},
		Evidence:      "The running application does not use the store implementation.",
	})
	if err != nil {
		t.Fatal(err)
	}
	next, err := manager.CompleteTask(workflowID, "failure", "", string(report), "", nil)
	if err != nil || next != "implementation" {
		t.Fatalf("QA failure transition = %q, err=%v", next, err)
	}
	assertActivePhase(t, townRoot, rig, "backend")
	reloaded, ok, err := LoadRigWorkflowProfileFile(townRoot, rig)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if !ImplementBeadIsStillOpen(townRoot, rig, repairBead, reloaded.ForActivePhase()) {
		t.Fatalf("QA repair bead %s was not reopened in its owning phase", repairBead)
	}
	if pending := manager.instances[workflowID].PendingRework; pending == nil || !strings.Contains(pending.Feedback, "rewound active phase") {
		t.Fatalf("phase rewind was not included in repair feedback: %+v", pending)
	}
}

func TestVerificationReviewWorkflowConfiguration(t *testing.T) {
	for _, name := range []string{"rig-flow", "req-flow"} {
		data, err := os.ReadFile(filepath.Join("town", "templates", name+".yaml"))
		if err != nil {
			t.Fatal(err)
		}
		var template WorkflowTemplate
		if err := yaml.Unmarshal(data, &template); err != nil {
			t.Fatal(err)
		}
		qa := template.States["qa_review"]
		if qa.Hooks.VerificationErrorLimit != 3 || qa.Transitions["verification_error"].To != "qa_review" || qa.Transitions["timeout"].To != "qa_review" || qa.Transitions["failure"].To != "implementation" {
			t.Fatalf("%s: incorrect review routing: %+v", name, qa)
		}
	}
}

func mustListImplementBeads(t *testing.T, townRoot, rig string, v WorkflowValidation, status string) []PlanBead {
	t.Helper()
	beads, err := listImplementBeadsForGuard(townRoot, rig, v, status)
	if err != nil {
		t.Fatal(err)
	}
	return beads
}
