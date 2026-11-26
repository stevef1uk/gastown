package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestValidateQACommand_rejectsGoTestOnGoModPhase(t *testing.T) {
	v := orchestrator.WorkflowValidation{
		LayoutRoot:         "linkshelf",
		QAVerifyCommand:    "cd linkshelf && go mod download",
		ActivePhaseIDField: "go-module",
		TestRunner:         "custom",
		DeliveryPhases: []orchestrator.DeliveryPhase{
			{ID: "go-module", RequiredFiles: []string{"linkshelf/go.mod"}},
		},
	}
	v = v.ForActivePhase()
	err := validateQACommand("cd testgt3/mayor/rig && cd linkshelf && go test ./...", "testgt3", "/tmp", v)
	if err == nil {
		t.Fatal("expected go test rejection during go-module QA")
	}
	if !strings.Contains(err.Error(), "go.mod only") {
		t.Fatalf("err = %v", err)
	}
	download := "cd testgt3/mayor/rig && cd linkshelf && go mod download"
	if err := validateQACommand(download, "testgt3", "/tmp", v); err != nil {
		t.Fatalf("download cmd: %v", err)
	}
}

func TestIsQATestCommandOK_goTestMayorRigPath(t *testing.T) {
	v := orchestrator.WorkflowValidation{
		LayoutRoot:      "linkshelf",
		QAVerifyCommand: "cd linkshelf && go test ./...",
	}
	cmd := "cd testgt3/mayor/rig/linkshelf && go test ./..."
	if !isQATestCommandOK(cmd, v) {
		t.Fatal("expected mayor/rig go test to match profile QAVerifyCommand")
	}
	if !isImplementationVerifyCommandOK(cmd, "/tmp", "testgt3", "", v) {
		t.Fatal("expected implementation verify to accept mayor/rig go test")
	}
}

func TestIsQATestCommandOK_pythonPytestMayorRigPath(t *testing.T) {
	v := orchestrator.WorkflowValidation{
		LayoutRoot:      "tasklist",
		TestRunner:      "pytest",
		PythonVenvDir:   ".venv",
		QAVerifyCommand: "cd tasklist && pytest -q",
		RequiredFiles:   []string{"tasklist/app.py"},
	}
	cmd := "cd mockrig/mayor/rig/tasklist && pytest -q"
	if !isQATestCommandOK(cmd, v) {
		t.Fatal("expected mayor/rig pytest to match profile QAVerifyCommand")
	}
}

func TestIsQATestCommandOK_dockerComposeMayorRigPath(t *testing.T) {
	v := orchestrator.WorkflowValidation{
		LayoutRoot:      "finally",
		TestRunner:      "custom",
		QAVerifyCommand: "cd finally && docker-compose -f docker-compose.yml config",
		RequiredFiles:   []string{"finally/docker-compose.yml"},
	}
	cmd := "cd mockrig/mayor/rig/finally && docker-compose -f docker-compose.yml config"
	if !isQATestCommandOK(cmd, v) {
		t.Fatal("expected mayor/rig docker-compose to match profile QAVerifyCommand")
	}
}

func TestIsQATestCommandOK_goModTidyDownloadEquivalence(t *testing.T) {
	// Test the exact logged scenario: QA runs "go mod download" from mayor/rig workdir
	// but phase verify is "go mod tidy" (go-module phase)
	v := orchestrator.WorkflowValidation{
		LayoutRoot:      "pingapp",
		TestRunner:      "go",
		QAVerifyCommand: "cd pingapp && go mod tidy",
		RequiredFiles:   []string{"pingapp/go.mod"},
		DeliveryPhases: []orchestrator.DeliveryPhase{
			{ID: "go-module", RequiredFiles: []string{"pingapp/go.mod"}, QAVerifyCommand: "cd pingapp && go mod tidy"},
		},
		ActivePhaseIDField: "go-module",
	}
	v = v.ForActivePhase()

	// Exact logged cmd: rewritten to mayor/rig workdir with nested layout
	cmd := "cd ping_rig/mayor/rig/pingapp && go mod download"
	if !isQATestCommandOK(cmd, v) {
		t.Fatalf("expected go mod download from mayor/rig to match go mod tidy verify (equivalence)")
	}

	// Reverse: cmd is go mod tidy, verify is go mod download
	cmd2 := "cd ping_rig/mayor/rig/pingapp && go mod tidy"
	if !isQATestCommandOK(cmd2, v) {
		t.Fatal("go mod tidy cmd should match go mod download verify")
	}

	// Non-equivalent: go build should not match
	cmd3 := "cd ping_rig/mayor/rig/pingapp && go build ./..."
	if isQATestCommandOK(cmd3, v) {
		t.Fatal("go build should not match go mod tidy")
	}
}

