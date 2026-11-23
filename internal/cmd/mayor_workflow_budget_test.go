package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestRunMayorWorkflowBudgetSetsScopedOverrides(t *testing.T) {
	t.Setenv("GT_WORKFLOW_MAX_LLM_TOKENS", "20000000")
	town := t.TempDir()
	if err := os.MkdirAll(filepath.Join(town, "mayor"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(town, "mayor", "town.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(town, "orchestrator"), 0755); err != nil {
		t.Fatal(err)
	}
	instances := `{"instances":[{"id":"wf-1","template_id":"rig-flow","current_state":"implementation","variables":{"rig":"pwtest"},"status":"paused"}],"next_seq":1}`
	if err := os.WriteFile(filepath.Join(town, "orchestrator", "instances.json"), []byte(instances), 0600); err != nil {
		t.Fatal(err)
	}
	tokenLedger := `{"[\"pwtest\",\"wf-1\"]":{"reserved_tokens":19988194,"token_limit":20000000}}`
	if err := os.WriteFile(filepath.Join(town, "orchestrator", "execution-budgets.json"), []byte(tokenLedger), 0600); err != nil {
		t.Fatal(err)
	}

	oldTown, oldTokens, oldUSD := workflowTownRoot, workflowBudgetTokens, workflowBudgetUSD
	t.Cleanup(func() {
		workflowTownRoot = oldTown
		workflowBudgetTokens = oldTokens
		workflowBudgetUSD = oldUSD
	})
	workflowTownRoot = town
	cmd := &cobra.Command{}
	cmd.Flags().Int64Var(&workflowBudgetTokens, "tokens", 0, "")
	cmd.Flags().Float64Var(&workflowBudgetUSD, "usd", 0, "")
	if err := cmd.Flags().Set("tokens", "40000000"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("usd", "10"); err != nil {
		t.Fatal(err)
	}
	if err := runMayorWorkflowBudget(cmd, []string{"wf-1"}); err != nil {
		t.Fatal(err)
	}

	got, persisted, err := orchestrator.ReadWorkflowExecutionBudget(town, "pwtest", "wf-1")
	if err != nil || !persisted || got.ReservedTokens != 19988194 || got.TokenLimit != 40000000 || got.TokenLimitOverride != 40000000 {
		t.Fatalf("budget=%+v persisted=%v err=%v", got, persisted, err)
	}
	if got.DollarLimitMicros != 10000000 || got.DollarLimitOverrideMicros != 10000000 {
		t.Fatalf("dollar limit not persisted: %+v", got)
	}
	other, _, err := orchestrator.ReadWorkflowExecutionBudget(town, "pwtest", "another-workflow")
	if err != nil || other.TokenLimit != 20000000 || other.DollarLimitOverrideMicros != 0 {
		t.Fatalf("other workflow budget=%+v err=%v", other, err)
	}
}
