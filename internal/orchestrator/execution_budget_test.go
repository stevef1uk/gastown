package orchestrator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReserveWorkflowLLMRequestReportsAvailableTokens(t *testing.T) {
	town := t.TempDir()
	t.Setenv("GT_WORKFLOW_MAX_LLM_TOKENS", "100")

	if available, err := ReserveWorkflowLLMRequestWithAvailable(town, "rig", "wf", 70); err != nil || available != 100 {
		t.Fatalf("available=%d err=%v, want available=100 and no error", available, err)
	}
	if available, err := ReserveWorkflowLLMRequestWithAvailable(town, "rig", "wf", 31); err == nil ||
		available != 30 || !strings.Contains(err.Error(), "requested 31 reservation units, 30 available, token limit 100") {
		t.Fatalf("available=%d err=%v, want a 30-unit denial diagnostic", available, err)
	}

	got, _, err := ReadWorkflowExecutionBudget(town, "rig", "wf")
	if err != nil || got.ReservedTokens != 70 {
		t.Fatalf("denied request changed ledger: %+v err=%v", got, err)
	}
}

func TestExecutionBudgetSnapshotPersistsAdmissionLimits(t *testing.T) {
	town := t.TempDir()
	t.Setenv("GT_WORKFLOW_MAX_LLM_TOKENS", "500")
	if err := ReserveWorkflowLLMRequest(town, "rig", "wf", 100); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GT_WORKFLOW_MAX_LLM_TOKENS", "999")
	got, persisted, err := ReadWorkflowExecutionBudget(town, "rig", "wf")
	if err != nil || !persisted || got.ReservedTokens != 100 || got.TokenLimit != 500 {
		t.Fatalf("%+v persisted=%v err=%v", got, persisted, err)
	}
}

func TestReserveWorkflowUsesPersistedTokenLimitWhenEnvironmentIsUnset(t *testing.T) {
	town := t.TempDir()
	t.Setenv("GT_WORKFLOW_MAX_LLM_TOKENS", "")
	path := filepath.Join(town, "orchestrator", "execution-budgets.json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	ledger := map[string]ExecutionBudget{
		`["rig","wf"]`: {
			ReservedTokens: 1000,
			TokenLimit:     200000000,
		},
	}
	data, err := json.Marshal(ledger)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}

	if err := ReserveWorkflowLLMRequest(town, "rig", "wf", 100); err != nil {
		t.Fatalf("resume admission with persisted token limit: %v", err)
	}
	got, persisted, err := ReadWorkflowExecutionBudget(town, "rig", "wf")
	if err != nil || !persisted || got.ReservedTokens != 1100 || got.TokenLimit != 200000000 {
		t.Fatalf("budget after admission: %+v persisted=%t err=%v", got, persisted, err)
	}

	t.Setenv("GT_WORKFLOW_MAX_LLM_TOKENS", "1100")
	if err := ReserveWorkflowLLMRequest(town, "rig", "wf", 100); err == nil {
		t.Fatal("explicit lower token limit should override the persisted limit")
	}
}

func TestWorkflowTokenLimitOverride(t *testing.T) {
	town := t.TempDir()
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
	if err != nil || !persisted || got.ReservedTokens != 180 || got.TokenLimit != 200 || got.TokenLimitOverride != 200 {
		t.Fatalf("%+v persisted=%v err=%v", got, persisted, err)
	}
	if err := SetWorkflowTokenLimit(town, "rig", "wf", 180); err == nil {
		t.Fatal("accepted a token limit that leaves no room for another request")
	}
}

func TestWorkflowDollarLimitOverride(t *testing.T) {
	town := t.TempDir()
	if err := SetWorkflowDollarLimit(town, "rig", "wf", 10); err != nil {
		t.Fatal(err)
	}
	ledger, err := readExecutionBudget(executionBudgetPath(town))
	if err != nil {
		t.Fatal(err)
	}
	got := ledger[`["rig","wf"]`]
	if got.DollarLimitOverrideMicros != 10000000 || got.DollarLimitMicros != 10000000 {
		t.Fatalf("dollar limit not persisted locally: %+v", got)
	}
	if err := SetWorkflowDollarLimit(town, "rig", "wf", 0); err == nil {
		t.Fatal("accepted a zero dollar limit")
	}
	path := executionBudgetPath(town)
	if err := os.WriteFile(path, []byte(`{"[\"rig\",\"wf\"]":{"dollar_limit_micros":-1}}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := SetWorkflowDollarLimit(town, "rig", "wf", 20); err == nil {
		t.Fatal("accepted a negative dollar limit")
	}
}

func TestExecutionBudgetSnapshotLegacyAndCorrupt(t *testing.T) {
	town := t.TempDir()
	dir := filepath.Join(town, "orchestrator")
	os.MkdirAll(dir, 0755)
	path := filepath.Join(dir, "execution-budgets.json")
	t.Setenv("GT_WORKFLOW_MAX_LLM_TOKENS", "20000000")
	for _, body := range []string{`{}`, `{"[\"rig\",\"wf\"]":{"reserved_tokens":45}}`} {
		if err := os.WriteFile(path, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
		got, persisted, err := ReadWorkflowExecutionBudget(town, "rig", "wf")
		if err != nil || persisted || got.TokenLimit != 20000000 {
			t.Fatalf("%+v %v %v", got, persisted, err)
		}
	}
	for _, body := range []string{`null`, `broken`, `{"[\"rig\",\"wf\"]":{"reserved_tokens":-1}}`} {
		os.WriteFile(path, []byte(body), 0644)
		if _, _, err := ReadWorkflowExecutionBudget(town, "rig", "wf"); err == nil {
			t.Fatalf("accepted corrupt ledger %s", body)
		}
	}
}
