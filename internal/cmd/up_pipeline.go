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
	if sp == nil || sessionID == "" {
		return false
	}
	running, err := sp.Exists(ctx, sessionID)
	if err != nil || !running {
		return false
	}
	if !session.PipelineSessionNeedsRestart(ctx, sp, townRoot, sessionID, wantOrchestrated) {
		return false
	}
	_ = sp.Stop(ctx, sessionID, false)
	if !upQuiet {
		fmt.Fprintf(os.Stderr, "%s Restarting pipeline session %s (gt-agent dead or not orchestrated)\n",
			style.Warning.Render("!"), sessionID)
	}
	return true
}

// reconcileOrchestratedPipelineAgents re-checks rig-flow pipeline sessions after the
// orchestrator has started. Catches the race where planner/polecat started before
// orchestrator.IsRunning was true, or agents that exited mid-workflow.
func reconcileOrchestratedPipelineAgents(townRoot string, rigNames []string, prefetched map[string]*rig.Rig) {
	orchRunning, _, _ := orchestrator.IsRunning(townRoot)
	if !orchRunning {
		return
	}
	sp := session.GetDefaultProvider(townRoot)
	ctx := context.Background()

	wantMayor := orchestrator.OrchestratedForRole(true, constants.RoleMayor)
	if upEnsureFreshPipelineSession(ctx, sp, townRoot, session.MayorSessionName(), wantMayor) {
		mayorMgr := mayor.NewManager(townRoot)
		if err := mayorMgr.Start("", wantMayor); err != nil && !errors.Is(err, mayor.ErrAlreadyRunning) {
			fmt.Fprintf(os.Stderr, "%s mayor restart after reconcile: %v\n", style.Warning.Render("!"), err)
		}
	}

	wantPlanner := orchestrator.OrchestratedForRole(true, constants.RolePlanner)
	if upEnsureFreshPipelineSession(ctx, sp, townRoot, session.PlannerSessionName(), wantPlanner) {
		_ = upStartPlanner(townRoot)
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
		if orchestrator.SkipRigAgentStartReason(townRoot, rigName) != "" {
			continue
		}
		prefix := session.PrefixFor(rigName)

		archID := session.ArchitectSessionName(prefix, rigName)
		wantArch := orchestrator.OrchestratedForRole(true, constants.RoleArchitect)
		if upEnsureFreshPipelineSession(ctx, sp, townRoot, archID, wantArch) {
			_ = upStartArchitect(rigName, r)
		}

		qaID := session.QASessionName(prefix, rigName)
		wantQA := orchestrator.OrchestratedForRole(true, constants.RoleQA)
		if upEnsureFreshPipelineSession(ctx, sp, townRoot, qaID, wantQA) {
			_ = upStartQA(rigName, r)
		}

		poleID := session.RigPolecatSessionName(prefix, rigName)
		wantPole := orchestrator.OrchestratedForRole(true, constants.RolePolecat)
		if upEnsureFreshPipelineSession(ctx, sp, townRoot, poleID, wantPole) {
			_ = upStartRigPolecat(rigName, r)
		}
	}
}

func beaconTopicForOrchestrated(orchestrated bool) string {
	if orchestrated {
		return "orchestrated"
	}
	return "startup"
}
