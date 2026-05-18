package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/constants"
	"github.com/steveyegge/gastown/internal/git"
	"github.com/steveyegge/gastown/internal/orchestrator"
	"github.com/steveyegge/gastown/internal/rig"
	"github.com/steveyegge/gastown/internal/session"
	"github.com/steveyegge/gastown/internal/style"
)

// rigOrchestratorAgentSkip returns (true, detail) when orchestrator mode should not start rig agents.
func rigOrchestratorAgentSkip(townRoot, rigName, agentLabel string) (bool, string) {
	if reason := orchestrator.SkipRigAgentStartReason(townRoot, rigName); reason != "" {
		return true, "skipped (" + reason + ")"
	}
	return false, ""
}

// loadRigAtTownRoot loads a rig by name when the town root is already known.
func loadRigAtTownRoot(townRoot, rigName string) (*rig.Rig, error) {
	rigsConfigPath := constants.MayorRigsPath(townRoot)
	rigsConfig, err := config.LoadRigsConfig(rigsConfigPath)
	if err != nil {
		rigsConfig = &config.RigsConfig{Rigs: make(map[string]config.RigEntry)}
	}
	g := git.NewGit(townRoot)
	rigMgr := rig.NewManager(townRoot, rigsConfig, g)
	return rigMgr.GetRig(rigName)
}

// EnsureOrchestratedRigAgents starts witness/refinery and rig-flow pipeline agents for a rig
// with a running workflow. Call after workflow start/resume or from gt up reconcile.
func EnsureOrchestratedRigAgents(townRoot, rigName string) {
	if orchestrator.SkipRigAgentStartReason(townRoot, rigName) != "" {
		return
	}
	orchRunning, _, _ := orchestrator.IsRunning(townRoot)
	if !orchRunning {
		return
	}
	r, err := loadRigAtTownRoot(townRoot, rigName)
	if err != nil {
		if !upQuiet {
			fmt.Fprintf(os.Stderr, "%s EnsureOrchestratedRigAgents %s: %v\n", style.Warning.Render("!"), rigName, err)
		}
		return
	}
	ensureOrchestratedRigAgentsRunning(townRoot, rigName, r)
}

func ensureOrchestratedRigAgentsRunning(townRoot, rigName string, r *rig.Rig) {
	_ = upStartWitness(rigName, r)
	_ = upStartRefinery(rigName, r)

	sp := session.GetDefaultProvider(townRoot)
	ctx := context.Background()
	prefix := session.PrefixFor(rigName)

	archID := session.ArchitectSessionName(prefix, rigName)
	wantArch := orchestrator.OrchestratedForRole(true, constants.RoleArchitect)
	upEnsureFreshPipelineSession(ctx, sp, townRoot, archID, wantArch)
	_ = upStartArchitect(rigName, r)

	qaID := session.QASessionName(prefix, rigName)
	wantQA := orchestrator.OrchestratedForRole(true, constants.RoleQA)
	upEnsureFreshPipelineSession(ctx, sp, townRoot, qaID, wantQA)
	_ = upStartQA(rigName, r)

	poleID := session.RigPolecatSessionName(prefix, rigName)
	wantPole := orchestrator.OrchestratedForRole(true, constants.RolePolecat)
	upEnsureFreshPipelineSession(ctx, sp, townRoot, poleID, wantPole)
	_ = upStartRigPolecat(rigName, r)
}
