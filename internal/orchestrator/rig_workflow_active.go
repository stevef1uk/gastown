package orchestrator

import "strings"

// Rig workflow activity for gt up / rig agent provisioning.
const (
	RigWorkflowRunning = "running"
	RigWorkflowPaused  = "paused"
	RigWorkflowIdle    = "idle" // no non-terminal workflow for this rig
)

// RigWorkflowActivityForRig reports whether the rig has a running, paused, or idle workflow.
func RigWorkflowActivityForRig(townRoot, rigName string) string {
	if rigName == "" {
		return RigWorkflowIdle
	}
	statuses, err := GetWorkflowStatuses(townRoot, "")
	if err != nil {
		return RigWorkflowIdle
	}
	return rigWorkflowActivityFromStatuses(rigName, statuses)
}

func rigWorkflowActivityFromStatuses(rigName string, statuses []WorkflowStatus) string {
	hasPaused := false
	for _, s := range statuses {
		if strings.TrimSpace(s.Variables["rig"]) != rigName {
			continue
		}
		if isWorkflowTerminalStatus(s.Status) {
			continue
		}
		if isWorkflowRunningStatus(s.Status) {
			return RigWorkflowRunning
		}
		if s.Status == "paused" {
			hasPaused = true
		}
	}
	if hasPaused {
		return RigWorkflowPaused
	}
	return RigWorkflowIdle
}

// SkipRigAgentStartReason returns a non-empty operator message when gt up should not
// start rig-scoped agents while the orchestrator is running (paused or idle rig).
func SkipRigAgentStartReason(townRoot, rigName string) string {
	orchRunning, _, err := IsRunning(townRoot)
	if err != nil || !orchRunning {
		return ""
	}
	switch RigWorkflowActivityForRig(townRoot, rigName) {
	case RigWorkflowRunning:
		return ""
	case RigWorkflowPaused:
		return "workflow paused — use gt mayor workflow resume, or delete the instance"
	default:
		return "no running workflow for this rig"
	}
}
