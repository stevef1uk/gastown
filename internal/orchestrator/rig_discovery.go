package orchestrator

import (
	"os"
	"path/filepath"

	"github.com/steveyegge/gastown/internal/config"
)

// DiscoverTownPolecatRig resolves the rig for legacy town hq-polecat when GT_RIG is unset.
// Precedence: envRig, identityRig, sole town rig, active workflow instance rig var.
func DiscoverTownPolecatRig(townRoot, envRig, identityRig string) string {
	if envRig != "" {
		return envRig
	}
	if identityRig != "" {
		return identityRig
	}
	rigs := discoverTownRigs(townRoot)
	if len(rigs) == 1 {
		return rigs[0]
	}
	return rigFromActiveWorkflow(townRoot)
}

func discoverTownRigs(townRoot string) []string {
	rigsConfigPath := filepath.Join(townRoot, "mayor", "rigs.json")
	if rigsConfig, err := config.LoadRigsConfig(rigsConfigPath); err == nil {
		names := make([]string, 0, len(rigsConfig.Rigs))
		for name := range rigsConfig.Rigs {
			names = append(names, name)
		}
		return names
	}

	var rigs []string
	entries, err := os.ReadDir(townRoot)
	if err != nil {
		return rigs
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == "mayor" || name == "daemon" || name == "deacon" ||
			name == ".git" || name == "docs" || name == "polecat" || name[0] == '.' {
			continue
		}
		dirPath := filepath.Join(townRoot, name)
		if _, err := os.Stat(filepath.Join(dirPath, ".beads")); err == nil {
			rigs = append(rigs, name)
			continue
		}
		if _, err := os.Stat(filepath.Join(dirPath, "polecats")); err == nil {
			rigs = append(rigs, name)
		}
	}
	return rigs
}

func rigFromActiveWorkflow(townRoot string) string {
	snap, err := LoadInstancesSnapshot(townRoot)
	if err != nil {
		return ""
	}
	for _, inst := range snap.Instances {
		if inst == nil {
			continue
		}
		if inst.Status == "completed" || inst.Status == "failed" {
			continue
		}
		if rig := inst.Variables["rig"]; rig != "" {
			return rig
		}
	}
	return ""
}
