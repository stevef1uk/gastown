package session

import (
	"context"
	"os"
	"path/filepath"
)

// PipelineSessionNeedsRestart reports whether gt up should stop and recreate a pipeline
// agent session (mayor, planner, architect, qa, polecat).
//
//   - Dead gt-agent (wrapper may still exist): restart.
//   - Orchestrator mode: when .gt-nats-pids/<session> exists, require --orchestrated in argv.
func PipelineSessionNeedsRestart(ctx context.Context, p Provider, townRoot, sessionID string, wantOrchestrated bool) bool {
	if p == nil || sessionID == "" {
		return false
	}
	alive, err := p.IsAgentRunning(ctx, sessionID)
	if err == nil && !alive {
		return true
	}
	if !wantOrchestrated || townRoot == "" {
		return false
	}
	pidPath := filepath.Join(townRoot, ".gt-nats-pids", sessionID)
	if _, err := os.Stat(pidPath); err != nil {
		return false
	}
	return !GTAgentHasFlagInSession(townRoot, sessionID, "--orchestrated")
}
