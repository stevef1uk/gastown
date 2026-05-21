package daemon

import (
	"time"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

// ensureOrchestratorHealthy starts or restarts the rig-flow orchestrator when the MCP
// service is dead or unresponsive. Prefer false positives (restart) over a stuck FSM.
func (d *Daemon) ensureOrchestratorHealthy() {
	townRoot := d.config.TownRoot
	action, err := orchestrator.EnsureHealthy(townRoot, d.orchestratorLastRestart)
	if err != nil {
		if d.logger != nil {
			d.logger.Printf("Orchestrator health: %v", err)
		}
		return
	}
	if action == "" {
		return
	}
	d.orchestratorLastRestart = time.Now()
	if d.logger != nil {
		d.logger.Printf("Orchestrator %s", action)
	}
}
