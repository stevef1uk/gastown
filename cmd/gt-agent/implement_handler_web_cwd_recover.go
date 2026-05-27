package main

import (
	"fmt"
	"strings"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

// tryHandlerWebCwdAutoFix patches handler cwd / Getwd issues when verify shows TestServeIndex 404 but web/ exists.
func (r *stateRunner) tryHandlerWebCwdAutoFix(mayorDir, cmdOutput string, combined *strings.Builder) bool {
	if r == nil || !orchestrator.ShouldAutoFixHandlerWebCwd404(mayorDir, r.townRoot, r.rig, r.v, cmdOutput) {
		return false
	}
	fixed, err := orchestrator.TryAutoFixHandlerWebCwd404(mayorDir, r.townRoot, r.rig, r.v, cmdOutput)
	if err != nil {
		combined.WriteString(fmt.Sprintf("handler web cwd auto-fix: %v\n\n", err))
		return false
	}
	if len(fixed) == 0 {
		return false
	}
	orchestratedPrintf("[gt-agent] auto-fixed handler web cwd: %v\n", fixed)
	combined.WriteString(fmt.Sprintf(
		"Auto-fixed handler web cwd (GT-VERIFY-001): %s — tests were using wrong working directory while web/ exists on disk; re-run the same verify CMD\n\n",
		strings.Join(fixed, ", "),
	))
	return true
}
