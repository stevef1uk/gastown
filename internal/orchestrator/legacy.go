package orchestrator

// legacyPolecatsPausedFromStatuses implements rig-flow pause logic from workflow snapshots.
func legacyPolecatsPausedFromStatuses(statuses []WorkflowStatus, rigName string) bool {
	for _, s := range statuses {
		if s.Status == "completed" || s.Status == "failed" {
			continue
		}
		if s.TemplateID != "rig-flow" {
			continue
		}
		if rigName != "" && s.Variables["rig"] != rigName {
			continue
		}
		return true
	}
	return false
}

// LegacyPolecatsPaused reports whether per-bead polecat sessions should stay
// stopped while a rig-flow workflow is active for the rig (orchestrator uses
// hq-polecat for the implementation step instead).
func LegacyPolecatsPaused(townRoot, rigName string) bool {
	running, _, err := IsRunning(townRoot)
	if err != nil || !running {
		return false
	}
	statuses, err := GetWorkflowStatuses(townRoot, "")
	if err != nil {
		return false
	}
	return legacyPolecatsPausedFromStatuses(statuses, rigName)
}
