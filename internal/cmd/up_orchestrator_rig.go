package cmd

import (
	"github.com/steveyegge/gastown/internal/orchestrator"
)

// rigOrchestratorAgentSkip returns (true, detail) when orchestrator mode should not start rig agents.
func rigOrchestratorAgentSkip(townRoot, rigName, agentLabel string) (bool, string) {
	if reason := orchestrator.SkipRigAgentStartReason(townRoot, rigName); reason != "" {
		return true, "skipped (" + reason + ")"
	}
	return false, ""
}
