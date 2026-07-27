package orchestrator

import (
	"context"
	"log"
	"os/exec"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/steveyegge/gastown/internal/events"
	"github.com/steveyegge/gastown/internal/refinery"
	"github.com/steveyegge/gastown/internal/telemetry"
	"gopkg.in/yaml.v3"
)

// ErrNoTask indicates no workflow task matches this agent.
var ErrNoTask = fmt.Errorf("no task available")

// ErrWorkflowAlreadyActive is returned when StartWorkflow would duplicate a running instance.
var ErrWorkflowAlreadyActive = fmt.Errorf("workflow already active for this template and rig")

// Manager coordinates workflows and templates.
type Manager struct {
	townRoot  string
	templates map[string]*WorkflowTemplate
	instances map[string]*WorkflowInstance
	nextSeq   int
	mu        sync.RWMutex
}

// NewManager creates a new Orchestrator Manager.
func NewManager(townRoot string) *Manager {
	m := &Manager{
		townRoot:  townRoot,
		templates: make(map[string]*WorkflowTemplate),
		instances: make(map[string]*WorkflowInstance),
	}
	_ = m.LoadInstances()
	return m
}

// LoadTownTemplates loads workflow templates from {townRoot}/orchestrator/templates.
func (m *Manager) LoadTownTemplates() error {
	if m.townRoot == "" {
		return nil
	}
	return m.LoadTemplatesFromDir(filepath.Join(m.townRoot, "orchestrator", "templates"))
}

// LoadTemplatesFromDir loads all .yaml templates from a directory.
func (m *Manager) LoadTemplatesFromDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".yaml" {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		var tpl WorkflowTemplate
		if err := yaml.Unmarshal(data, &tpl); err != nil {
			fmt.Printf("[Manager] Warning: skip template %s: %v\n", entry.Name(), err)
			continue
		}
		if tpl.ID == "" {
			fmt.Printf("[Manager] Warning: skip template %s: missing id\n", entry.Name())
			continue
		}
		if warn := validateTemplateSchema(&tpl, entry.Name()); warn != "" {
			fmt.Printf("[Manager] Warning: %s\n", warn)
		}

		m.LoadTemplate(&tpl)
	}
	return nil
}

// LoadTemplate adds a workflow template to the manager.
func (m *Manager) LoadTemplate(t *WorkflowTemplate) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.templates[t.ID] = t
}

// StartWorkflow creates a new workflow instance from a template.
func (m *Manager) StartWorkflow(templateID string, vars map[string]string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	tpl, ok := m.templates[templateID]
	if !ok {
		return "", fmt.Errorf("template %q not found", templateID)
	}
	if vars == nil {
		vars = map[string]string{}
	}
	rig := vars["rig"]
	if m.hasActiveWorkflowLocked(templateID, rig) {
		return "", ErrWorkflowAlreadyActive
	}

	id := m.allocateWorkflowID()
	instance := &WorkflowInstance{
		ID:           id,
		TemplateID:   templateID,
		CurrentState: tpl.InitialState,
		Variables:    vars,
		Status:       "running",
	}
	instance.touchStateEnteredAt()
	m.instances[id] = instance
	if err := m.persistLocked(); err != nil {
		return "", fmt.Errorf("persist instances: %w", err)
	}
	role := ""
	if state, ok := tpl.States[tpl.InitialState]; ok {
		role = state.Role
	}
	m.logWorkflowFeed(events.TypeWorkflowStart, id, templateID, "", tpl.InitialState, "", role, rig)
	return id, nil
}

func (m *Manager) FetchTask(agentID string) (map[string]interface{}, error) {
	fmt.Printf("[Manager] FetchTask for agent: %q\n", agentID)
	for {
		instID, timedOut := m.findTaskInstanceID(agentID)
		if instID == "" {
			return nil, fmt.Errorf("%w for agent %q", ErrNoTask, agentID)
		}
		if timedOut {
			if _, err := m.applyStateTimeout(instID); err != nil {
				fmt.Printf("[Manager] Warning: state timeout %s: %v\n", instID, err)
			}
			continue
		}
		payload, err := m.buildTaskPayloadForInstance(instID)
		if err != nil {
			fmt.Printf("[Manager] Warning: build payload failed for %s: %v\n", instID, err)
			return nil, fmt.Errorf("build payload failed: %w", err)
		}
		return payload, nil
	}
}

