package orchestrator

import (
	"strings"

	"github.com/steveyegge/gastown/internal/session"
)

// SessionShouldAutoRespawn reports whether a NATS session that exited should be
// restarted automatically (AutoRespawn). Patrol agents always respawn; orchestrated
// pipeline agents respawn only while the orchestrator is up and the rig workflow runs.
func SessionShouldAutoRespawn(townRoot, sessionID string) bool {
	if townRoot == "" || sessionID == "" {
		return false
	}
	orchRunning, _, _ := IsRunning(townRoot)
	switch sessionID {
	case session.MayorSessionName(), session.SetupSessionName():
		return orchRunning
	}
	rig := rigNameFromPipelineSessionID(sessionID)
	if rig != "" && isOrchestratedRigPipelineSession(sessionID) {
		if !orchRunning {
			return false
		}
		return RigWorkflowActivityForRig(townRoot, rig) == RigWorkflowRunning
	}
	return true
}

func isOrchestratedRigPipelineSession(sessionID string) bool {
	for _, suffix := range []string{"-polecat", "-architect", "-planner", "-qa"} {
		if strings.HasSuffix(sessionID, suffix) {
			return true
		}
	}
	return false
}

// rigNameFromPipelineSessionID extracts the rig from te-<rig>-<role> session names.
func rigNameFromPipelineSessionID(sessionID string) string {
	if !strings.HasPrefix(sessionID, "te-") {
		return ""
	}
	rest := strings.TrimPrefix(sessionID, "te-")
	for _, suffix := range []string{"-polecat", "-architect", "-planner", "-qa", "-witness", "-refinery"} {
		if strings.HasSuffix(rest, suffix) {
			return strings.TrimSuffix(rest, suffix)
		}
	}
	return ""
}
