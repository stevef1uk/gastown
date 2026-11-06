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

// IsRigWorkflowPaused reports whether the rig's active workflow instance is paused.
func IsRigWorkflowPaused(townRoot, rigName string) bool {
	return RigWorkflowActivityForRig(townRoot, rigName) == RigWorkflowPaused
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

// RigWorkflowPauseReason supplies the same persisted cause to polling agents and
// operator status; old log history cannot be backfilled with missing reasons.
func RigWorkflowPauseReason(townRoot, rig string) string {
	statuses, err := GetWorkflowStatuses(townRoot, "")
	if err != nil {
		return "Cannot read the saved pause reason; check workflow status and agent logs."
	}
	return workflowPauseReasonForRig(statuses, rig)
}

func workflowPauseReasonForRig(statuses []WorkflowStatus, rig string) string {
	for _, status := range statuses {
		if status.Variables["rig"] == rig && status.Status == "paused" && strings.TrimSpace(status.PauseReason) != "" {
			return status.PauseReason
		}
	}
	return "No pause reason was recorded; check workflow status and agent logs."
}
