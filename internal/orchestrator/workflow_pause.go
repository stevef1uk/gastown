package orchestrator

import (
	"fmt"

	"github.com/steveyegge/gastown/internal/events"
)

// ErrWorkflowPaused is returned when an operation requires a running workflow.
var ErrWorkflowPaused = fmt.Errorf("workflow is paused")

// ErrWorkflowNotPaused is returned when resume is called on a non-paused instance.
var ErrWorkflowNotPaused = fmt.Errorf("workflow is not paused")

func isWorkflowTerminalStatus(status string) bool {
	switch status {
	case "completed", "failed":
		return true
	default:
		return false
	}
}

func isWorkflowRunningStatus(status string) bool {
	return status == "" || status == "running"
}

// PauseWorkflow marks an instance paused so fetch_task stops dispatching work.
func (m *Manager) PauseWorkflow(workflowID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	inst, ok := m.instances[workflowID]
	if !ok {
		return "", fmt.Errorf("workflow instance %q not found", workflowID)
	}
	if isWorkflowTerminalStatus(inst.Status) {
		return "", fmt.Errorf("workflow %q is %s (cannot pause)", workflowID, inst.Status)
	}
	if inst.Status == "paused" {
		rig := rigFromInst(inst)
		return rig, nil
	}
	inst.Status = "paused"
	if err := m.persistLocked(); err != nil {
		return "", fmt.Errorf("persist instances: %w", err)
	}
	rig := rigFromInst(inst)
	m.logWorkflowFeed(events.TypeWorkflowTransition, workflowID, inst.TemplateID, inst.CurrentState, inst.CurrentState, "paused", "", rig)
	return rig, nil
}

// ResumeWorkflow marks a paused instance running again.
func (m *Manager) ResumeWorkflow(workflowID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	inst, ok := m.instances[workflowID]
	if !ok {
		return fmt.Errorf("workflow instance %q not found", workflowID)
	}
	if inst.Status != "paused" {
		return fmt.Errorf("%w (status=%q)", ErrWorkflowNotPaused, inst.Status)
	}
	inst.Status = "running"
	if err := m.persistLocked(); err != nil {
		return fmt.Errorf("persist instances: %w", err)
	}
	rig := rigFromInst(inst)
	m.logWorkflowFeed(events.TypeWorkflowTransition, workflowID, inst.TemplateID, inst.CurrentState, inst.CurrentState, "resumed", "", rig)
	return nil
}

// PauseRunningWorkflowsForRig pauses all non-terminal, non-paused instances for rig.
func (m *Manager) PauseRunningWorkflowsForRig(rig string) ([]string, error) {
	if rig == "" {
		return nil, fmt.Errorf("rig name required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	var ids []string
	for id, inst := range m.instances {
		if isWorkflowTerminalStatus(inst.Status) || inst.Status == "paused" {
			continue
		}
		if rigFromInst(inst) != rig {
			continue
		}
		inst.Status = "paused"
		ids = append(ids, id)
		m.logWorkflowFeed(events.TypeWorkflowTransition, id, inst.TemplateID, inst.CurrentState, inst.CurrentState, "paused", "", rig)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("no running workflows for rig %q", rig)
	}
	if err := m.persistLocked(); err != nil {
		return nil, fmt.Errorf("persist instances: %w", err)
	}
	return ids, nil
}

func rigFromInst(inst *WorkflowInstance) string {
	if inst == nil || inst.Variables == nil {
		return ""
	}
	return inst.Variables["rig"]
}
