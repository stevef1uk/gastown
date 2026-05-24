package orchestrator

import (
	"fmt"
	"strings"

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
	next, err := inst.Transition(tpl, "timeout")
	if err != nil {
		return "", err
	}
	// Hard reset hooks ran; start a fresh wall-clock window (same-state transition does not touch StateEnteredAt).
	inst.touchStateEnteredAt()
	reworkFeedback := PrepareWorkflowReworkFeedback(fromState, next, summary, feedback, v)
	inst.PendingRework = &WorkflowRework{
		FromState: fromState,
		Outcome:   "timeout",
		Summary:   truncateWorkflowText(summary, maxWorkflowReworkSummary),
		Feedback:  reworkFeedback,
		AgentID:   "orchestrator",
	}
	if err := m.persistLocked(); err != nil {
		return next, err
	}
	if cerr := refinery.CommitMayorRigOrchestratorCheckpoint(m.townRoot, rig, workflowID, inst.TemplateID, fromState, next, "timeout"); cerr != nil {
		fmt.Printf("[Manager] Warning: rig-flow mayor/rig git (commit/push): %v\n", cerr)
	}
	m.logWorkflowFeed(events.TypeWorkflowTransition, workflowID, inst.TemplateID, fromState, next, "timeout", state.Role, rig)
	fmt.Printf("[Manager] state timeout wf=%s %s -> %s (%s)\n", workflowID, fromState, next, strings.TrimSpace(hookLog))
	return next, nil
}