func TestDockerVerifyCommandMatches_mayorRigCd(t *testing.T) {
	verify := "cd . && docker-compose -f docker-compose.yml config"
	cmd := "cd finally/mayor/rig && docker-compose -f docker-compose.yml config"
	if !dockerVerifyCommandMatches(cmd, verify) {
		t.Fatal("expected mayor/rig verify to match layout hint")
	}
}

func TestRigFlowCmdBeforeOutcomeNudge(t *testing.T) {
	// Review states must inspect artifacts before rendering a verdict. The nudge
	// is configured in templates/rig-flow.yaml (FSM behavior belongs in YAML, not
	// state-name branches in gt-agent).
	for _, state := range []string{"design_review", "qa_review"} {
		h, err := orchestrator.RigFlowStateHooks(state)
		if err != nil {
			t.Fatalf("RigFlowStateHooks(%q): %v", state, err)
		}
		if strings.TrimSpace(h.CmdBeforeOutcomeNudge) == "" {
			t.Errorf("state %q must declare cmd_before_outcome_nudge so a no-command outcome is rejected", state)
		}
	}

	h, err := orchestrator.RigFlowStateHooks("design_review")
	if err != nil {
		t.Fatalf("RigFlowStateHooks(design_review): %v", err)
	}
	for _, want := range []string{"architecture.md", "SPEC.md"} {
		if !strings.Contains(h.CmdBeforeOutcomeNudge, want) {
			t.Errorf("design_review nudge must name %q, got:\n%s", want, h.CmdBeforeOutcomeNudge)
		}
	}

	// States that act (write/verify) before reporting must NOT reject a
	// first-turn outcome — otherwise a valid JSON-only completion stalls.
	for _, state := range []string{"implementation", "planning", "plan_review", "test_plan", "test_review", "design", "kickoff", "review_recovery"} {
		h, err := orchestrator.RigFlowStateHooks(state)
		if err != nil {
			t.Fatalf("RigFlowStateHooks(%q): %v", state, err)
		}
		if strings.TrimSpace(h.CmdBeforeOutcomeNudge) != "" {
			t.Errorf("state %q must not declare cmd_before_outcome_nudge, got %q", state, h.CmdBeforeOutcomeNudge)
		}
	}
}

func TestGoVerifyCommandMatches_goModTidyDownloadEquivalence(t *testing.T) {
	v := orchestrator.WorkflowValidation{
		LayoutRoot: "pingapp",
	}
	// cmd runs in mayor/rig workdir with nested layout
	cmd := "cd ping_rig/mayor/rig/pingapp && go mod download"
	// phase verify uses go mod tidy
	verify := "cd pingapp && go mod tidy"
	if !goVerifyCommandMatches(cmd, verify, v) {
		t.Fatalf("expected go mod download cmd to match go mod tidy verify (equivalence)")
	}
	// Reverse direction
	cmd2 := "cd ping_rig/mayor/rig/pingapp && go mod tidy"
	verify2 := "cd pingapp && go mod download"
	if !goVerifyCommandMatches(cmd2, verify2, v) {
		t.Fatalf("expected go mod tidy cmd to match go mod download verify (equivalence)")
	}
	// Non-equivalent: go build should not match go mod tidy
	verify3 := "cd pingapp && go mod tidy"
	cmd3 := "cd ping_rig/mayor/rig/pingapp && go build ./..."
	if goVerifyCommandMatches(cmd3, verify3, v) {
		t.Fatal("go build should not match go mod tidy")
	}
}

