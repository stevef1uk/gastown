package orchestrator

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/doltserver"
	"github.com/steveyegge/gastown/internal/rig"
)

const (
	rigLabelDocked = "status:docked"
	rigLabelParked = "status:parked"
)

// EnsureRigBeadOperationalForWorkflow ensures the rig identity bead exists and is not
// docked/parked while a rig-flow workflow is running (idempotent).
func EnsureRigBeadOperationalForWorkflow(townRoot, rigName string) (string, error) {
	if townRoot == "" || rigName == "" {
		return "", nil
	}
	rigPath := filepath.Join(townRoot, rigName)
	prefix := config.GetRigPrefix(townRoot, rigName)
	gitURL := ""
	if rigCfg, err := rig.LoadRigConfig(rigPath); err == nil && rigCfg != nil {
		if rigCfg.Beads != nil && strings.TrimSpace(rigCfg.Beads.Prefix) != "" {
			prefix = rigCfg.Beads.Prefix
		}
		if strings.TrimSpace(rigCfg.GitURL) != "" {
			gitURL = rigCfg.GitURL
		}
	}
	beadsDir := doltserver.FindRigBeadsDir(townRoot, rigName)
	if beadsDir == "" {
		beadsDir = beads.ResolveBeadsDir(rigPath)
	}
	bd := beads.New(beadsDir)
	fields := &beads.RigFields{
		Repo:   gitURL,
		Prefix: prefix,
		State:  beads.RigStateActive,
	}
	issue, err := bd.EnsureRigBead(rigName, fields)
	if err != nil {
		return "", fmt.Errorf("ensure rig bead: %w", err)
	}
	var removed []string
	for _, label := range []string{rigLabelDocked, rigLabelParked} {
		if !hasLabel(issue.Labels, label) {
			continue
		}
		if err := bd.Update(issue.ID, beads.UpdateOptions{RemoveLabels: []string{label}}); err != nil {
			return "", fmt.Errorf("remove %s from rig bead: %w", label, err)
		}
		removed = append(removed, label)
	}
	if len(removed) == 0 {
		return "", nil
	}
	return "cleared rig labels: " + strings.Join(removed, ", "), nil
}

func hasLabel(labels []string, want string) bool {
	for _, l := range labels {
		if l == want {
			return true
		}
	}
	return false
}
