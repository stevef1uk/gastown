package main

import (
	"fmt"
	"strings"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

// tryHandlerWebScaffoldAutoFix creates minimal web/ files when handler tests 404 on missing assets.
func (r *stateRunner) tryHandlerWebScaffoldAutoFix(mayorDir, cmdOutput string, combined *strings.Builder) bool {
	if r == nil {
		return false
	}
	created, err := orchestrator.TryScaffoldWebAssetsForHandlerTestFailure(mayorDir, r.v, cmdOutput)
	if err != nil {
		combined.WriteString(fmt.Sprintf("handler web scaffold auto-fix: %v\n\n", err))
		return false
	}
	if len(created) == 0 {
		return false
	}
	orchestratedPrintf("[gt-agent] auto-scaffolded web for handler tests: %v\n", created)
	combined.WriteString(fmt.Sprintf(
		"Auto-scaffolded web assets for handler tests: %s — handler tests use paths relative to the module root (web/index.html); re-run the same verify CMD\n\n",
		strings.Join(created, ", "),
	))
	return true
}

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

// tryHandlerTestStoreDBAutoFix patches handlers_test.go TestMain when store package DB was never assigned.
func (r *stateRunner) tryHandlerTestStoreDBAutoFix(mayorDir, cmdOutput string, combined *strings.Builder) bool {
	if r == nil || !strings.Contains(cmdOutput, "store DB not initialized") {
		return false
	}
	fixed, err := orchestrator.TryAutoFixHandlerTestStoreDB(mayorDir, r.v, cmdOutput)
	if err != nil {
		combined.WriteString(fmt.Sprintf("handler TestMain store.DB auto-fix: %v\n\n", err))
		return false
	}
	if len(fixed) == 0 {
		return false
	}
	orchestratedPrintf("[gt-agent] auto-fixed handler TestMain store.DB: %v\n", fixed)
	combined.WriteString(fmt.Sprintf(
		"Auto-fixed handler TestMain store.DB: %s — TestMain opened sqlite but never set store.DB; re-run the same verify CMD\n\n",
		strings.Join(fixed, ", "),
	))
	return true
}