func (m *Manager) findTaskInstanceID(agentID string) (instID string, timedOut bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	now := time.Now().UTC()
	for _, inst := range m.instances {
		if isWorkflowTerminalStatus(inst.Status) || inst.Status == "paused" {
			continue
		}
		tpl := m.templates[inst.TemplateID]
		if tpl == nil {
			continue
		}
		state, _ := inst.GetCurrentTask(tpl)
		if state.Role == "" {
			continue
		}
		fmt.Printf("[Manager] Checking WF %s state %s role %s against %s\n",
			inst.ID, inst.CurrentState, state.Role, agentID)
		if !AgentMatchesTask(agentID, state.Role, inst.Variables) {
			continue
		}
		if strings.TrimSpace(inst.StateEnteredAt) == "" {
			inst.touchStateEnteredAt()
		}
		if stateTimedOut(inst, state, now) {
			return inst.ID, true
		}
		return inst.ID, false
	}
	return "", false
}

// reloadInstancesFromDisk merges instances from disk into memory without
// overwriting existing in-memory state. This recovers from external edits to
// instances.json (e.g. from another process or CLI) without restarting.
func (m *Manager) reloadInstancesFromDisk() {
	snap, err := LoadInstancesSnapshot(m.townRoot)
	if err != nil || snap == nil {
		return
	}
	for _, candidate := range snap.Instances {
		if candidate == nil || candidate.ID == "" {
			continue
		}
		if _, exists := m.instances[candidate.ID]; !exists {
			cp := *candidate
			if cp.Variables == nil {
				cp.Variables = map[string]string{}
			}
			m.instances[candidate.ID] = &cp
		}
	}
	if snap.NextSeq > m.nextSeq {
		m.nextSeq = snap.NextSeq
	}
}

func (m *Manager) buildTaskPayloadForInstance(instID string) (map[string]interface{}, error) {
	m.mu.Lock()
	inst := m.instances[instID]
	if inst == nil {
		m.reloadInstancesFromDisk()
		inst = m.instances[instID]
	}
	if inst == nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("workflow instance %q not found", instID)
	}
	tpl := m.templates[inst.TemplateID]
	if tpl == nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("template %q not found", inst.TemplateID)
	}
	state, _ := inst.GetCurrentTask(tpl)
	payload, err := m.BuildTaskPayload(inst, tpl, state)
	m.mu.Unlock()
	return payload, err
}

const maxWorkflowReworkSummary = 2000
const maxWorkflowReworkFeedback = 6000

