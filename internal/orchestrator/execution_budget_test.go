package orchestrator

import (
	"encoding/json"
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

func TestWorkflowTokenLimitOverride(t *testing.T) {
	town := t.TempDir()
	t.Setenv("GT_WORKFLOW_MAX_LLM_REQUESTS", "12")
	t.Setenv("GT_WORKFLOW_MAX_LLM_TOKENS", "100")
	if err := ReserveWorkflowLLMRequest(town, "rig", "wf", 80); err != nil {
		t.Fatal(err)
	}
	if err := SetWorkflowTokenLimit(town, "rig", "wf", 200); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GT_WORKFLOW_MAX_LLM_TOKENS", "100")
	if err := ReserveWorkflowLLMRequest(town, "rig", "wf", 100); err != nil {
		t.Fatal(err)
	}
	got, persisted, err := ReadWorkflowExecutionBudget(town, "rig", "wf")
	if err != nil || !persisted || got.Requests != 2 || got.ReservedTokens != 180 || got.TokenLimit != 200 || got.TokenLimitOverride != 200 {
		t.Fatalf("%+v persisted=%v err=%v", got, persisted, err)
	}
	if err := SetWorkflowTokenLimit(town, "rig", "wf", 180); err == nil {
		t.Fatal("accepted a token limit that leaves no room for another request")
	}
}

func TestWorkflowDollarLimitOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "execution-budgets.json")
	if err := os.WriteFile(path+".usd", []byte(`{"[\"rig\",\"wf\"]":714426}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := SetWorkflowDollarLimit(path, "rig", "wf", 10); err != nil {
		t.Fatal(err)
	}
	limits, err := readExecutionBudgetValues(path + ".limits")
	if err != nil || limits[`["rig","wf"]`] != 10000000 {
		t.Fatalf("limits=%v err=%v", limits, err)
	}
	if err := SetWorkflowDollarLimit(path, "rig", "wf", 0.714426); err == nil {
		t.Fatal("accepted a dollar limit equal to already-accounted usage")
	}
	if err := os.WriteFile(path+".usd", []byte(`{"[\"rig\",\"wf\"]":-1}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := SetWorkflowDollarLimit(path, "rig", "wf", 20); err == nil {
		t.Fatal("accepted a negative cost ledger")
	}
	var persisted map[string]int64
	data, err := os.ReadFile(path + ".limits")
	if err != nil || json.Unmarshal(data, &persisted) != nil || persisted[`["rig","wf"]`] != 10000000 {
		t.Fatalf("persisted limits=%s err=%v", data, err)
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
