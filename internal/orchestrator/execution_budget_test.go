package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExecutionBudgetSnapshotPersistsAdmissionLimits(t *testing.T) {
	town := t.TempDir()
	t.Setenv("GT_WORKFLOW_MAX_LLM_REQUESTS", "12")
	t.Setenv("GT_WORKFLOW_MAX_LLM_TOKENS", "500")
	if err := ReserveWorkflowLLMRequest(town, "rig", "wf", 100); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GT_WORKFLOW_MAX_LLM_REQUESTS", "99")
	got, persisted, err := ReadWorkflowExecutionBudget(town, "rig", "wf")
	if err != nil || !persisted || got.Requests != 1 || got.ReservedTokens != 100 || got.RequestLimit != 12 || got.TokenLimit != 500 {
		t.Fatalf("%+v persisted=%v err=%v", got, persisted, err)
	}
}
func TestExecutionBudgetSnapshotLegacyAndCorrupt(t *testing.T) {
	town := t.TempDir()
	dir := filepath.Join(town, "orchestrator")
	os.MkdirAll(dir, 0755)
	path := filepath.Join(dir, "execution-budgets.json")
	t.Setenv("GT_WORKFLOW_MAX_LLM_REQUESTS", "1000")
	t.Setenv("GT_WORKFLOW_MAX_LLM_TOKENS", "20000000")
	for _, body := range []string{`{}`, `{"[\"rig\",\"wf\"]":{"requests":3,"reserved_tokens":45}}`} {
		if err := os.WriteFile(path, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
		got, persisted, err := ReadWorkflowExecutionBudget(town, "rig", "wf")
		if err != nil || persisted || got.RequestLimit != 1000 || got.TokenLimit != 20000000 {
			t.Fatalf("%+v %v %v", got, persisted, err)
		}
	}
	for _, body := range []string{`null`, `broken`, `{"[\"rig\",\"wf\"]":{"requests":-1}}`} {
		os.WriteFile(path, []byte(body), 0644)
		if _, _, err := ReadWorkflowExecutionBudget(town, "rig", "wf"); err == nil {
			t.Fatalf("accepted corrupt ledger %s", body)
		}
	}
}
