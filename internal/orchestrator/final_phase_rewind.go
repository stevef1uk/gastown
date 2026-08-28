package orchestrator

import (
	"fmt"
	"path/filepath"
	"strings"
)

// RewindToPhaseForClosedFile returns the rig to the delivery phase that owns the given closed
// implement file so its bead can be reopened and the file repaired. Beads can only be opened in
// the current phase: an earlier-phase bead cannot be reopened while a later phase is active, so
// the workflow must return to the owning phase instead of dead-ending on the closed-bead write
// guard. rewound_from_phase_id is set so phase advancement jumps back to the original phase once
// the repair phase completes. Returns "" when the file is not owned by an earlier (completed)
// phase or has an open bead.
func RewindToPhaseForClosedFile(townRoot, rig, filePath string, v WorkflowValidation) (string, error) {
	if townRoot == "" || rig == "" {
		return "", nil
	}
	if !v.HasPhasedDelivery() {
		return "", nil
	}
	filePath = NormalizePlannerBeadPath(filePath, v.LayoutRoot, rig)
	if filePath == "" {
		return "", nil
	}
	idx := v.FindDeliveryPhaseForFile(filePath)
	activeIdx := v.ActivePhaseIndex()
	if idx < 0 || activeIdx < 0 || idx >= activeIdx {
		return "", nil // not owned by an earlier phase
	}
	closedOnly, err := ImplementPathHasOnlyClosedBeads(townRoot, rig, filePath, v)
	if err != nil || !closedOnly {
		return "", err
	}
	targetPhase := strings.TrimSpace(v.DeliveryPhases[idx].ID)
	originalPhase := v.ActivePhaseID()
	if targetPhase == "" || originalPhase == "" || originalPhase == targetPhase {
		return "", nil
	}
	_ = SetRigRewoundFromPhase(townRoot, rig, originalPhase)

	if err := SetRigActivePhase(townRoot, rig, targetPhase); err != nil {
		return "", fmt.Errorf("closed file %s needs repair but could not rewind active phase to %s: %w", filePath, targetPhase, err)
	}

	reloaded, _, err := LoadRigWorkflowProfileFile(townRoot, rig)
	if err != nil {
		return "", fmt.Errorf("rewound active phase to %s for %s but failed to reload profile: %w", targetPhase, filePath, err)
	}
	reopened, err := EnsurePlanningImplementBeads(townRoot, rig, reloaded.ForActivePhase())
	if err != nil {
		return "", fmt.Errorf("rewound active phase to %s for %s but failed to reopen implement beads: %w", targetPhase, filePath, err)
	}
	_ = commitDoltWorkingSet(townRoot, rig) // best-effort commit of bead state changes

	logLine := fmt.Sprintf("rewound active phase %s → %s to repair closed implement file %s", originalPhase, targetPhase, filePath)
	if len(reopened) > 0 {
		logLine += " (reopened beads: " + strings.Join(reopened, ", ") + ")"
	}
	return logLine, nil
}

// rewindProblemPhaseMode selects which delivery phases a rewind scan considers.
type rewindProblemPhaseMode int

const (
	// rewindPastAndPresentOnly scans only completed phases (index < active). This is what QA
	// uses when deciding whether it is safe for the active phase to progress: a phase that was
	// marked complete without its files ever being written must be rewound and repaired first.
	rewindPastAndPresentOnly rewindProblemPhaseMode = iota
	// rewindAllPhases scans every delivery phase. This is meaningful only in the final phase,
	// where all earlier phases are already past and none legitimately lack files yet.
	rewindAllPhases
)

