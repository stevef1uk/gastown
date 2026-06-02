package daemon

import (
	"github.com/steveyegge/gastown/internal/session"
)

// restartStalePipelineSession stops a rig-flow pipeline session when gt-agent is dead
// or missing --orchestrated while the orchestrator expects orchestrated mode.
func (d *Daemon) restartStalePipelineSession(sessionID string, wantOrchestrated bool) bool {
	if session.RestartStalePipelineSession(d.ctx, d.sp, d.config.TownRoot, sessionID, wantOrchestrated) {
		d.logger.Printf("Restarting pipeline session %s (gt-agent dead or not orchestrated)", sessionID)
		return true
	}
	return false
}
