package orchestrator

import (
	"encoding/json"
	"testing"
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
	if _, err := m.PauseWorkflow(id); err != nil {
		t.Fatal(err)
	}
	if err := m.ResumeWorkflow(id); err != nil {
		t.Fatal(err)
	}
	if m.instances[id].Status != "running" {
		t.Fatalf("status = %q", m.instances[id].Status)
	}
	task, err := m.FetchTask("mockrig/architect")
	if err != nil || task == nil {
		t.Fatalf("FetchTask after resume: %v", err)
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
