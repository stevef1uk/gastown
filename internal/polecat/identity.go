package polecat

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RepairIdentityFiles rewrites .gt-agent in every polecat worktree under a rig.
// Returns the number of worktrees updated.
func RepairIdentityFiles(rigPath, rigName string) (int, error) {
	polecatsDir := filepath.Join(rigPath, "polecats")
	entries, err := os.ReadDir(polecatsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	updated := 0
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		name := entry.Name()
		workDir := filepath.Join(polecatsDir, name, rigName)
		if _, err := os.Stat(workDir); err != nil {
			workDir = filepath.Join(polecatsDir, name)
		}
		if err := writePolecatIdentityFile(workDir, rigName, name); err != nil {
			return updated, fmt.Errorf("polecat %s: %w", name, err)
		}
		updated++
	}
	return updated, nil
}
