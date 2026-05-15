package orchestrator

import "testing"

func TestLegacyPolecatsPausedFromStatuses(t *testing.T) {
	active := []WorkflowStatus{{
		ID: "wf-1", TemplateID: "rig-flow", Status: "running",
		Variables: map[string]string{"rig": "testgt2"},
	}}
	if !legacyPolecatsPausedFromStatuses(active, "testgt2") {
		t.Fatal("expected pause for active rig-flow on same rig")
	}
	if legacyPolecatsPausedFromStatuses(active, "other") {
		t.Fatal("should not pause for different rig")
	}
	if !legacyPolecatsPausedFromStatuses(active, "") {
		t.Fatal("empty rig filter should pause when any active rig-flow exists")
	}

	done := []WorkflowStatus{{
		ID: "wf-1", TemplateID: "rig-flow", Status: "completed",
		Variables: map[string]string{"rig": "testgt2"},
	}}
	if legacyPolecatsPausedFromStatuses(done, "testgt2") {
		t.Fatal("completed workflow should not pause polecats")
	}

	otherTpl := []WorkflowStatus{{
		ID: "wf-2", TemplateID: "build-spec", Status: "running",
		Variables: map[string]string{"rig": "testgt2"},
	}}
	if legacyPolecatsPausedFromStatuses(otherTpl, "testgt2") {
		t.Fatal("non rig-flow template should not pause polecats")
	}
}
