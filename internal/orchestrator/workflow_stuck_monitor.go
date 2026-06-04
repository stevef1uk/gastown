package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// PolecatSessionChecker reports whether the rig pipeline polecat tmux session is running.
type PolecatSessionChecker func(rig string) bool

// RunWorkflowStuckMonitorTick scans active rig-flow instances and applies repair when stuck.
// Returns human-readable log lines for the daemon (may be empty).
func RunWorkflowStuckMonitorTick(townRoot string, polecatRunning PolecatSessionChecker) []string {
	cfg := WorkflowStuckConfigFromEnv()
	if !cfg.Enabled || townRoot == "" {
		return nil
	}
	orchRunning, _, _ := IsRunning(townRoot)
	if !orchRunning {
		return nil
	}
	snap, err := LoadInstancesSnapshot(townRoot)
	if err != nil || snap == nil {
		return nil
	}
	now := time.Now().UTC()
	stateSnap := loadWorkflowStuckState(townRoot)
	var lines []string
	changed := false

	for _, inst := range snap.Instances {
		if inst == nil || !isWorkflowRunningStatus(inst.Status) {
			continue
		}
		if inst.TemplateID != "rig-flow" {
			continue
		}
		rig := ""
		if inst.Variables != nil {
			rig = inst.Variables["rig"]
		}
		if rig == "" {
			continue
		}
		if RigWorkflowActivityForRig(townRoot, rig) != RigWorkflowRunning {
			continue
		}

		v := workflowValidationForInstance(townRoot, inst)
		fp, err := ImplementBeadProgressFingerprint(townRoot, rig, v)
		if err != nil {
			lines = append(lines, fmt.Sprintf("[workflow-stuck] %s: bead fingerprint: %v", rig, err))
			continue
		}
		nonReq, err := CountNonRequiredOpenImplementBeads(townRoot, rig, v)
		if err != nil {
			lines = append(lines, fmt.Sprintf("[workflow-stuck] %s: count flat beads: %v", rig, err))
			continue
		}
		rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
		missingContract := PlanMissingIntegrationContract(rigDir, v)

		st := stateSnap.rigState(rig)
		lastFP := st.LastBeadFingerprint
		polecatUp := true
		if polecatRunning != nil {
			polecatUp = polecatRunning(rig)
		}
		eval := EvalWorkflowStuck(WorkflowStuckEvalInput{
			Now:                  now,
			Config:               cfg,
			CurrentState:         inst.CurrentState,
			StateEnteredAt:       inst.StateEnteredAt,
			PendingRework:        inst.PendingRework != nil,
			BeadFingerprint:      fp,
			LastBeadFingerprint:  lastFP,
			PolecatRunning:       polecatUp,
			NonRequiredBeadCount: nonReq,
			MissingIntegration:   missingContract,
		})

		if fp != "" && fp != lastFP {
			st.LastBeadFingerprint = fp
			changed = true
		}

		if !eval.Stuck {
			continue
		}
		if !repairCooldownElapsed(st, cfg.RepairCooldown, now) {
			continue
		}

		repairLog, err := RunWorkflowStuckRepair(townRoot, rig, v, eval.Signals)
		if err != nil {
			lines = append(lines, fmt.Sprintf("[workflow-stuck] %s: repair failed (%s): %v", rig, eval.Detail, err))
			continue
		}
		st.LastRepairAt = now
		sigNames := signalNames(eval.Signals)
		st.LastSignals = sigNames
		changed = true
		stepSummary := ""
		if repairLog != nil && len(repairLog.Steps) > 0 {
			stepSummary = " — " + joinStrings(repairLog.Steps, "; ")
		}
		lines = append(lines, fmt.Sprintf("[workflow-stuck] %s: repaired (%s): %s%s",
			rig, strings.Join(sigNames, ","), eval.Detail, stepSummary))
	}

	if changed {
		_ = saveWorkflowStuckState(townRoot, stateSnap)
	}
	return lines
}

func workflowValidationForInstance(townRoot string, inst *WorkflowInstance) WorkflowValidation {
	vars := inst.Variables
	if vars == nil {
		vars = map[string]string{}
	}
	v := DefaultWorkflowValidation()
	tpl, _ := loadWorkflowTemplateFromTown(townRoot, inst.TemplateID)
	if tpl != nil {
		v = mergeValidationFields(v, tpl.Validation.SubstituteVars(vars))
	}
	if rig := vars["rig"]; rig != "" {
		if prof, ok, err := LoadRigWorkflowProfileFile(townRoot, rig); err == nil && ok {
			v = mergeValidationFields(v, prof)
		}
		mayorDir := filepath.Join(townRoot, rig, "mayor", "rig")
		v = EnrichWorkflowValidationFromArchitecture(v, mayorDir)
	}
	return v.ForActivePhase()
}

func loadWorkflowTemplateFromTown(townRoot, templateID string) (*WorkflowTemplate, error) {
	if templateID == "" {
		return nil, nil
	}
	path := filepath.Join(townRoot, "orchestrator", "templates", templateID+".yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var tpl WorkflowTemplate
	if err := yaml.Unmarshal(data, &tpl); err != nil {
		return nil, err
	}
	return &tpl, nil
}

func signalNames(signals []WorkflowStuckSignal) []string {
	out := make([]string, 0, len(signals))
	for _, s := range signals {
		out = append(out, string(s))
	}
	return out
}
