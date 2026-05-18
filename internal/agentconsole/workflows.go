package agentconsole

import (
	"fmt"
	"strings"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

// WorkflowInfo is a snapshot of one workflow instance for the console UI.
type WorkflowInfo struct {
	ID           string `json:"id"`
	TemplateID   string `json:"template_id"`
	Rig          string `json:"rig"`
	CurrentState string `json:"current_state"`
	Status       string `json:"status"`
	ActiveRole   string `json:"active_role,omitempty"`
}

// rigFlowStateRole maps rig-flow FSM states to the agent role that owns the step.
var rigFlowStateRole = map[string]string{
	"kickoff":        "mayor",
	"design":         "architect",
	"planning":       "planner",
	"project_setup":  "setup",
	"implementation": "polecat",
	"qa_review":      "qa",
	"completed":      "",
}

func (s *Server) loadWorkflows() []WorkflowInfo {
	snap, err := orchestrator.LoadInstancesSnapshot(s.townRoot)
	if err != nil || snap == nil {
		return nil
	}
	var out []WorkflowInfo
	for _, inst := range snap.Instances {
		if inst == nil {
			continue
		}
		w := WorkflowInfo{
			ID:           inst.ID,
			TemplateID:   inst.TemplateID,
			Rig:          inst.Variables["rig"],
			CurrentState: inst.CurrentState,
			Status:       inst.Status,
		}
		if inst.TemplateID == "rig-flow" {
			w.ActiveRole = rigFlowStateRole[inst.CurrentState]
		}
		out = append(out, w)
	}
	return out
}

func enrichAgentsWithWorkflows(agents []Agent, workflows []WorkflowInfo) {
	for i := range agents {
		for _, w := range workflows {
			if w.Status != "running" {
				continue
			}
			if agents[i].Role != w.ActiveRole || w.ActiveRole == "" {
				continue
			}
			// Town-level pipeline agents (mayor, planner, setup) have no rig on the Agent row.
			if agents[i].Rig == "" {
				switch agents[i].Role {
				case "mayor", "planner", "setup":
					agents[i].WorkflowID = w.ID
					agents[i].WorkflowState = w.CurrentState
					agents[i].WorkflowActive = true
				}
				continue
			}
			if w.Rig != "" && agents[i].Rig == w.Rig {
				agents[i].WorkflowID = w.ID
				agents[i].WorkflowState = w.CurrentState
				agents[i].WorkflowActive = true
			}
		}
	}
}

func orchestratorActivity(workflows []WorkflowInfo) string {
	var running []WorkflowInfo
	for _, w := range workflows {
		if w.Status == "running" {
			running = append(running, w)
		}
	}
	switch len(running) {
	case 0:
		return "Idle"
	case 1:
		w := running[0]
		if w.Rig != "" {
			return fmt.Sprintf("%s on %s (%s)", w.ID, w.CurrentState, w.Rig)
		}
		return fmt.Sprintf("%s (%s)", w.ID, w.CurrentState)
	default:
		parts := make([]string, 0, len(running))
		for _, w := range running {
			if w.Rig != "" {
				parts = append(parts, w.ID+":"+w.CurrentState+"@"+w.Rig)
			} else {
				parts = append(parts, w.ID+":"+w.CurrentState)
			}
		}
		return strings.Join(parts, ", ")
	}
}
