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
	if orchestrator.IsRigWorkflowPaused(townRoot, rigName) {
		return true, "skipped (workflow paused)"
	}
	if reason := orchestrator.SkipRigAgentStartReason(townRoot, rigName); reason != "" {
		return true, "skipped (" + reason + ")"
	}
	return false, ""
}

// stopOrchestratedRigAgentsForPausedWorkflow stops pipeline agents when a rig workflow is paused.
// gt up reconcile and pause both rely on this so AutoRespawn sessions do not keep polling.
func stopOrchestratedRigAgentsForPausedWorkflow(townRoot, rigName string) {
	if townRoot == "" || rigName == "" || !orchestrator.IsRigWorkflowPaused(townRoot, rigName) {
		return
	}
	if !upQuiet {
		fmt.Fprintf(os.Stderr, "%s Stopping rig %s agents (workflow paused)\n",
			style.Warning.Render("!"), style.Bold.Render(rigName))
	}
	if err := shutdownRigAgents(townRoot, rigName, true); err != nil && !upQuiet {
		fmt.Fprintf(os.Stderr, "%s stop paused rig %s: %v\n", style.Warning.Render("!"), rigName, err)
	}
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

// EnsureOrchestratedTownPipeline starts hq-setup when the orchestrator is running.
// project_setup work is fetched by hq-setup — it must not stay stopped while wf is running.
// Planner is now rig-level and is started via ensureOrchestratedRigAgentsRunning.
func EnsureOrchestratedTownPipeline(townRoot string) {
	orchRunning, _, _ := orchestrator.IsRunning(townRoot)
	if !orchRunning {
		return
	}
	_ = upStartSetup(townRoot)
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
	if !orchestrator.PipelineOnlyEnabled(townRoot, false) {
		_ = upStartWitness(rigName, r)
		_ = upStartRefinery(rigName, r)
	}

	sp := session.GetDefaultProvider(townRoot)
	ctx := context.Background()
	prefix := session.PrefixFor(rigName)

	archID := session.ArchitectSessionName(prefix, rigName)
	wantArch := orchestrator.OrchestratedForRole(true, constants.RoleArchitect)
	upEnsureFreshPipelineSession(ctx, sp, townRoot, archID, wantArch)
	_ = upStartArchitect(rigName, r)

	plannerID := session.PlannerSessionName(prefix, rigName)
	wantPlanner := orchestrator.OrchestratedForRole(true, constants.RolePlanner)
	upEnsureFreshPipelineSession(ctx, sp, townRoot, plannerID, wantPlanner)
	_ = upStartPlanner(rigName, r)

	analystID := session.AnalystSessionName(prefix, rigName)
	wantAnalyst := orchestrator.OrchestratedForRole(true, "analyst")
	upEnsureFreshPipelineSession(ctx, sp, townRoot, analystID, wantAnalyst)
	_ = upStartAnalyst(rigName, r)

	qaID := session.QASessionName(prefix, rigName)
	wantQA := orchestrator.OrchestratedForRole(true, constants.RoleQA)
	upEnsureFreshPipelineSession(ctx, sp, townRoot, qaID, wantQA)
	_ = upStartQA(rigName, r)

	poleID := session.RigPolecatSessionName(prefix, rigName)
	wantPole := orchestrator.OrchestratedForRole(true, constants.RolePolecat)
	upEnsureFreshPipelineSession(ctx, sp, townRoot, poleID, wantPole)
	_ = upStartRigPolecat(rigName, r)
}
