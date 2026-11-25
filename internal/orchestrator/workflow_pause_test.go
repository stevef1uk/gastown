package orchestrator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPauseWorkflow_allowsNewStartOnSameRig(t *testing.T) {
	m, id := loadTestManager(t, designFlowTemplate())
	if _, err := m.PauseWorkflow(id); err != nil {
		t.Fatal(err)
	}
	if m.HasActiveWorkflow("rig-flow", "mockrig") {
		t.Fatal("paused workflow should not block HasActiveWorkflow")
	}
	newID, err := m.StartWorkflow("rig-flow", map[string]string{"rig": "mockrig"})
	if err != nil {
		t.Fatalf("StartWorkflow after pause: %v", err)
	}
	if newID == id {
		t.Fatal("expected new workflow id")
	}
}

func TestFetchTask_skipsPaused(t *testing.T) {
	m, id := loadTestManager(t, designFlowTemplate())
	if _, err := m.PauseWorkflow(id); err != nil {
		t.Fatal(err)
	}
	_, err := m.FetchTask("mockrig/architect")
	if err == nil {
		t.Fatal("expected no task for paused workflow")
	}
}

func TestResumeWorkflow(t *testing.T) {
	m, id := loadTestManager(t, designFlowTemplate())
	m.instances[id].PendingRework = &WorkflowRework{FromState: "planning", Outcome: "architect_rework", Summary: "superseded"}
	m.instances[id].ReviewFailures = map[string]int{"qa_review:phase-1": 3}
	m.instances[id].ReviewEscalations = map[string]int{"qa_review:phase-1": 1}
	m.instances[id].ReviewRecoveryBudgetStarted = map[string]bool{"qa_review:phase-1": true}
	if _, err := m.PauseWorkflow(id); err != nil {
		t.Fatal(err)
	}
	if err := m.ResumeWorkflow(id); err != nil {
		t.Fatal(err)
	}
	if m.instances[id].Status != "running" {
		t.Fatalf("status = %q", m.instances[id].Status)
	}
	if m.instances[id].PendingRework != nil {
		t.Fatalf("expected PendingRework to be cleared on design resume, got %v", m.instances[id].PendingRework)
	}
	if m.instances[id].ReviewFailures != nil || m.instances[id].ReviewEscalations != nil || m.instances[id].ReviewRecoveryBudgetStarted != nil {
		t.Fatalf("expected review failure tracking to be cleared on resume: failures=%v escalations=%v recovery_started=%v",
			m.instances[id].ReviewFailures, m.instances[id].ReviewEscalations, m.instances[id].ReviewRecoveryBudgetStarted)
	}
	task, err := m.FetchTask("mockrig/architect")
	if err != nil || task == nil {
		t.Fatalf("FetchTask after resume: %v", err)
	}
}

func TestResumeWorkflowReconcilesLegacyPhaseFromDisk(t *testing.T) {
	m, id := loadTestManager(t, designFlowTemplate())
	rigDir := filepath.Join(m.townRoot, "mockrig", "mayor", "rig")
	profileDir := filepath.Join(rigDir, rigProfileDir)
	if err := os.MkdirAll(filepath.Join(rigDir, "phase-one"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(profileDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rigDir, "phase-one", "done.go"), []byte("package phaseone\n"), 0644); err != nil {
		t.Fatal(err)
	}
	env := rigProfileEnvelope{Validation: WorkflowValidation{
		ActivePhaseIDField: "phase-01",
		DeliveryPhases: []DeliveryPhase{
			{ID: "phase-01", RequiredFiles: []string{"phase-one/done.go"}},
			{ID: "phase-02", RequiredFiles: []string{"phase-two/todo.go"}},
		},
	}}
	raw, err := marshalRigProfileJSON(env)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profileDir, rigProfileFile), raw, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.PauseWorkflow(id); err != nil {
		t.Fatal(err)
	}
	if err := m.ResumeWorkflow(id); err != nil {
		t.Fatal(err)
	}
	profile, found, err := LoadRawRigWorkflowProfile(m.townRoot, "mockrig")
	if err != nil || !found {
		t.Fatalf("load reconciled profile: found=%t err=%v", found, err)
	}
	if got := profile.ActivePhaseID(); got != "phase-02" {
		t.Fatalf("active phase after resume = %q, want phase-02", got)
	}
}

func TestResumeWorkflowPreservesExplicitPhaseRewind(t *testing.T) {
	m, id := loadTestManager(t, designFlowTemplate())
	rigDir := filepath.Join(m.townRoot, "mockrig", "mayor", "rig")
	profileDir := filepath.Join(rigDir, rigProfileDir)
	if err := os.MkdirAll(filepath.Join(rigDir, "phase-one"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(profileDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rigDir, "phase-one", "done.go"), []byte("package phaseone\n"), 0644); err != nil {
		t.Fatal(err)
	}
	env := rigProfileEnvelope{Validation: WorkflowValidation{
		ActivePhaseIDField:      "phase-01",
		RewoundFromPhaseIDField: "phase-02",
		DeliveryPhases: []DeliveryPhase{
			{ID: "phase-01", RequiredFiles: []string{"phase-one/done.go"}},
			{ID: "phase-02", RequiredFiles: []string{"phase-two/todo.go"}},
		},
	}}
	raw, err := marshalRigProfileJSON(env)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profileDir, rigProfileFile), raw, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.PauseWorkflow(id); err != nil {
		t.Fatal(err)
	}
	if err := m.ResumeWorkflow(id); err != nil {
		t.Fatal(err)
	}
	profile, found, err := LoadRawRigWorkflowProfile(m.townRoot, "mockrig")
	if err != nil || !found {
		t.Fatalf("load profile after resume: found=%t err=%v", found, err)
	}
	if got := profile.ActivePhaseID(); got != "phase-01" {
		t.Fatalf("active phase after resume = %q, want explicit rewind phase-01", got)
	}
}

