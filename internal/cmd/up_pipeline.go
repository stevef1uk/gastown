package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/steveyegge/gastown/internal/constants"
	"github.com/steveyegge/gastown/internal/mayor"
	"github.com/steveyegge/gastown/internal/orchestrator"
	"github.com/steveyegge/gastown/internal/rig"
	"github.com/steveyegge/gastown/internal/session"
	"github.com/steveyegge/gastown/internal/style"
)

// upEnsureFreshPipelineSession stops a pipeline session when gt-agent is dead or missing
// --orchestrated while the orchestrator is active. Returns true if the session was stopped.
func upEnsureFreshPipelineSession(ctx context.Context, sp session.Provider, townRoot, sessionID string, wantOrchestrated bool) bool {
	stopped := session.RestartStalePipelineSession(ctx, sp, townRoot, sessionID, wantOrchestrated)
	if stopped && !upQuiet {
		fmt.Fprintf(os.Stderr, "%s Restarting pipeline session %s (gt-agent dead or not orchestrated)\n",
			style.Warning.Render("!"), sessionID)
	}
	return stopped
}

// reconcileOrchestratedPipelineAgents re-checks rig-flow pipeline sessions after the
// orchestrator has started. Catches the race where planner/polecat started before
// orchestrator.IsRunning was true, or agents that exited mid-workflow.
func reconcileOrchestratedPipelineAgents(townRoot string, rigNames []string, prefetched map[string]*rig.Rig) {
	orchRunning, _, _ := orchestrator.IsRunning(townRoot)
	if !orchRunning {
		return
	}
	EnsureOrchestratedTownPipeline(townRoot)
	sp := session.GetDefaultProvider(townRoot)
	ctx := context.Background()

	wantMayor := orchestrator.OrchestratedForRole(true, constants.RoleMayor)
	if upEnsureFreshPipelineSession(ctx, sp, townRoot, session.MayorSessionName(), wantMayor) {
		mayorMgr := mayor.NewManager(townRoot)
		if err := mayorMgr.Start("", wantMayor); err != nil && !errors.Is(err, mayor.ErrAlreadyRunning) {
			fmt.Fprintf(os.Stderr, "%s mayor restart after reconcile: %v\n", style.Warning.Render("!"), err)
		}
	}



	wantSetup := orchestrator.OrchestratedForRole(true, constants.RoleSetup)
	if upEnsureFreshPipelineSession(ctx, sp, townRoot, session.SetupSessionName(), wantSetup) {
		_ = upStartSetup(townRoot)
	}

	for _, rigName := range rigNames {
		r := prefetched[rigName]
		if r == nil {
			continue
		}
		if orchestrator.IsRigWorkflowPaused(townRoot, rigName) {
			stopOrchestratedRigAgentsForPausedWorkflow(townRoot, rigName)
			continue
		}
		if orchestrator.SkipRigAgentStartReason(townRoot, rigName) != "" {
			continue
		}
		ensureOrchestratedRigAgentsRunning(townRoot, rigName, r)
	}
}

func beaconTopicForOrchestrated(orchestrated bool) string {
	if orchestrated {
		return "orchestrated"
	}
	return "startup"
}
