package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// WorkflowStuckSignal names a deterministic stuck condition for rig-flow.
type WorkflowStuckSignal string

const (
	SignalPhaseIdleNoBeadProgress    WorkflowStuckSignal = "phase_idle_no_bead_progress"
	SignalPendingReworkLinger        WorkflowStuckSignal = "pending_rework_linger"
	SignalPolecatSessionMissing      WorkflowStuckSignal = "polecat_session_missing"
	SignalNonRequiredImplementBeads  WorkflowStuckSignal = "non_required_implement_beads"
	SignalMissingIntegrationContract WorkflowStuckSignal = "missing_integration_contract"
	SignalPlanningDocsMisaligned       WorkflowStuckSignal = "planning_docs_misaligned"
)

// WorkflowStuckEvalInput is the pure snapshot for stuck detection (testable without daemon).
type WorkflowStuckEvalInput struct {
	Now                 time.Time
	Config              WorkflowStuckConfig
	CurrentState        string
	StateEnteredAt      string
	PendingRework       bool
	BeadFingerprint     string
	LastBeadFingerprint   string
	PolecatRunning      bool
	NonRequiredBeadCount  int
	MissingIntegration    bool
	PlanningDocsMisaligned bool
}

// WorkflowStuckEvalResult lists fired signals and whether repair should run.
type WorkflowStuckEvalResult struct {
	Stuck   bool
	Signals []WorkflowStuckSignal
	Detail  string
}

// EvalWorkflowStuck applies rig-flow stuck heuristics to a workflow snapshot.
func EvalWorkflowStuck(in WorkflowStuckEvalInput) WorkflowStuckEvalResult {
	var signals []WorkflowStuckSignal
	var parts []string
	now := in.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	entered, hasEntered := parseStateEnteredAt(in.StateEnteredAt)
	inState := time.Duration(0)
	if hasEntered {
		inState = now.Sub(entered)
	}
	grace := in.Config.StateGrace
	if grace <= 0 {
		grace = 10 * time.Minute
	}

	if in.PendingRework && hasEntered && inState >= in.Config.ReworkLinger {
		signals = append(signals, SignalPendingReworkLinger)
		parts = append(parts, fmt.Sprintf("pending_rework for %s", inState.Round(time.Second)))
	}

	if beadProgressState(in.CurrentState) && hasEntered && inState > grace {
		if in.BeadFingerprint != "" && in.BeadFingerprint == in.LastBeadFingerprint && inState >= in.Config.IdleAfter {
			signals = append(signals, SignalPhaseIdleNoBeadProgress)
			parts = append(parts, fmt.Sprintf("no bead progress in %s for %s", in.CurrentState, inState.Round(time.Second)))
		}
	}

	if in.CurrentState == "implementation" && hasEntered && inState > 5*time.Minute && !in.PolecatRunning {
		signals = append(signals, SignalPolecatSessionMissing)
		parts = append(parts, "implementation active but rig polecat session not running")
	}

	if in.NonRequiredBeadCount > 0 && planningOrImplementationState(in.CurrentState) {
		signals = append(signals, SignalNonRequiredImplementBeads)
		parts = append(parts, fmt.Sprintf("%d open/in_progress implement bead(s) off required_files", in.NonRequiredBeadCount))
	}

	if in.MissingIntegration && planningStateNeedsContract(in.CurrentState) {
		signals = append(signals, SignalMissingIntegrationContract)
		parts = append(parts, "plan.md missing Integration contract (server profile)")
	}

	if in.PlanningDocsMisaligned && planningOrImplementationState(in.CurrentState) {
		signals = append(signals, SignalPlanningDocsMisaligned)
		parts = append(parts, "SPEC/architecture/plan doc alignment failed")
	}

	if len(signals) == 0 {
		return WorkflowStuckEvalResult{}
	}
	return WorkflowStuckEvalResult{
		Stuck:   true,
		Signals: signals,
		Detail:  strings.Join(parts, "; "),
	}
}

func beadProgressState(state string) bool {
	switch state {
	case "planning", "plan_review", "project_setup", "implementation", "qa_review":
		return true
	default:
		return false
	}
}

func planningOrImplementationState(state string) bool {
	switch state {
	case "planning", "plan_review", "project_setup", "implementation":
		return true
	default:
		return false
	}
}

func planningStateNeedsContract(state string) bool {
	switch state {
	case "planning", "plan_review", "project_setup", "implementation":
		return true
	default:
		return false
	}
}

// ImplementBeadProgressFingerprint returns a stable signature of implement-bead queue state.
func ImplementBeadProgressFingerprint(townRoot, rig string, v WorkflowValidation) (string, error) {
	if !BeadsDatabaseReady(townRoot, rig) {
		return "", nil
	}
	v = v.ForActivePhase()
	var parts []string
	for _, status := range []string{"open", "in_progress", "closed"} {
		beads, err := listImplementBeadsByStatus(townRoot, rig, v, status)
		if err != nil {
			return "", err
		}
		for _, b := range beads {
			if b.ID == "" {
				continue
			}
			parts = append(parts, status+":"+b.ID+":"+strings.TrimSpace(b.Title))
		}
	}
	if len(parts) == 0 {
		return "", nil
	}
	sort.Strings(parts)
	return strings.Join(parts, "|"), nil
}

// CountNonRequiredOpenImplementBeads counts open/in_progress implement-like beads not in required_files.
func CountNonRequiredOpenImplementBeads(townRoot, rig string, v WorkflowValidation) (int, error) {
	if !BeadsDatabaseReady(townRoot, rig) {
		return 0, nil
	}
	v = v.ForActivePhase()
	if len(v.RequiredFiles) == 0 {
		return 0, nil
	}
	beads, err := listImplementBeadsForPrune(townRoot, rig, v)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, b := range beads {
		if !looksLikeOpenImplementBeadTitle(b.Title, v) {
			continue
		}
		p := NormalizePlannerBeadPath(ExtractPathFromBeadTitle(b.Title, v.BeadTitleContains), v.LayoutRoot, rig)
		if !IsValidImplementBeadPath(p) || pathMatchesRequiredForProfile(p, v.RequiredFiles, v) {
			continue
		}
		n++
	}
	return n, nil
}

// PlanMissingIntegrationContract reports whether plan.md lacks ## Integration contract for server rigs.
func PlanMissingIntegrationContract(rigDir string, v WorkflowValidation) bool {
	if !profileHasServerEntrypoint(v) {
		return false
	}
	rigDir = strings.TrimSpace(rigDir)
	if rigDir == "" {
		return false
	}
	data, err := os.ReadFile(filepath.Join(rigDir, "plan.md"))
	if err != nil {
		return true
	}
	return ExtractSpecMarkdownSection(string(data), "Integration contract") == ""
}