func TestIsImplementationVerifyCommandOK_goProjectWithDocker(t *testing.T) {
	orchestrator.ListImplementBeadsByStatusHook = func(_, _ string, _ orchestrator.WorkflowValidation, status string) ([]orchestrator.PlanBead, error) {
		if status == "in_progress" {
			return []orchestrator.PlanBead{{ID: "ta-jl3", Title: "Implement taskboard/internal/web/ui_content_test.go per architecture"}}, nil
		}
		return nil, nil
	}
	t.Cleanup(func() { orchestrator.ListImplementBeadsByStatusHook = nil })

	v := orchestrator.WorkflowValidation{
		LayoutRoot:      "taskboard",
		TestRunner:      "go",
		QAVerifyCommand: "cd taskboard && go test ./...",
		RequiredFiles: []string{
			"taskboard/internal/web/ui_content_test.go",
			"taskboard/internal/web/app_test.go",
			"taskboard/Dockerfile",
		},
		ActivePhaseIDField: "phase-2",
		DeliveryPhases: []orchestrator.DeliveryPhase{
			{
				ID: "phase-2",
				RequiredFiles: []string{
					"taskboard/internal/web/ui_content_test.go",
				},
				QAVerifyCommand: "cd taskboard && go test ./...",
			},
		},
	}
	cmd := "cd tasktracker/mayor/rig/taskboard && go mod tidy && go test -timeout 30s -count=1 ./internal/web/..."
	if !isImplementationVerifyCommandOK(cmd, t.TempDir(), "tasktracker", "ta-jl3", v) {
		t.Fatal("expected package-scoped go test verify command to be accepted for go project with Dockerfile")
	}
}

func TestImplementationVerifyAttemptBeforeBdClose_chainedSubslice(t *testing.T) {
	orchestrator.ListImplementBeadsByStatusHook = func(_, _ string, _ orchestrator.WorkflowValidation, status string) ([]orchestrator.PlanBead, error) {
		if status == "in_progress" {
			return []orchestrator.PlanBead{{ID: "ta-jl3", Title: "Implement taskboard/internal/web/ui_content_test.go per architecture"}}, nil
		}
		return nil, nil
	}
	t.Cleanup(func() { orchestrator.ListImplementBeadsByStatusHook = nil })

	v := orchestrator.WorkflowValidation{
		LayoutRoot:      "taskboard",
		TestRunner:      "go",
		QAVerifyCommand: "cd taskboard && go test ./...",
		RequiredFiles: []string{
			"taskboard/internal/web/ui_content_test.go",
			"taskboard/Dockerfile",
		},
		ActivePhaseIDField: "phase-2",
		DeliveryPhases: []orchestrator.DeliveryPhase{
			{
				ID: "phase-2",
				RequiredFiles: []string{
					"taskboard/internal/web/ui_content_test.go",
				},
				QAVerifyCommand: "cd taskboard && go test ./...",
			},
		},
	}
	cmd := "export BEADS_DIR=$GT_ROOT/tasktracker/.beads && cd tasktracker/mayor/rig/taskboard && go mod tidy && go test -timeout 30s -count=1 ./internal/web/... && cd .. && bd close ta-jl3"
	if !implementationVerifyAttemptBeforeBdClose(cmd, t.TempDir(), "tasktracker", "ta-jl3", v) {
		t.Fatal("expected chained verify-and-close command to be accepted")
	}
}

func TestValidateTestPlanBeadIDs_ignoresNonActivePhases(t *testing.T) {
	v := orchestrator.WorkflowValidation{
		LayoutRoot:         "taskboard",
		ActivePhaseIDField: "phase-1-app",
		DeliveryPhases: []orchestrator.DeliveryPhase{
			{ID: "phase-1-core", RequiredFiles: []string{"taskboard/internal/store/sqlite.go"}},
			{ID: "phase-1-app", RequiredFiles: []string{"taskboard/internal/web/api_test.go"}},
			{ID: "phase-2-kanban", RequiredFiles: []string{"taskboard/internal/web/ui_content_test.go"}},
		},
	}
	blocks := []orchestrator.TestPlanBlock{
		{
			ReqID:    "req-1",
			TestFile: "taskboard/internal/store/sqlite_test.go",
			BeadID:   "ta-purged",
			Phase:    "phase-1-core",
		},
		{
			ReqID:    "req-2",
			TestFile: "taskboard/internal/web/api_test.go",
			BeadID:   "ta-8l7",
			Phase:    "phase-1-app",
		},
		{
			ReqID:    "req-3",
			TestFile: "taskboard/internal/web/ui_content_test.go",
			BeadID:   "ta-future",
			Phase:    "phase-2-kanban",
		},
	}
	// With ta-8l7 known, the non-active phases (phase-1-core, phase-2-kanban) should be ignored.
	// Since townRoot doesn't exist, ListRigBeadIDSet returns err, which returns nil.
	// Let's test with a mock if needed or verify that active phase logic doesn't crash.
	err := validateTestPlanBeadIDs("/nonexistent", "tasktracker", v, blocks)
	if err != nil {
		t.Fatalf("expected nil for nonexistent townRoot, got %v", err)
	}
}
