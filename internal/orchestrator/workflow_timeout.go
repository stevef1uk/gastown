package orchestrator

import (
	"fmt"
	"strings"
	"time"
)

// IsTimeoutOutcome reports whether complete_task outcome is a state timeout transition.
func IsTimeoutOutcome(outcome string) bool {
	return strings.EqualFold(strings.TrimSpace(outcome), "timeout")
}

func (wi *WorkflowInstance) touchStateEnteredAt() {
	wi.StateEnteredAt = time.Now().UTC().Format(time.RFC3339)
}

func parseStateEnteredAt(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// stateTimedOut reports whether the instance has exceeded the state's wall-clock limit.
func stateTimedOut(inst *WorkflowInstance, state State, now time.Time) bool {
	sec := state.Hooks.EffectiveStateTimeoutSeconds()
	if sec <= 0 {
		return false
	}
	entered, ok := parseStateEnteredAt(inst.StateEnteredAt)
	if !ok {
		return false
	}
	return now.Sub(entered) >= time.Duration(sec)*time.Second
}

// RunOnTimeoutHook runs hooks.on_timeout steps from rig-flow.yaml (e.g. reset_planning_phase).
func RunOnTimeoutHook(step, townRoot, rig string, v WorkflowValidation) (string, error) {
	switch step {
	case "reset_planning_phase", "sync_planning_on_timeout":
		// Deterministic repair (canonical beads + plan.md); avoid ResetPlanningPhase flat recreate.
		logLine, err := SyncPlanningArtifacts(townRoot, rig, v, true)
		if err != nil {
			return "", err
		}
		if logLine == "" {
			logLine = "synced planning artifacts (timeout recovery)"
		}
		return logLine, nil
	case "recover_implementation_stall":
		return RecoverImplementationStall(townRoot, rig, v)
	case "reset_implementation_phase":
		return ResetImplementationPhase(townRoot, rig, v)
	case "hard_reset_implementation_phase":
		return HardResetImplementationPhase(townRoot, rig, v)
	default:
		return "", fmt.Errorf("unknown on_timeout hook %q", step)
	}
}

// RunOnTimeoutHooks runs all on_timeout hooks for a state; returns a combined log line.
func RunOnTimeoutHooks(steps []string, townRoot, rig string, v WorkflowValidation) (string, error) {
	var parts []string
	for _, step := range steps {
		step = strings.TrimSpace(step)
		if step == "" {
			continue
		}
		logLine, err := RunOnTimeoutHook(step, townRoot, rig, v)
		if err != nil {
			return joinStrings(parts, "; "), err
		}
		if logLine != "" {
			parts = append(parts, logLine)
		}
	}
	return joinStrings(parts, "; "), nil
}
