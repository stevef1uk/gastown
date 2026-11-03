package orchestrator

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
)

// Review progress is template-configured, persisted, and scoped to a workflow.
// Cosmetic architecture amendments cannot reset a profile-based progress budget.
func (inst *WorkflowInstance) recordReviewProgress(townRoot, rig, state, outcome string, hooks StateHooks) {
	if hooks.NoProgressLimit <= 0 || len(hooks.ProgressFiles) == 0 || rig == "" {
		return
	}
	if !IsFailureOutcome(outcome) {
		delete(inst.ReviewProgress, state)
		return
	}
	hash := sha256.New()
	for _, file := range hooks.ProgressFiles {
		data, err := os.ReadFile(filepath.Join(townRoot, rig, "mayor", "rig", file))
		fmt.Fprintf(hash, "%s\x00%v\x00", file, os.IsNotExist(err))
		if err != nil && !os.IsNotExist(err) {
			return // An I/O outage is not evidence that the artifacts are unchanged.
		}
		hash.Write(data)
		hash.Write([]byte{0})
	}
	if inst.ReviewProgress == nil {
		inst.ReviewProgress = map[string]ReviewProgress{}
	}
	progress := inst.ReviewProgress[state]
	fingerprint := fmt.Sprintf("%x", hash.Sum(nil))
	if progress.Fingerprint != fingerprint {
		progress = ReviewProgress{Fingerprint: fingerprint}
	}
	progress.Failures++
	inst.ReviewProgress[state] = progress
	if progress.Failures >= hooks.NoProgressLimit {
		inst.Status = "paused"
		if inst.PendingRework != nil {
			inst.PendingRework.Feedback += fmt.Sprintf("\n\nWorkflow paused: %s failed %d times without changing %v. Correct the effective contract, then resume the workflow; repeated unchanged failures will pause it again.", state, progress.Failures, hooks.ProgressFiles)
		}
	}
}
