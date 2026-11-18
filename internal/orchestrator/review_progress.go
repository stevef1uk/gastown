package orchestrator

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type reviewFailureAction int

const (
	reviewFailureContinue reviewFailureAction = iota
	reviewFailureEscalate
	reviewFailurePause
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
func (inst *WorkflowInstance) recordReviewFailure(state, phase, outcome string, limit int, recoveryState string) reviewFailureAction {
	if limit <= 0 {
		return reviewFailureContinue
	}
	phase = strings.TrimSpace(phase)
	if phase == "" {
		phase = "unphased"
	}
	key := state + ":" + phase
	for previous := range inst.ReviewFailures {
		if strings.HasPrefix(previous, state+":") && previous != key {
			delete(inst.ReviewFailures, previous)
			delete(inst.ReviewEscalations, previous)
			delete(inst.ReviewRecoveryBudgetStarted, previous)
		}
	}
	if IsCheckpointOutcome(outcome) {
		return reviewFailureContinue
	}
	if !IsCrossStateReworkOutcome(outcome) && !IsVerificationErrorOutcome(outcome) {
		delete(inst.ReviewFailures, key)
		delete(inst.ReviewEscalations, key)
		delete(inst.ReviewRecoveryBudgetStarted, key)
		return reviewFailureContinue
	}
	if inst.ReviewFailures == nil {
		inst.ReviewFailures = map[string]int{}
	}
	if inst.ReviewEscalations[key] > 0 && !inst.ReviewRecoveryBudgetStarted[key] {
		// Older persisted workflows may already have escalated before the
		// recovery-budget reset was deployed. Initialize their fresh budget once.
		inst.ReviewFailures[key] = 0
		if inst.ReviewRecoveryBudgetStarted == nil {
			inst.ReviewRecoveryBudgetStarted = map[string]bool{}
		}
		inst.ReviewRecoveryBudgetStarted[key] = true
	}
	inst.ReviewFailures[key]++
	if inst.ReviewFailures[key] < limit {
		return reviewFailureContinue
	}
	if recoveryState == "" {
		return reviewFailurePause
	}
	if inst.ReviewEscalations == nil {
		inst.ReviewEscalations = map[string]int{}
	}
	if inst.ReviewEscalations[key] == 0 {
		inst.ReviewEscalations[key]++
		inst.ReviewFailures[key] = 0
		return reviewFailureEscalate
	}
	return reviewFailurePause
}

func reviewPhaseKey(phase string) string {
	phase = strings.TrimSpace(phase)
	if phase == "" {
		return "unphased"
	}
	return phase
}

func reviewRecoveryFeedback(state, summary, feedback string, v WorkflowValidation) string {
	var b strings.Builder
	b.WriteString("Repeated review failures reached the automatic retry limit. The Mayor must diagnose the evidence and route one targeted repair before another review cycle.\n\n")
	b.WriteString("Review state: ")
	b.WriteString(state)
	b.WriteString("\nDelivery phase: ")
	b.WriteString(v.ActivePhaseID())
	if summary != "" {
		b.WriteString("\n\nLatest reviewer finding:\n")
		b.WriteString(summary)
	}
	if feedback != "" {
		b.WriteString("\n\nReviewer evidence and prior rework:\n")
		b.WriteString(feedback)
	}
	b.WriteString("\n\nInspect the authoritative source documents and the rejected artifact. Identify the exact contradiction or missing correction, then route to its owner. Do not send work back unchanged. The owner must receive the concrete evidence and the smallest correction needed. If the evidence is insufficient or the contract itself needs a human decision, choose pause.")
	b.WriteString("\n\nOutcomes: planner_rework (correct plan.md/bead mapping), architect_rework (correct architecture/SPEC contract), tester_rework (correct TEST_PLAN.md), implementation_rework (repair owned code/tests), pause (stop and preserve this diagnosis for the user).")
	return truncateWorkflowText(b.String(), maxWorkflowReworkFeedback)
}

func reviewRecoveryPauseFeedback(state, summary, feedback string) string {
	return truncateWorkflowText(fmt.Sprintf("The workflow repeated the %s rejection after a Mayor diagnosis/recovery attempt and is paused. Review the latest evidence, verify the proposed owner/correction, then resume only after the underlying mismatch has been addressed.\n\nLatest finding: %s\n\nRecovery feedback: %s", state, strings.TrimSpace(summary), strings.TrimSpace(feedback)), maxWorkflowReworkFeedback)
}