// CompleteTask transitions a workflow to the next state.
// When agentID is non-empty, it must match the role for the workflow's current state.
// summary and feedback are stored on cross-state failure for the next agent (e.g. QA → polecat).
func (m *Manager) CompleteTask(workflowID string, outcome string, agentID, summary, feedback string) (string, error) {
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
		return "", fmt.Errorf("template %q not found for workflow %q", inst.TemplateID, workflowID)
	}

	state, _ := inst.GetCurrentTask(tpl)
	if agentID != "" && state.Role != "" && !AgentMatchesTask(agentID, state.Role, inst.Variables) {
		return "", fmt.Errorf("agent %q cannot complete state %q (role %q)",
			agentID, inst.CurrentState, state.Role)
	}
	if state.Role != "" && !state.AcceptsOutcome(outcome) {
		return "", fmt.Errorf("outcome %q not allowed for state %q (allowed: %s)",
			outcome, inst.CurrentState, strings.Join(state.AllowedOutcomes(), ", "))
	}

	fromState := inst.CurrentState
	rig := ""
	if inst.Variables != nil {
		rig = inst.Variables["rig"]
	}
	if IsPlanningGateSuccessOutcome(outcome) && IsPlanningGateState(fromState) && rig != "" {
		prof, hasProf, perr := LoadRigWorkflowProfileFile(m.townRoot, rig)
		if perr != nil {
			return "", fmt.Errorf("load rig workflow profile: %w", perr)
		}
		if hasProf && len(prof.ForActivePhase().RequiredFiles) > 0 {
			v := m.workflowValidationFor(inst, tpl)
			switch fromState {
			case "planning", "plan_review", "project_setup":
				if _, err := SyncPlanningArtifacts(m.townRoot, rig, v, true); err != nil {
					if planningSyncNeedsArchitect(err, m.townRoot, rig, v) {
						next, _ := inst.Transition(tpl, "architecture_failure")
						if next == "" {
							next = "design"
						}
						inst.CurrentState = next
						inst.touchStateEnteredAt()
						inst.PendingRework = &WorkflowRework{
							FromState: fromState,
							Outcome:   "architecture_failure",
							Summary:   truncateWorkflowText("plan.md could not be generated — architecture.md is misaligned with SPEC.md", maxWorkflowReworkSummary),
							Feedback:  fmt.Sprintf("The orchestrator could not generate plan.md because architecture.md does not match SPEC.md.\n\nPlease revise architecture.md to align with SPEC.md, then re-run planning.\n\nError: %v", err),
						}
						if perr := m.persistLocked(); perr != nil {
							return "", fmt.Errorf("persist after architecture redirect: %w", perr)
						}
						return next, nil
					}
					return "", fmt.Errorf("sync planning before %s success: %w", fromState, err)
				}
			}
			if err := ValidatePlanningPhaseGate(m.townRoot, rig, fromState, v); err != nil {
				if planningGateNeedsArchitect(err, rig, v, fromState) {
					next, _ := inst.Transition(tpl, "architecture_failure")
					if next == "" {
						next = "design"
					}
					inst.CurrentState = next
					inst.touchStateEnteredAt()
					inst.PendingRework = &WorkflowRework{
						FromState: fromState,
						Outcome:   "architecture_failure",
						Summary:   truncateWorkflowText("planning gate blocked — architecture.md is misaligned with SPEC.md", maxWorkflowReworkSummary),
						Feedback:  fmt.Sprintf("The planning phase gate failed because architecture.md does not match SPEC.md.\n\nPlease revise architecture.md to align with SPEC.md.\n\nError: %v", err),
					}
					if perr := m.persistLocked(); perr != nil {
						return "", fmt.Errorf("persist after architecture redirect: %w", perr)
					}
					return next, nil
				}
				return "", err
			}
		}
	}
	// Reconcile profile with architecture.md backtick paths on design success.
	if fromState == "design" && outcome == "success" && rig != "" {
		ReconcileProfileWithArchitecture(m.townRoot, rig)
	}
	next, err := inst.Transition(tpl, outcome)
	if err != nil {
		return "", err
	}
	// Ensure workflow-profile.json exists before entering design (spec_review -> design).
	// The spec_review agent runs gt rig spec-index which may take 30+ seconds.
	if fromState == "spec_review" && outcome == "success" && next == "design" && rig != "" {
		// Trigger spec-index asynchronously so workflow can proceed while profile is built.
		// Profile path: <townRoot>/<rig>/mayor/rig/.gastown/workflow-profile.json
		go func(townRoot, rigName string) {
			cmd := exec.Command("gt", "rig", "spec-index", rigName)
			cmd.Dir = townRoot
			if err := cmd.Run(); err != nil {
				log.Printf("[req-flow] spec-index after spec_review failed: %v", err)
			} else {
				log.Printf("[req-flow] workflow-profile.json generated for %s", rigName)
			}
		}(m.townRoot, rig)
		// Wait for profile file to exist (up to 120s).
		profilePath := filepath.Join(m.townRoot, rig, "mayor", "rig", ".gastown", "workflow-profile.json")
		deadline := time.Now().Add(120 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(profilePath); err == nil {
				break
			}
			time.Sleep(2 * time.Second)
		}
		if _, err := os.Stat(profilePath); err != nil {
			return "", fmt.Errorf("workflow-profile.json not created after spec_review (timed out): %w", err)
		}
	}
	if fromState == "qa_review" && IsArchitectureReworkOutcome(outcome) && next == "design" && rig != "" {
		if reason := rejectSpuriousArchitectureRework(m.townRoot, rig, summary); reason != "" {
			return "", fmt.Errorf("architecture_failure rejected: %s", reason)
		}
	}
	if fromState == "plan_review" && IsFailureOutcome(outcome) && PlanReviewFailureNeedsArchitect(summary) {
		next = "design"
		inst.CurrentState = next
		inst.touchStateEnteredAt()
	}
	if fromState == "qa_review" && IsFailureOutcome(outcome) && next == "implementation" && rig != "" {
		if reason := rejectSpuriousQAFailure(m.townRoot, rig, summary, feedback); reason != "" {
			return "", fmt.Errorf("qa failure rejected: %s", reason)
		}
	}
	if next == "implementation" && rig != "" && fromState != "implementation" {
		if err := m.prepareImplementationPhase(rig, inst, tpl); err != nil {
			return "", err
		}
	}
	var phaseAdvance *WorkflowRework
	if next == "advance_phase" || (next == "completed" && fromState == "qa_review" && outcome == "all_passed") {
		advRig := ""
		if inst.Variables != nil {
			advRig = inst.Variables["rig"]
		}
		if advRig != "" {
			redirected, fromPhase, toPhase, logLine, advErr := TryAdvanceDeliveryPhaseAfterQA(m.townRoot, advRig)
			if advErr != nil {
				fmt.Printf("[Manager] Warning: delivery phase advance: %v\n", advErr)
			}
			if redirected {
				next = "planning"
				inst.CurrentState = next
				inst.Status = "running"
				inst.touchStateEnteredAt()
				if logLine != "" {
					summary = strings.TrimSpace(logLine + "\n\n" + summary)
				}
				full, ok, _ := LoadRigWorkflowProfileFile(m.townRoot, advRig)
				if ok {
					full.ActivePhaseIDField = toPhase
					phaseAdvance = &WorkflowRework{
						FromState: fromState,
						Outcome:   outcome,
						Summary:   truncateWorkflowText(summary, maxWorkflowReworkSummary),
						Feedback:  truncateWorkflowText(preparePhaseAdvanceToPlanningFeedback(fromPhase, toPhase, full.ForActivePhase()), maxWorkflowReworkFeedback),
					}
				}
				// Fast-forward: if we advanced a phase, check if further phases
				// have no open beads and jump directly to the furthest ready phase.
				if phaseAdvance != nil {
					v := m.workflowValidationFor(inst, tpl)
					ffPhase, ffErr := TryFastForwardDeliveryPhase(m.townRoot, advRig, v)
					if ffErr != nil {
						fmt.Printf("[Manager] Warning: fast-forward delivery phase: %v\n", ffErr)
					} else if ffPhase != "" && ffPhase != toPhase {
						full2, ok2, _ := LoadRigWorkflowProfileFile(m.townRoot, advRig)
						if ok2 && full2.HasPhasedDelivery() {
							inst.CurrentState = "planning"
							inst.Status = "running"
							inst.touchStateEnteredAt()
							full2.ActivePhaseIDField = ffPhase
							phaseAdvance = &WorkflowRework{
								FromState: fromState,
								Outcome:   outcome,
								Summary:   truncateWorkflowText(summary, maxWorkflowReworkSummary),
								Feedback:  truncateWorkflowText(preparePhaseAdvanceToPlanningFeedback(fromPhase, ffPhase, full2.ForActivePhase()), maxWorkflowReworkFeedback),
							}
						}
					}
				}
			}
		}
		if next == "advance_phase" {
			// No more phases (or no rig variable) — complete the workflow.
			next = "completed"
			inst.CurrentState = next
			inst.Status = "completed"
		}
	}
	// Timeout keeps PendingRework even on same-state transitions (planning → planning).
	setRework := IsTimeoutOutcome(outcome) ||
		(next != fromState && (IsFailureOutcome(outcome) || IsArchitectureReworkOutcome(outcome)))
	if setRework && next != "" {
		v := m.workflowValidationFor(inst, tpl)
		reworkFeedback := PrepareWorkflowReworkFeedback(fromState, next, summary, feedback, v)
		rig := ""
		if inst.Variables != nil {
			rig = inst.Variables["rig"]
		}
		if fromState == "qa_review" && next == "implementation" && rig != "" && phaseAdvance == nil {
			reopenText := CombineQAReworkText(summary, feedback)
			if reopened, rerr := ReopenImplementationBeadsAfterQAFailure(m.townRoot, rig, v, reopenText); rerr != nil {
				fmt.Printf("[Manager] Warning: reopen implement beads after QA failure: %v\n", rerr)
			} else if len(reopened) > 0 {
				reworkFeedback = strings.TrimSpace(reworkFeedback + "\n\nAuto-reopened closed implement beads: " + strings.Join(reopened, ", "))
			}
		}
		inst.PendingRework = &WorkflowRework{
			FromState: fromState,
			Outcome:   outcome,
			Summary:   truncateWorkflowText(summary, maxWorkflowReworkSummary),
			Feedback:  reworkFeedback,
			AgentID:   agentID,
		}
	} else if !IsTimeoutOutcome(outcome) && !IsFailureOutcome(outcome) && !IsArchitectureReworkOutcome(outcome) {
		// Success clears QA/plan-review rework for the next agent.
		inst.PendingRework = nil
	}
	if phaseAdvance != nil {
		inst.PendingRework = phaseAdvance
	}
	if err := m.persistLocked(); err != nil {
		return next, fmt.Errorf("persist instances: %w", err)
	}
	if rig == "" && inst.Variables != nil {
		rig = inst.Variables["rig"]
	}
	if cerr := refinery.CommitMayorRigOrchestratorCheckpoint(m.townRoot, rig, workflowID, inst.TemplateID, fromState, next, outcome); cerr != nil {
		fmt.Printf("[Manager] Warning: rig-flow mayor/rig git (commit/push): %v\n", cerr)
	}
	m.logWorkflowFeed(events.TypeWorkflowTransition, workflowID, inst.TemplateID, fromState, next, outcome, state.Role, rig)
	telemetry.RecordWorkflowStateChange(context.Background(), rig, workflowID, fromState, next, nil)
	return next, nil
}

