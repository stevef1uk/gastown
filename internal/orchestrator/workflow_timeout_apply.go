package orchestrator

import (
	"fmt"
	"strings"

	"github.com/steveyegge/gastown/internal/constants"
	"github.com/steveyegge/gastown/internal/events"
	"github.com/steveyegge/gastown/internal/refinery"
)

// applyStateTimeout runs on_timeout hooks and completes the workflow with outcome timeout.
func (m *Manager) applyStateTimeout(workflowID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	inst, ok := m.instances[workflowID]
	if !ok {
		return "", fmt.Errorf("workflow instance %q not found", workflowID)
	}
	if inst.Status == "paused" {
		return "", fmt.Errorf("%w %q", ErrWorkflowPaused, workflowID)
	}
	tpl := m.templates[inst.TemplateID]
	if tpl == nil {
		return "", fmt.Errorf("template %q not found", inst.TemplateID)
	}
	state, _ := inst.GetCurrentTask(tpl)
	if state.Hooks.ReviewFailureLimit > 0 && state.Hooks.ReviewRecoveryState != "" {
		recovery, ok := tpl.States[state.Hooks.ReviewRecoveryState]
		if !ok || recovery.Role != constants.RoleMayor {
			return "", fmt.Errorf("review recovery state %q for %q must exist and be assigned to mayor", state.Hooks.ReviewRecoveryState, inst.CurrentState)
		}
	}
	if !state.AcceptsOutcome("timeout") {
		return "", fmt.Errorf("state %q does not accept timeout outcome", inst.CurrentState)
	}
	sec := state.Hooks.EffectiveStateTimeoutSeconds()
	fromState := inst.CurrentState
	rig := ""
	if inst.Variables != nil {
		rig = inst.Variables["rig"]
	}
	v := m.workflowValidationFor(inst, tpl)
	var hookLog string
	timeoutHooks := state.Hooks.EffectiveOnStateTimeoutHooks()
	if len(timeoutHooks) > 0 && rig != "" {
		logLine, err := RunOnTimeoutHooks(timeoutHooks, m.townRoot, rig, v)
		if err != nil {
			return "", fmt.Errorf("on_timeout: %w", err)
		}
		hookLog = logLine
	}
	summary := fmt.Sprintf("%s timed out after %ds", fromState, sec)
	feedback := hookLog
	if feedback == "" {
		feedback = "state wall-clock timeout"
	}
	outcome := "timeout"
	if state.Hooks.VerificationErrorLimit > 0 {
		outcome = "verification_error"
	}
	next, err := inst.Transition(tpl, outcome)
	if err != nil {
		return "", err
	}
	reviewAction := inst.recordReviewFailure(fromState, v.ActivePhaseID(), outcome, state.Hooks.ReviewFailureLimit, state.Hooks.ReviewRecoveryState)
	recovery := strings.TrimSpace(state.Hooks.ReviewRecoveryState)
	if reviewAction == reviewFailureEscalate || reviewAction == reviewFailurePause {
		if recovery != "" {
			next = recovery
			inst.CurrentState = next
		}
		if reviewAction == reviewFailurePause {
			inst.Status = "paused"
			inst.PauseReason = fmt.Sprintf("%s timed out %d times in delivery phase %q, including after Mayor diagnosis/recovery; inspect the retained findings and fix before resuming", fromState, state.Hooks.ReviewFailureLimit, v.ActivePhaseID())
		}
	}
	// Hard reset hooks ran; start a fresh wall-clock window (same-state transition does not touch StateEnteredAt).
	inst.touchStateEnteredAt()
	reworkFeedback := PrepareWorkflowReworkFeedback(fromState, next, summary, feedback, v, tpl.ReworkFeedback)
	if reviewAction == reviewFailureEscalate {
		reworkFeedback = reviewRecoveryFeedback(fromState, summary, feedback, v)
	} else if reviewAction == reviewFailurePause {
		reworkFeedback = reviewRecoveryPauseFeedback(fromState, summary, feedback)
	}
	if IsVerificationErrorOutcome(outcome) {
		reworkFeedback = verificationRetryFeedback(v, feedback)
	}
	previousTestRepair := inst.PendingRework
	inst.PendingRework = &WorkflowRework{
		FromState: fromState,
		Outcome:   outcome,
		Summary:   truncateWorkflowText(summary, maxWorkflowReworkSummary),
		Feedback:  reworkFeedback,
		AgentID:   "orchestrator",
	}
	if next == fromState && previousTestRepair != nil && previousTestRepair.FromState == "test_review" && len(previousTestRepair.TestRepairFiles) > 0 {
		inst.PendingRework = previousTestRepair
	}
	inst.recordVerificationError(fromState, outcome, state.Hooks.VerificationErrorLimit)
	if err := m.persistLocked(); err != nil {
		return next, err
	}
	if cerr := refinery.CommitMayorRigOrchestratorCheckpoint(m.townRoot, rig, workflowID, inst.TemplateID, fromState, next, outcome); cerr != nil {
		fmt.Printf("[Manager] Warning: rig-flow mayor/rig git (commit/push): %v\n", cerr)
	}
	m.logWorkflowFeed(events.TypeWorkflowTransition, workflowID, inst.TemplateID, fromState, next, outcome, state.Role, rig)
	fmt.Printf("[Manager] state timeout wf=%s %s -> %s (%s)\n", workflowID, fromState, next, strings.TrimSpace(hookLog))
	return next, nil
}
