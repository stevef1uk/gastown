package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// rejectTestPlanSpuriousFailure blocks tester failure outcomes that claim the
// required input documents are missing/unusable when all of them verifiably
// exist on disk. Weak models lose context across turns and emit "SPEC.md
// missing / no route table" failures for files they read earlier in the same
// attempt, cycling test_plan -> planning -> test_plan forever without ever
// attempting the TEST_PLAN.md heredoc. The rejection feedback names the
// verified files and demands a write attempt before any outcome.
func (r *stateRunner) rejectTestPlanSpuriousFailure(outcome string) (string, bool) {
	if r == nil || r.hooks.Artifacts != "test_plan" || !isOrchestratedFailureOutcome(outcome) {
		return "", false
	}
	if r.track.testPlanWriteOK {
		return "", false // a real write happened; let normal validation speak
	}
	rigDir := rigMayorRigDir(r.townRoot, r.rig)
	var present []string
	for _, name := range []string{"SPEC.md", "architecture.md", "plan.md"} {
		if info, err := os.Stat(filepath.Join(rigDir, name)); err == nil && info.Size() > 0 {
			present = append(present, name)
		}
	}
	if len(present) < 3 {
		return "", false // genuine missing-input complaint — allow it
	}
	return fmt.Sprintf(
		"Failure rejected: %s all exist and are non-empty (verified on disk just now). "+
			"Do not report failure before attempting the write. Reply with CMD lines only:\n"+
			"CMD: cd %s/mayor/rig && head -n 40 SPEC.md\n"+
			"CMD: cd %s/mayor/rig && cat > TEST_PLAN.md <<'EOF'\n"+
			"### <active-phase-id> ... (Level, Test file, Bead ID, Scenarios, Assertions)\n"+
			"EOF\n"+
			"CMD: cd %s/mayor/rig && wc -c TEST_PLAN.md",
		strings.Join(present, ", "), r.rig, r.rig, r.rig), true
}
