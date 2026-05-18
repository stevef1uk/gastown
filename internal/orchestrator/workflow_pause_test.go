package orchestrator

import "testing"

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
