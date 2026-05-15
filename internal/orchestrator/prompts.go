package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// IsPipelineRole reports roles that participate in orchestrator rig-flow FSM tasks.
func IsPipelineRole(role string) bool {
	switch role {
	case "mayor", "architect", "planner", "polecat", "qa":
		return true
	default:
		return false
	}
}

// IsPatrolRole reports roles that must never use orchestrator idle polling.
func IsPatrolRole(role string) bool {
	switch role {
	case "witness", "refinery", "mechanic", "deacon", "boot", "dog", "crew":
		return true
	default:
		return false
	}
}

// OrchestratedForRole is true when the agent should run gt-agent --orchestrated.
// Patrol roles (witness, refinery, mechanic, deacon, …) always return false.
// Per-bead polecat sessions are never orchestrated; only town hq-polecat is.
func OrchestratedForRole(orchestratorRunning bool, role string) bool {
	if !orchestratorRunning || IsPatrolRole(role) {
		return false
	}
	return IsPipelineRole(role)
}

// OrchestratedForTownPolecat is true for the single town-level rig-flow polecat session.
func OrchestratedForTownPolecat(orchestratorRunning bool) bool {
	return orchestratorRunning
}

// SubstituteVars replaces {{key}} placeholders in text.
func SubstituteVars(text string, vars map[string]string) string {
	if text == "" || len(vars) == 0 {
		return text
	}
	out := text
	for k, v := range vars {
		out = strings.ReplaceAll(out, "{{"+k+"}}", v)
	}
	return out
}

// LoadPromptFile reads a prompt file relative to {townRoot}/orchestrator/.
func LoadPromptFile(townRoot, promptFile string, vars map[string]string) (string, error) {
	if promptFile == "" {
		return "", nil
	}
	path := filepath.Join(townRoot, "orchestrator", filepath.FromSlash(promptFile))
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read prompt file %q: %w", promptFile, err)
	}
	return SubstituteVars(string(data), vars), nil
}

// rigScopedPipelineRoles require agent_id "{rig}/{role}" when the workflow sets rig.
var rigScopedPipelineRoles = map[string]bool{
	"architect": true,
	"planner":   false, // town hq-planner is not rig-prefixed
	"polecat":   true,
	"qa":        true,
}

// AgentMatchesTask reports whether agentID may claim a task for role in a workflow instance.
func AgentMatchesTask(agentID, role string, vars map[string]string) bool {
	if agentID == "any" {
		return true
	}
	rig := vars["rig"]
	if strings.HasSuffix(agentID, "/"+role) {
		if rig == "" {
			return true
		}
		prefix := strings.TrimSuffix(agentID, "/"+role)
		return prefix == rig
	}
	if agentID != role {
		return false
	}
	// Bare role id (e.g. mayor, planner at town level).
	if rig == "" || !rigScopedPipelineRoles[role] {
		return true
	}
	return false
}

// BuildTaskPayload assembles the fetch_task response map.
func (m *Manager) BuildTaskPayload(inst *WorkflowInstance, tpl *WorkflowTemplate, state State) (map[string]interface{}, error) {
	vars := inst.Variables
	if vars == nil {
		vars = map[string]string{}
	}
	systemPrompt, err := LoadPromptFile(m.townRoot, state.PromptFile, vars)
	if err != nil {
		return nil, err
	}
	taskPrompt := SubstituteVars(state.Instructions, vars)
	if systemPrompt == "" && taskPrompt == "" {
		return nil, fmt.Errorf("state %q has no prompt_file or instructions", inst.CurrentState)
	}

	return map[string]interface{}{
		"workflow_id":       inst.ID,
		"template_id":       inst.TemplateID,
		"state":             inst.CurrentState,
		"role":              state.Role,
		"system_prompt":     systemPrompt,
		"task_prompt":       taskPrompt,
		"instructions":      taskPrompt,
		"allowed_outcomes":  state.AllowedOutcomes(),
	}, nil
}