func TestResumeWorkflowClearsPlanningTimeoutWhenPlanWasUpdated(t *testing.T) {
	m, id := loadTestManager(t, designFlowTemplate())
	inst := m.instances[id]
	inst.CurrentState = "planning"
	inst.StateEnteredAt = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	inst.PendingRework = &WorkflowRework{
		FromState: "planning",
		Outcome:   "timeout",
		Summary:   "Previous plan referenced stale bead IDs.",
	}
	planPath := filepath.Join(m.townRoot, "mockrig", "mayor", "rig", "plan.md")
	if err := os.MkdirAll(filepath.Dir(planPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(planPath, []byte("current plan\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.PauseWorkflow(id); err != nil {
		t.Fatal(err)
	}
	if err := m.ResumeWorkflow(id); err != nil {
		t.Fatal(err)
	}
	if got := m.instances[id].PendingRework; got != nil {
		t.Fatalf("pending rework after updated plan resume = %+v, want nil", got)
	}
}

func TestResumeWorkflowPreservesPlanningTimeoutWhenPlanUnchanged(t *testing.T) {
	m, id := loadTestManager(t, designFlowTemplate())
	inst := m.instances[id]
	inst.CurrentState = "planning"
	inst.StateEnteredAt = time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	inst.PendingRework = &WorkflowRework{
		FromState: "planning",
		Outcome:   "timeout",
		Summary:   "Planner timed out before finishing the current plan.",
	}
	planPath := filepath.Join(m.townRoot, "mockrig", "mayor", "rig", "plan.md")
	if err := os.MkdirAll(filepath.Dir(planPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(planPath, []byte("current plan\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.PauseWorkflow(id); err != nil {
		t.Fatal(err)
	}
	if err := m.ResumeWorkflow(id); err != nil {
		t.Fatal(err)
	}
	if got := m.instances[id].PendingRework; got == nil || got.Summary != "Planner timed out before finishing the current plan." {
		t.Fatalf("pending rework after unchanged plan resume = %+v", got)
	}
}

func TestLegacyPolecatsPausedFromStatuses_skipsPaused(t *testing.T) {
	paused := []WorkflowStatus{{
		ID: "wf-1", TemplateID: "rig-flow", Status: "paused",
		Variables: map[string]string{"rig": "mockrig"},
	}}
	if legacyPolecatsPausedFromStatuses(paused, "mockrig") {
		t.Fatal("paused rig-flow should not pause legacy polecats")
	}
}

func TestPauseReasonSurvivesReloadAndClearsOnResume(t *testing.T) {
	m, id := loadTestManager(t, designFlowTemplate())
	reason := "LLM API error (status 402): execution budget exhausted: workflow cost allowance $5.00"
	if _, err := m.PauseWorkflow(id, reason); err != nil {
		t.Fatal(err)
	}
	reloaded := NewManager(m.townRoot)
	statuses, err := reloaded.GetWorkflowStatus(id)
	if err != nil || len(statuses) != 1 || statuses[0].PauseReason != reason {
		t.Fatalf("persisted status: %+v %v", statuses, err)
	}
	if err := reloaded.ResumeWorkflow(id); err != nil {
		t.Fatal(err)
	}
	statuses, err = reloaded.GetWorkflowStatus(id)
	if err != nil || statuses[0].PauseReason != "" {
		t.Fatalf("stale pause reason: %+v %v", statuses, err)
	}
}

func TestPauseReasonThroughMCP(t *testing.T) {
	m, id := loadTestManager(t, designFlowTemplate())
	server := NewServer(m)
	params, _ := json.Marshal(map[string]interface{}{"name": "pause_workflow", "arguments": map[string]string{"workflow_id": id, "reason": "Budget: workflow cost allowance $5.00 exhausted"}})
	response := server.handleRequest(MCPRequest{JSONRPC: "2.0", ID: 1, Method: "call_tool", Params: params})
	if response.Error != nil {
		t.Fatal(response.Error)
	}
	statuses, err := m.GetWorkflowStatus(id)
	if err != nil || statuses[0].PauseReason != "Budget: workflow cost allowance $5.00 exhausted" {
		t.Fatalf("status: %+v, %v", statuses, err)
	}
}

func TestPollingAgentPauseReasonMatchesRig(t *testing.T) {
	statuses := []WorkflowStatus{
		{Status: "paused", Variables: map[string]string{"rig": "other"}, PauseReason: "Other rig error"},
		{Status: "paused", Variables: map[string]string{"rig": "demo"}, PauseReason: "Budget cost allowance exhausted"},
	}
	if got := workflowPauseReasonForRig(statuses, "demo"); got != "Budget cost allowance exhausted" {
		t.Fatalf("reason: %s", got)
	}
	if got := workflowPauseReasonForRig(statuses, "missing"); got == "" || got == "Other rig error" {
		t.Fatalf("fallback: %s", got)
	}
}
