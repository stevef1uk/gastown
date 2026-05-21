package main

import (
	"fmt"
	"os"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func validateImplementationMissingFileRead(cmd, townRoot, rig, activeBead, activeBeadPath string, v orchestrator.WorkflowValidation) error {
	for _, rel := range orchestrator.ExtractImplementReadPathsFromCmd(cmd, v.LayoutRoot) {
		if err := orchestrator.ValidateImplementReadMissingFile(townRoot, rig, activeBead, activeBeadPath, rel, v); err != nil {
			return err
		}
	}
	return nil
}

func (r *stateRunner) validateImplementationMissingFileRead(cmd string) error {
	if r == nil {
		return nil
	}
	return validateImplementationMissingFileRead(cmd, r.townRoot, r.rig, r.track.activeBead, r.track.activeBeadPath, r.v)
}

func (r *stateRunner) ensureTestBeadSkeletonAfterInProgress(cmd string) {
	if r == nil || !isBeadUpdateInProgressCommand(cmd) {
		return
	}
	id := extractBeadIDFromBdUpdate(cmd)
	if id == "" {
		return
	}
	path, created, err := orchestrator.EnsureTestBeadSkeleton(r.townRoot, r.rig, id, r.v)
	if err != nil {
		orchestratedFprintfStderr("[gt-agent] test skeleton: %v\n", err)
		return
	}
	if created {
		orchestratedPrintf("[gt-agent] created test skeleton at %s\n", path)
	}
}

func nativeReadMissingFileError(townRoot, rig, activeBead, activeBeadPath, relPath string, v orchestrator.WorkflowValidation, readErr error) error {
	if readErr == nil || !os.IsNotExist(readErr) {
		return readErr
	}
	nudge := orchestrator.ImplementMissingFileReadNudge(townRoot, rig, activeBead, activeBeadPath, relPath, v)
	if nudge == "" {
		return readErr
	}
	return fmt.Errorf("%s: %w", nudge, readErr)
}
