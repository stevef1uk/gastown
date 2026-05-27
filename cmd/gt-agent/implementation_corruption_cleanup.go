package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

var nativeCorruptionMarkers = []string{
	"<<<<<<< SEARCH",
	">>>>>>> REPLACE",
	"<<<<<<<",
	"=======",
	">>>>>>>",
	"---END WRITE---",
	"---END EDIT---",
}

func containsNativeCorruptionMarker(s string) bool {
	for _, m := range nativeCorruptionMarkers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

func goLooksCorruptedForRewrite(data []byte) bool {
	text := string(data)
	if containsNativeCorruptionMarker(text) {
		return true
	}
	if orchestrator.ImplementGoBytesCorrupted(data) {
		return true
	}
	head := text
	if len(head) > 200 {
		head = head[:200]
	}
	return !strings.Contains(head, "package ")
}

func pythonLooksCorruptedForRewrite(data []byte, relPath string) bool {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		// Keep empty .py files (module markers / optional placeholders).
		return false
	}
	if containsNativeCorruptionMarker(trimmed) {
		return true
	}
	return orchestrator.CheckPythonSourceValid(data, relPath) != nil
}

// cleanupCorruptedOpenImplementBeadFiles deletes marker/syntax-corrupted files for
// open/in_progress implement beads so polecat can WRITE them fresh.
func (r *stateRunner) cleanupCorruptedOpenImplementBeadFiles() ([]string, error) {
	if r == nil || r.townRoot == "" || r.rig == "" {
		return nil, nil
	}
	if !orchestrator.BeadsDatabaseReady(r.townRoot, r.rig) && orchestrator.ListImplementBeadsByStatusHook == nil {
		return nil, nil
	}
	beads, err := orchestrator.ListImplementBeadsOpenOrInProgress(r.townRoot, r.rig, r.v)
	if err != nil {
		return nil, err
	}
	rigDir := rigMayorRigDir(r.townRoot, r.rig)
	var deleted []string
	for _, b := range beads {
		rel := orchestrator.NormalizeBeadPathForLayout(
			orchestrator.ExtractPathFromBeadTitle(b.Title, r.v.BeadTitleContains), r.v.LayoutRoot)
		if rel == "" {
			continue
		}
		lower := strings.ToLower(rel)
		if !strings.HasSuffix(lower, ".go") && !strings.HasSuffix(lower, ".py") {
			continue
		}
		abs := filepath.Join(rigDir, filepath.FromSlash(rel))
		data, readErr := os.ReadFile(abs)
		if readErr != nil {
			continue
		}
		corrupted := false
		if strings.HasSuffix(lower, ".go") {
			corrupted = goLooksCorruptedForRewrite(data)
		} else if strings.HasSuffix(lower, ".py") {
			corrupted = pythonLooksCorruptedForRewrite(data, rel)
		}
		if !corrupted {
			continue
		}
		if err := os.Remove(abs); err != nil {
			return deleted, err
		}
		deleted = append(deleted, rel)
	}
	return deleted, nil
}
