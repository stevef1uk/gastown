// Package townconfig syncs embedded runtime assets into a Gas Town HQ.
package townconfig

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/steveyegge/gastown/internal/formula"
	"github.com/steveyegge/gastown/internal/orchestrator"
)

// SyncResult summarizes files refreshed during Sync.
type SyncResult struct {
	Orchestrator orchestrator.SyncResult
	Formulas     FormulaSyncResult
}

// FormulaSyncResult counts formula files updated by Sync.
type FormulaSyncResult struct {
	Updated     int
	Skipped     int
	Reinstalled int
}

// Sync copies embedded orchestrator templates/prompts and refreshes formulas
// into townRoot. Orchestrator files that differ from the embedded source are
// always overwritten (same as gt orchestrator sync --update-changed).
// Formulas use the same safe-update rules as gt upgrade (skips user-modified).
func Sync(townRoot string) (*SyncResult, error) {
	if townRoot == "" {
		return nil, fmt.Errorf("town root is required")
	}
	if !isTownRoot(townRoot) {
		cfgPath := filepath.Join(townRoot, "config.json")
		return nil, fmt.Errorf("not a Gas Town HQ (missing %s or mayor/town.json)", cfgPath)
	}

	ores, err := orchestrator.SyncTownAssets(townRoot, true)
	if err != nil {
		return nil, fmt.Errorf("orchestrator sync: %w", err)
	}

	updated, skipped, reinstalled, err := formula.UpdateFormulas(townRoot)
	if err != nil {
		return nil, fmt.Errorf("formula sync: %w", err)
	}

	return &SyncResult{
		Orchestrator: ores,
		Formulas: FormulaSyncResult{
			Updated:     updated,
			Skipped:     skipped,
			Reinstalled: reinstalled,
		},
	}, nil
}

func isTownRoot(townRoot string) bool {
	for _, rel := range []string{
		"config.json",
		filepath.Join("mayor", "town.json"),
		filepath.Join("settings", "config.json"),
	} {
		if info, err := os.Stat(filepath.Join(townRoot, rel)); err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}