// ResetWorkflow rewinds a workflow instance to an earlier FSM state (default design).
// Use when artifacts were removed or a step needs to be redone.
func (m *Manager) ResetWorkflow(workflowID, toState string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	inst, ok := m.instances[workflowID]
	if !ok {
		return "", fmt.Errorf("workflow instance %q not found", workflowID)
	}
	tpl := m.templates[inst.TemplateID]
	if tpl == nil {
		return "", fmt.Errorf("template %q not found for workflow %q", inst.TemplateID, workflowID)
	}
	if toState == "" {
		toState = "design"
	}
	if _, ok := tpl.States[toState]; !ok {
		return "", fmt.Errorf("state %q not in template %q", toState, inst.TemplateID)
	}

	fromState := inst.CurrentState
	inst.CurrentState = toState
	inst.Status = "running"
	inst.touchStateEnteredAt()
	if err := m.persistLocked(); err != nil {
		return "", fmt.Errorf("persist instances: %w", err)
	}
	rig := ""
	if inst.Variables != nil {
		rig = inst.Variables["rig"]
	}
	role := ""
	if state, _ := inst.GetCurrentTask(tpl); state.Role != "" {
		role = state.Role
	}
	m.logWorkflowFeed(events.TypeWorkflowTransition, workflowID, inst.TemplateID, fromState, toState, "reset", role, rig)
	telemetry.RecordWorkflowStateChange(context.Background(), rig, workflowID, fromState, toState, nil)
	return toState, nil
}

