package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/steveyegge/gastown/internal/events"
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

// FetchTask finds the next available task for an agent.
func (m *Manager) FetchTask(agentID string) (map[string]interface{}, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	fmt.Printf("[Manager] FetchTask for agent: %q\n", agentID)

	for _, inst := range m.instances {
		if inst.Status == "completed" || inst.Status == "failed" {
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
		payload, err := m.BuildTaskPayload(inst, tpl, state)
		if err != nil {
			fmt.Printf("[Manager] Warning: %v\n", err)
			continue
		}
		return payload, nil
	}

	return nil, fmt.Errorf("%w for agent %q", ErrNoTask, agentID)
}

// CompleteTask transitions a workflow to the next state.
// When agentID is non-empty, it must match the role for the workflow's current state.
func (m *Manager) CompleteTask(workflowID string, outcome string, agentID string) (string, error) {
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
	next, err := inst.Transition(tpl, outcome)
	if err != nil {
		return "", err
	}
	if err := m.persistLocked(); err != nil {
		return next, fmt.Errorf("persist instances: %w", err)
	}
	rig := ""
	if inst.Variables != nil {
		rig = inst.Variables["rig"]
	}
	m.logWorkflowFeed(events.TypeWorkflowTransition, workflowID, inst.TemplateID, fromState, next, outcome, state.Role, rig)
	return next, nil
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
		if inst.Status == "completed" || inst.Status == "failed" {
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

func validateTemplateSchema(tpl *WorkflowTemplate, filename string) string {
	var missing []string
	for name, st := range tpl.States {
		if st.Role == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return ""
	}
	return fmt.Sprintf("template %q (%s): states missing role: %s (use role:, not agent_role:)",
		tpl.ID, filename, strings.Join(missing, ", "))
}