// rewindProblemPhases is the shared implementation behind the final-phase and QA rewinds. It
// scans the selected delivery phases (earliest first) and, when the first problematic phase has
// a required file that is missing or stubbed on disk, rewinds active_phase_id to that phase,
// reopening/creating its implement beads, and returns an explanatory error so the owner (polecat)
// can repair the files. Returns "" and nil when no rewind is needed.
func rewindProblemPhases(townRoot, rig string, v WorkflowValidation, mode rewindProblemPhaseMode) (string, error) {
	if townRoot == "" || rig == "" {
		return "", nil
	}
	if !v.HasPhasedDelivery() {
		return "", nil
	}
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	activeIdx := v.ActivePhaseIndex()

	// Find the earliest phase in scope whose required files are not all present and non-stubbed.
	var targetPhase string
	var problemFiles []string
	for i, p := range v.DeliveryPhases {
		if mode == rewindPastAndPresentOnly && (activeIdx < 0 || i >= activeIdx) {
			continue // skip the active phase and any future phases in QA mode
		}
		phaseFiles := normalizePathList(p.RequiredFiles)
		if len(phaseFiles) == 0 {
			continue
		}
		phaseScope := v
		phaseScope.ActivePhaseIDField = strings.TrimSpace(p.ID)
		phaseScope.RequiredFiles = phaseFiles

		if err := ValidateRequiredFilesNotStubbed(rigDir, phaseScope); err != nil {
			targetPhase = phaseScope.ActivePhaseIDField
			for _, rel := range phaseFiles {
				rel = filepath.ToSlash(strings.TrimSpace(rel))
				if rel == "" {
					continue
				}
				path := ResolveRequiredFileOnDisk(rigDir, rel, v.LayoutRoot)
				opts := StubCheckOptionsFromValidation(phaseScope)
				if cerr := CheckPathNotStub(path, rel, optsForPath(rel, opts)); cerr != nil {
					problemFiles = append(problemFiles, rel)
				}
			}
			break
		}
	}

	if targetPhase == "" {
		return "", nil
	}

	// Save the phase we're rewinding FROM so advancement can jump back
	// instead of progressing sequentially through intermediate phases.
	originalPhase := v.ActivePhaseID()
	logLine, err := performRewindToPhase(townRoot, rig, targetPhase, originalPhase, problemFiles)
	if err != nil {
		return "", err
	}
	return logLine, fmt.Errorf("%s. Implement and close these beads before the workflow can advance", logLine)
}

// performRewindToPhase rewinds active_phase_id to targetPhase, reopens/creates its implement
// beads, and records a rewound-from marker so phase advancement jumps back to originalPhase once
// the repair phase completes. Shared by the QA, final-phase, and implementation-failure rewinds.
// Returns a human-readable log line describing what was rewound and which beads were opened.
func performRewindToPhase(townRoot, rig, targetPhase, originalPhase string, problemFiles []string) (string, error) {
	if originalPhase != "" && originalPhase != targetPhase {
		_ = SetRigRewoundFromPhase(townRoot, rig, originalPhase)
	}

	if err := SetRigActivePhase(townRoot, rig, targetPhase); err != nil {
		return "", fmt.Errorf("validation found issues in %s but could not rewind active phase: %w", targetPhase, err)
	}

	reloaded, _, err := LoadRigWorkflowProfileFile(townRoot, rig)
	if err != nil {
		return "", fmt.Errorf("rewound active phase to %s but failed to reload profile: %w", targetPhase, err)
	}

	reopenedOrCreated, err := EnsurePlanningImplementBeads(townRoot, rig, reloaded.ForActivePhase())
	if err != nil {
		return "", fmt.Errorf("rewound active phase to %s but failed to ensure implement beads: %w", targetPhase, err)
	}

	logLine := fmt.Sprintf("rewound active phase to %s for missing/stubbed files: %s", targetPhase, strings.Join(problemFiles, ", "))
	if len(reopenedOrCreated) > 0 {
		logLine += fmt.Sprintf("; reopened/created beads: %s", strings.Join(reopenedOrCreated, ", "))
	}

	_ = commitDoltWorkingSet(townRoot, rig) // best-effort commit of bead state changes

	return logLine, nil
}

