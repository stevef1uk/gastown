package main

import (
	"os"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestProcessOrchestratedTools_RecordsLearnDirectiveWithoutRunningAsShell(t *testing.T) {
	town := t.TempDir()
	rig := "myrig"
	v := orchestrator.WorkflowValidation{
		LayoutRoot:      "app",
		QAVerifyCommand: "cd app && go test ./...",
		RequiredFiles:   []string{"app/go.mod", "app/main.go"},
	}
	task := rigFlowTask(t, "implementation", v)
	r := newStateRunner(task, town, rig)

	resp := "LEARN: Always initialize slices as []Item{} so JSON marshals [] instead of null\n"
	var combined strings.Builder
	hadNative, _, cmdCount := r.processOrchestratedTools(resp, "sess-1", &combined)
	if !hadNative {
		t.Fatal("expected LEARN: directive to count as native tool handling")
	}
	if cmdCount != 0 {
		t.Fatalf("LEARN: must not be parsed as a shell command, got cmdCount=%d", cmdCount)
	}
	if !strings.Contains(combined.String(), "LEARN recorded:") {
		t.Fatalf("expected confirmation in combined output, got: %q", combined.String())
	}

	block := orchestrator.FormatPolecatLearningsBlock(town, rig, v)
	if !strings.Contains(block, "Always initialize slices as []Item{}") {
		t.Fatalf("expected persisted learning in prompt context block, got:\n%s", block)
	}
	if _, err := os.Stat(orchestrator.TownStackLearningsPath(town, "go")); err != nil {
		t.Fatalf("expected town go learnings file to be created: %v", err)
	}
}

func TestRecordTerminalTaskLearnings_JSONAndQARework(t *testing.T) {
	town := t.TempDir()
	rig := "myrig"
	v := orchestrator.WorkflowValidation{
		LayoutRoot:      "backend",
		QAVerifyCommand: "python3 -m pytest -q backend",
		TestRunner:      "pytest",
		RequiredFiles:   []string{"backend/main.py", "backend/requirements.txt"},
	}
	task := rigFlowTask(t, "implementation", v)
	task.PendingRework = &orchestrator.WorkflowRework{
		FromState: "qa_review",
		Summary:   "POST /score returned 200 instead of 201 when updating high score.",
	}
	r := newStateRunner(task, town, rig)

	jsonResp := `{"outcome":"success","summary":"Fixed POST /score status code","learning":"Return explicit status_code=201 on POST /score in FastAPI"}`
	r.recordTerminalTaskLearnings(jsonResp, "success")

	block := orchestrator.FormatPolecatLearningsBlock(town, rig, v)
	if !strings.Contains(block, "Return explicit status_code=201") {
		t.Fatalf("expected JSON learning in block, got:\n%s", block)
	}
	if !strings.Contains(block, "QA lesson: POST /score returned 200") {
		t.Fatalf("expected QA rework learning in block, got:\n%s", block)
	}
}
