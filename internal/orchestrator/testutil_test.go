package orchestrator

import (
	"encoding/json"
	"testing"
)

// payloadToTask decodes a FetchTask / BuildTaskPayload map like gt-agent receives over NATS.
func payloadToTask(t *testing.T, payload map[string]interface{}) *Task {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var task Task
	if err := json.Unmarshal(data, &task); err != nil {
		t.Fatal(err)
	}
	return &task
}

// loadRigFlowTemplate loads the bundled rig-flow.yaml (embedded in town_embed.go).
func loadRigFlowTemplate(t *testing.T) *WorkflowTemplate {
	t.Helper()
	tpl, err := LoadRigFlowTemplate()
	if err != nil {
		t.Fatal(err)
	}
	return tpl
}