// MaybeRewindToProblemPhaseForImplementation is the implementation-failure safety net that runs
// when the polecat cannot complete the active phase. It uses the tested
// RequiredFilesForCompletedAndActive() to build the candidate file set (all completed phases plus
// the active phase). When an earlier completed phase's required file is missing or stubbed on
// disk while a later phase depends on it — so the active phase's build literally cannot progress
// (e.g. an api handlers.go imports a store package whose schema.go/store.go were never written) —
// it rewinds active_phase_id to the earliest problematic completed phase and returns an
// explanatory error so the caller can route the polecat back to implementation to actually write
// the missing files. Returns "" and nil when there is no completed-phase gap.
func MaybeRewindToProblemPhaseForImplementation(townRoot, rig string, v WorkflowValidation) (string, error) {
	if townRoot == "" || rig == "" {
		return "", nil
	}
	if !v.HasPhasedDelivery() {
		return "", nil
	}
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	activeIdx := v.ActivePhaseIndex()
	if activeIdx < 0 {
		return "", nil
	}

	// Candidates across completed + active phases (precisely the tested list).
	candidates := normalizePathList(v.RequiredFilesForCompletedAndActive())

	// Only rewind to a phase strictly earlier than the active phase: a missing file owned by the
	// active phase itself is the polecat's normal job, not a completed-phase rewind.
	firstProblem := -1
	var problemFiles []string
	for _, rel := range candidates {
		if rel == "" {
			continue
		}
		idx := v.FindDeliveryPhaseForFile(rel)
		if idx < 0 || idx >= activeIdx {
			continue
		}
		path := ResolveRequiredFileOnDisk(rigDir, rel, v.LayoutRoot)
		opts := StubCheckOptionsFromValidation(v)
		if cerr := CheckPathNotStub(path, rel, optsForPath(rel, opts)); cerr != nil {
			if firstProblem < 0 {
				firstProblem = idx
			}
			problemFiles = append(problemFiles, rel)
		}
	}
	if firstProblem < 0 {
		return "", nil
	}

	targetPhase := strings.TrimSpace(v.DeliveryPhases[firstProblem].ID)
	originalPhase := v.ActivePhaseID()
	logLine, err := performRewindToPhase(townRoot, rig, targetPhase, originalPhase, problemFiles)
	if err != nil {
		return "", err
	}
	return logLine, fmt.Errorf("%s. Implement and close these beads before the workflow can advance", logLine)
}

// MaybeRewindToProblemPhaseForFinalPhase runs full-project artifact validation when the active
// delivery phase is the final one. If any required file from an earlier phase is missing or
// stubbed, it rewinds active_phase_id to the earliest phase containing issues, reopens or creates
// implement beads for those files, and returns an explanatory error so the polecat can fix them.
// Returns an empty string and nil error when no rewind is needed.
func MaybeRewindToProblemPhaseForFinalPhase(townRoot, rig string, v WorkflowValidation) (string, error) {
	if townRoot == "" || rig == "" {
		return "", nil
	}
	if !v.HasPhasedDelivery() || !v.IsFinalDeliveryPhase() {
		return "", nil
	}
	return rewindProblemPhases(townRoot, rig, v, rewindAllPhases)
}

// MaybeRewindToProblemPhaseForQA runs phased-artifact validation on the completed phases only
// (those earlier than the active phase). If any completed phase has a required file that is
// missing or stubbed on disk, it rewinds active_phase_id to the earliest problematic completed
// phase, reopening/creating its implement beads, and returns an explanatory error so the polecat
// can write the files before the workflow advances. Returns "" and nil when no rewind is needed.
func MaybeRewindToProblemPhaseForQA(townRoot, rig string, v WorkflowValidation) (string, error) {
	if townRoot == "" || rig == "" {
		return "", nil
	}
	if !v.HasPhasedDelivery() {
		return "", nil
	}
	return rewindProblemPhases(townRoot, rig, v, rewindPastAndPresentOnly)
}