// DeleteWorkflow removes a workflow instance entirely.
func (m *Manager) DeleteWorkflow(workflowID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	inst, ok := m.instances[workflowID]
	if !ok {
		return fmt.Errorf("workflow instance %q not found", workflowID)
	}
	delete(m.instances, workflowID)
	if err := m.persistLocked(); err != nil {
		return fmt.Errorf("persist instances: %w", err)
	}
	rig := ""
	if inst.Variables != nil {
		rig = inst.Variables["rig"]
	}
	m.logWorkflowFeed(events.TypeWorkflowTransition, workflowID, inst.TemplateID, inst.CurrentState, "deleted", "deleted", "", rig)
	return nil
}

func truncateWorkflowText(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 || len(s) <= max {
		return s
	}
	return "...(truncated)\n" + s[len(s)-max:]
}

func (m *Manager) logWorkflowFeed(eventType, workflowID, templateID, fromState, toState, outcome, role, rig string) {
	if m.townRoot == "" {
		return
	}
	actor := "orchestrator"
	if rig != "" {
		actor = rig + "/orchestrator"
	}
	var payload map[string]interface{}
	if eventType == events.TypeWorkflowStart {
		payload = events.WorkflowStartPayload(workflowID, templateID, toState, role, rig)
	} else {
		payload = events.WorkflowTransitionPayload(workflowID, templateID, fromState, toState, outcome, role, rig)
	}
	_ = events.LogFeedAt(m.townRoot, eventType, actor, payload)
}

