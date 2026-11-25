package orchestrator

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// EnsurePhaseBoundaryIsolation ensures that files belonging exclusively to future delivery
// phases do not contaminate the active phase's worktree (e.g. future *_test.go files causing
// phase verification commands to fail prematurely before features are implemented).
// When future phase files are found on disk, they are moved into .gastown/phase-stash/<phaseID>/.
// When the workflow advances to a phase, any files previously stashed for that phase are restored.
func EnsurePhaseBoundaryIsolation(townRoot, rig string, v WorkflowValidation) (isolated []string, restored []string, err error) {
	if townRoot == "" || rig == "" || !v.HasPhasedDelivery() {
		return nil, nil, nil
	}
	activeIdx := v.ActivePhaseIndex()
	if activeIdx < 0 {
		return nil, nil, nil
	}

	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	stashBase := filepath.Join(rigDir, ".gastown", "phase-stash")
	activePhase := v.DeliveryPhases[activeIdx]

	// 1. Restore any files stashed for the active delivery phase.
	activeStashDir := filepath.Join(stashBase, activePhase.ID)
	if info, statErr := os.Stat(activeStashDir); statErr == nil && info.IsDir() {
		walkErr := filepath.WalkDir(activeStashDir, func(path string, d fs.DirEntry, werr error) error {
			if werr != nil || d.IsDir() {
				return werr
			}
			rel, relErr := filepath.Rel(activeStashDir, path)
			if relErr != nil {
				return relErr
			}
			dest := filepath.Join(rigDir, rel)
			if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
				return err
			}
			if err := os.Rename(path, dest); err != nil {
				// Fallback to copy+remove if rename fails across devices
				data, readErr := os.ReadFile(path)
				if readErr != nil {
					return readErr
				}
				if writeErr := os.WriteFile(dest, data, 0644); writeErr != nil {
					return writeErr
				}
				_ = os.Remove(path)
			}
			restored = append(restored, filepath.ToSlash(rel))
			return nil
		})
		if walkErr != nil {
			return isolated, restored, walkErr
		}
		// Clean up empty directories in stash
		_ = os.RemoveAll(activeStashDir)
	}

	// 2. Identify all files required by completed and current phases.
	currentAndPastFiles := make(map[string]bool)
	for i := 0; i <= activeIdx; i++ {
		for _, f := range v.DeliveryPhases[i].RequiredFiles {
			norm := NormalizeBeadPathForLayout(filepath.ToSlash(strings.TrimSpace(f)), v.LayoutRoot)
			if norm != "" {
				currentAndPastFiles[norm] = true
			}
		}
	}

	// 3. Stash any files belonging exclusively to future phases that exist on disk.
	for i := activeIdx + 1; i < len(v.DeliveryPhases); i++ {
		futurePhase := v.DeliveryPhases[i]
		for _, f := range futurePhase.RequiredFiles {
			norm := NormalizeBeadPathForLayout(filepath.ToSlash(strings.TrimSpace(f)), v.LayoutRoot)
			if norm == "" || currentAndPastFiles[norm] {
				continue
			}
			src := filepath.Join(rigDir, filepath.FromSlash(norm))
			if info, statErr := os.Stat(src); statErr == nil && !info.IsDir() {
				dest := filepath.Join(stashBase, futurePhase.ID, filepath.FromSlash(norm))
				if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
					return isolated, restored, err
				}
				if err := os.Rename(src, dest); err != nil {
					data, readErr := os.ReadFile(src)
					if readErr != nil {
						return isolated, restored, readErr
					}
					if writeErr := os.WriteFile(dest, data, 0644); writeErr != nil {
						return isolated, restored, writeErr
					}
					_ = os.Remove(src)
				}
				isolated = append(isolated, norm)
			}
		}
	}

	return isolated, restored, nil
}

// EnsurePhaseBoundaryIsolationLog runs EnsurePhaseBoundaryIsolation and returns a summary message.
func EnsurePhaseBoundaryIsolationLog(townRoot, rig string, v WorkflowValidation) (string, error) {
	isolated, restored, err := EnsurePhaseBoundaryIsolation(townRoot, rig, v)
	if err != nil {
		return "", err
	}
	var parts []string
	if len(isolated) > 0 {
		parts = append(parts, fmt.Sprintf("isolated %d future-phase file(s): %s", len(isolated), strings.Join(isolated, ", ")))
	}
	if len(restored) > 0 {
		parts = append(parts, fmt.Sprintf("restored %d active-phase file(s): %s", len(restored), strings.Join(restored, ", ")))
	}
	if len(parts) == 0 {
		return "phase boundary isolation: clean", nil
	}
	return "phase boundary isolation: " + strings.Join(parts, "; "), nil
}
