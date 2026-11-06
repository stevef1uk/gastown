package feed

import (
	"sort"
	"strings"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

// RigWorkflow is one workflow instance shown under a rig in the agent tree.
type RigWorkflow struct {
	PauseReason        string
	ID                 string
	TemplateID         string
	DeliveryPhase      string
	DeliveryPhaseIndex int
	DeliveryPhaseTotal int
	CurrentState       string
	Status             string
}

// LoadWorkflowsByRig returns workflow instances grouped by rig name.
// Uses the live orchestrator when running; otherwise reads orchestrator/instances.json.
func LoadWorkflowsByRig(townRoot string) (map[string][]RigWorkflow, error) {
	statuses, err := loadWorkflowStatuses(townRoot)
	if err != nil {
		return nil, err
	}
	byRig := make(map[string][]RigWorkflow)
	type phaseProgress struct {
		id    string
		index int
		total int
	}
	phasesByRig := make(map[string]phaseProgress)
	for _, s := range statuses {
		rig := strings.TrimSpace(s.Variables["rig"])
		if rig == "" {
			continue
		}
		phase, loaded := phasesByRig[rig]
		if !loaded {
			if profile, ok, err := orchestrator.LoadRawRigWorkflowProfile(townRoot, rig); err == nil && ok {
				if active, ok := profile.ActivePhase(); ok {
					phase.id = strings.TrimSpace(active.ID)
					phase.total = len(profile.DeliveryPhases)
					for i, p := range profile.DeliveryPhases {
						if strings.TrimSpace(p.ID) == phase.id {
							phase.index = i + 1
							break
						}
					}
				}
			}
			phasesByRig[rig] = phase
		}
		byRig[rig] = append(byRig[rig], RigWorkflow{
			ID:                 s.ID,
			TemplateID:         s.TemplateID,
			DeliveryPhase:      phase.id,
			DeliveryPhaseIndex: phase.index,
			DeliveryPhaseTotal: phase.total,
			CurrentState:       s.CurrentState,
			Status:             s.Status,
			PauseReason:        s.PauseReason,
		})
	}
	for rig := range byRig {
		sort.Slice(byRig[rig], func(i, j int) bool {
			if byRig[rig][i].ID != byRig[rig][j].ID {
				return byRig[rig][i].ID < byRig[rig][j].ID
			}
			return byRig[rig][i].TemplateID < byRig[rig][j].TemplateID
		})
	}
	return byRig, nil
}

func loadWorkflowStatuses(townRoot string) ([]orchestrator.WorkflowStatus, error) {
	if townRoot == "" {
		return nil, nil
	}
	if running, _, _ := orchestrator.IsRunning(townRoot); running {
		if live, err := orchestrator.GetWorkflowStatuses(townRoot, ""); err == nil {
			return live, nil
		}
	}
	snap, err := orchestrator.LoadInstancesSnapshot(townRoot)
	if err != nil {
		return nil, err
	}
	if snap == nil || len(snap.Instances) == 0 {
		return nil, nil
	}
	out := make([]orchestrator.WorkflowStatus, 0, len(snap.Instances))
	for _, inst := range snap.Instances {
		if inst == nil || inst.ID == "" {
			continue
		}
		vars := inst.Variables
		if vars == nil {
			vars = map[string]string{}
		}
		out = append(out, orchestrator.WorkflowStatus{
			ID:           inst.ID,
			TemplateID:   inst.TemplateID,
			CurrentState: inst.CurrentState,
			Status:       inst.Status,
			PauseReason:  inst.PauseReason,
			Variables:    vars,
		})
	}
	return out, nil
}
