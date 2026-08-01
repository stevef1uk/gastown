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
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")

	// Find the earliest phase whose required files are not all present and non-stubbed.
	var targetPhase string
	var problemFiles []string
	for _, p := range v.DeliveryPhases {
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
	if originalPhase != "" && originalPhase != targetPhase {
		_ = SetRigRewoundFromPhase(townRoot, rig, originalPhase)
	}

	if err := SetRigActivePhase(townRoot, rig, targetPhase); err != nil {
		return "", fmt.Errorf("final-phase validation found issues in %s but could not rewind active phase: %w", targetPhase, err)
	}

	reloaded, _, err := LoadRigWorkflowProfileFile(townRoot, rig)
	if err != nil {
		return "", fmt.Errorf("rewound active phase to %s but failed to reload profile: %w", targetPhase, err)
	}

	reopenedOrCreated, err := EnsurePlanningImplementBeads(townRoot, rig, reloaded.ForActivePhase())
	if err != nil {
		return "", fmt.Errorf("rewound active phase to %s but failed to ensure implement beads: %w", targetPhase, err)
	}

	logLine := fmt.Sprintf("final-phase validation failed; rewound active phase to %s for missing/stubbed files: %s", targetPhase, strings.Join(problemFiles, ", "))
	if len(reopenedOrCreated) > 0 {
		logLine += fmt.Sprintf("; reopened/created beads: %s", strings.Join(reopenedOrCreated, ", "))
	}

	_ = commitDoltWorkingSet(townRoot, rig) // best-effort commit of bead state changes

	return logLine, fmt.Errorf("%s. Implement and close these beads before the final phase can complete", logLine)
}
