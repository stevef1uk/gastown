package main

import (
	"fmt"
	"strings"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

// tryGoimportsForCompileFailure runs goimports on all .go files in packages cited by compile output.
func (r *stateRunner) tryGoimportsForCompileFailure(mayorDir, cmdOutput string, combined *strings.Builder) bool {
	if r == nil || !orchestrator.GoCompileOutputHasUnusedImport(cmdOutput) {
		return false
	}
	touched, ran, err := orchestrator.RunGoimportsOnCompileOutput(mayorDir, r.v.LayoutRoot, cmdOutput)
	if !ran {
		return false
	}
	if err != nil {
		combined.WriteString(fmt.Sprintf("goimports package tidy: %v\n\n", err))
		return false
	}
	if len(touched) > 0 {
		orchestratedPrintf("[gt-agent] goimports package tidy: %v\n", touched)
		combined.WriteString(fmt.Sprintf("Auto-ran goimports on package: %s — re-run the same verify CMD\n\n", strings.Join(touched, ", ")))
	}
	return true
}
