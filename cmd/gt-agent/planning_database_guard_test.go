package main

import "testing"

func TestPlanningBlocksDatabaseAdministration(t *testing.T) {
	for _, cmd := range []string{
		"cd pwtest/mayor/rig && export BEADS_DIR=$GT_ROOT/pwtest/.beads && bd init --reinit-local --discard-remote --destroy-token=DESTROY-pwtest --prefix pwtest --database pwtest --server --skip-agents --skip-hooks",
		"bd list --status=open && bd init --prefix pw",
		"cat plan.md; bd bootstrap",
		"bd migrate --yes",
		"bd sql 'delete from issues'",
		"bd config set issue_prefix pw",
		"bd dolt set database other",
		"dolt sql -q 'drop database pwtest'",
		"/usr/local/bin/bd init --prefix pw",
	} {
		if err := validatePlanningCommand(cmd, "pwtest"); err == nil {
			t.Errorf("allowed database administration: %s", cmd)
		}
	}
}

func TestPlanningAllowsOrdinaryBeadOperations(t *testing.T) {
	for _, cmd := range []string{"bd list --status=open", "bd config get issue_prefix", "bd dolt status", "bd create --type task --title 'Implement pingapp/go.mod per architecture'", "cd pwtest/mayor/rig && cat plan.md"} {
		if err := validatePlanningCommand(cmd, "pwtest"); err != nil {
			t.Errorf("blocked %s: %v", cmd, err)
		}
	}
}
