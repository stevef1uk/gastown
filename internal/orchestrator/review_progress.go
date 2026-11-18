package orchestrator

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// recordReviewFailure bounds repeated failures at a review gate even when each
// retry passes through successful intermediate states. Phase-scoped keys keep
// independent delivery phases from sharing a retry budget.
func (inst *WorkflowInstance) recordReviewFailure(state, phase, outcome string, limit int) {
	if limit <= 0 {
		return
	}
	phase = strings.TrimSpace(phase)
	if phase == "" {
		phase = "unphased"
	}
	key := state + ":" + phase
	for previous := range inst.ReviewFailures {
		if strings.HasPrefix(previous, state+":") && previous != key {
			delete(inst.ReviewFailures, previous)
		}
	}
	if IsCheckpointOutcome(outcome) {
		return
	}
	if !IsCrossStateReworkOutcome(outcome) && !IsVerificationErrorOutcome(outcome) {
		delete(inst.ReviewFailures, key)
		return
	}
	if inst.ReviewFailures == nil {
		inst.ReviewFailures = map[string]int{}
	}
	inst.ReviewFailures[key]++
	if inst.ReviewFailures[key] < limit {
		return
	}
	inst.Status = "paused"
	inst.PauseReason = fmt.Sprintf("%s failed %d times in delivery phase %q; workflow paused to prevent a repeated review/rework cycle", state, inst.ReviewFailures[key], phase)
	if inst.PendingRework != nil {
		inst.PendingRework.Feedback += "\n\n" + inst.PauseReason + ". Review the latest findings and correct the responsible artifact before resuming."
	}
}
