package main

import (
	"fmt"
	"strings"
)

// rejectReviewRecoverySpuriousPause blocks mayor pause outcomes in review_recovery
// that cite tool execution failures, path problems, or missing commands rather than
// an authoritative contradiction between specification and requirements.
func (r *stateRunner) rejectReviewRecoverySpuriousPause(outcome, summary string) (string, bool) {
	if r == nil || r.task == nil || r.task.State != "review_recovery" {
		return "", false
	}
	if outcome != "pause" {
		return "", false
	}
	lower := strings.ToLower(summary)
	spuriousIndicators := []string{
		"inspection blocked",
		"tool did not return",
		"cannot proceed without reading",
		"command failed",
		"no such file",
		"exit status",
		"did not output",
		"timed out",
		"execution failed",
		"shell",
	}
	for _, ind := range spuriousIndicators {
		if strings.Contains(lower, ind) {
			fromState := "prior reviewer"
			if r.task.PendingRework != nil && r.task.PendingRework.FromState != "" {
				fromState = r.task.PendingRework.FromState
			}
			return fmt.Sprintf("Pause outcome rejected: %q is a tool/inspection issue, not an authoritative requirement contradiction. The review findings from %s are authoritative and all relevant artifacts are pre-assembled in the prompt. You must route one targeted repair: implementation_rework, tester_rework, planner_rework, or architect_rework.", summary, fromState), true
		}
	}
	return "", false
}