// WorkflowStatus is a snapshot of one workflow instance for operators and MCP.
type WorkflowStatus struct {
	ID           string            `json:"id"`
	TemplateID   string            `json:"template_id"`
	CurrentState string            `json:"current_state"`
	Status       string            `json:"status"`
	Role         string            `json:"role"`
	Variables    map[string]string `json:"variables"`
}

// GetWorkflowStatus returns status for one workflow, or all when workflowID is empty.
func (m *Manager) GetWorkflowStatus(workflowID string) ([]WorkflowStatus, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var out []WorkflowStatus
	for id, inst := range m.instances {
		if workflowID != "" && id != workflowID {
			continue
		}
		role := ""
		if tpl := m.templates[inst.TemplateID]; tpl != nil {
			if state, _ := inst.GetCurrentTask(tpl); state.Role != "" {
				role = state.Role
			}
		}
		vars := inst.Variables
		if vars == nil {
			vars = map[string]string{}
		}
		out = append(out, WorkflowStatus{
			ID:           inst.ID,
			TemplateID:   inst.TemplateID,
			CurrentState: inst.CurrentState,
			Status:       inst.Status,
			Role:         role,
			Variables:    vars,
		})
	}
	if workflowID != "" && len(out) == 0 {
		return nil, fmt.Errorf("workflow instance %q not found", workflowID)
	}
	return out, nil
}

// HasActiveWorkflow reports whether a non-terminal workflow exists for templateID and rig.
func (m *Manager) HasActiveWorkflow(templateID, rig string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.hasActiveWorkflowLocked(templateID, rig)
}

func (m *Manager) hasActiveWorkflowLocked(templateID, rig string) bool {
	for _, inst := range m.instances {
		if !isWorkflowRunningStatus(inst.Status) {
			continue
		}
		if templateID != "" && inst.TemplateID != templateID {
			continue
		}
		if rig != "" && inst.Variables["rig"] != rig {
			continue
		}
		return true
	}
	return false
}

func (m *Manager) workflowValidationFor(inst *WorkflowInstance, tpl *WorkflowTemplate) WorkflowValidation {
	vars := inst.Variables
	if vars == nil {
		vars = map[string]string{}
	}
	v := DefaultWorkflowValidation()
	if tpl != nil {
		v = mergeValidationFields(v, tpl.Validation.SubstituteVars(vars))
	}
	if rig := vars["rig"]; rig != "" {
		if prof, ok, err := LoadRigWorkflowProfileFile(m.townRoot, rig); err == nil && ok {
			v = mergeValidationFields(v, prof)
		}
		mayorDir := filepath.Join(m.townRoot, rig, "mayor", "rig")
		v = EnrichWorkflowValidationFromArchitecture(v, mayorDir)
	}
	v = v.WithDefaults()
	return v.ForActivePhase()
}

// prepareImplementationPhase canonicalizes plan.md and implement beads before the polecat runs.
// Planners often rewrite plan.md with flattened paths; sync + doc alignment prevents implementation drift.
func (m *Manager) prepareImplementationPhase(rig string, inst *WorkflowInstance, tpl *WorkflowTemplate) error {
	prof, ok, err := LoadRigWorkflowProfileFile(m.townRoot, rig)
	if err != nil {
		return fmt.Errorf("load rig workflow profile: %w", err)
	}
	if !ok || len(prof.ForActivePhase().RequiredFiles) == 0 {
		return nil
	}
	v := m.workflowValidationFor(inst, tpl)
	if _, err := SyncPlanningArtifacts(m.townRoot, rig, v, true); err != nil {
		return fmt.Errorf("sync planning before implementation: %w", err)
	}
	rigDir := filepath.Join(m.townRoot, rig, "mayor", "rig")
	if err := ValidatePlanningDocAlignment(m.townRoot, rigDir, v); err != nil {
		return fmt.Errorf("implementation entry blocked: %w", err)
	}
	if _, err := EnforceSingleImplementInProgress(m.townRoot, rig, v); err != nil {
		return fmt.Errorf("implement bead queue before implementation: %w", err)
	}
	return nil
}

func validateTemplateSchema(tpl *WorkflowTemplate, filename string) string {
	var missing []string
	for name, st := range tpl.States {
		if st.Role == "" && st.PromptFile != "" {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return ""
	}
	return fmt.Sprintf("template %q (%s): states missing role: %s (use role:, not agent_role:)",
		tpl.ID, filename, strings.Join(missing, ", "))
}
